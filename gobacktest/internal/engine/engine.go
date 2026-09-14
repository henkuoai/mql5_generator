package engine

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"

	"gobacktest/internal/data"
	"gobacktest/internal/model"
)

// Engine 回测引擎：多品种共享一个账户，按合并时间轴逐 bar 推进。
type Engine struct {
	cfg      Config
	symbols  map[string]model.Symbol
	data     map[string]*data.SymbolData
	ind      map[string]*data.Indicators
	strat    Strategy
	policy   ManagePolicy
	acct     *Account
	positions map[string]*Position
	pending  map[string]*Order
	trades   []*Trade
	equity   []EquityPoint
	rng      *rand.Rand
	seq      int
	peak     float64
	rejected int
	skipped  int
	lastEntry map[string]time.Time
	last      map[string]model.Bar // 各品种当前最新 bar（避免前视偏差）
}

// New 创建引擎
func New(cfg Config, syms map[string]model.Symbol, sd map[string]*data.SymbolData,
	inds map[string]*data.Indicators, strat Strategy) *Engine {
	if cfg.PathModel == "" {
		cfg.PathModel = "adverse"
	}
	if cfg.Leverage <= 0 {
		cfg.Leverage = 500
	}
	e := &Engine{
		cfg:       cfg,
		symbols:   syms,
		data:      sd,
		ind:       inds,
		strat:     strat,
		acct:      &Account{Balance: cfg.Balance, Equity: cfg.Balance, Leverage: cfg.Leverage},
		positions: map[string]*Position{},
		pending:   map[string]*Order{},
		lastEntry: map[string]time.Time{},
		last:      map[string]model.Bar{},
		rng:       rand.New(rand.NewSource(cfg.Seed)),
	}
	if strat != nil {
		e.policy = strat.Policy()
	}
	e.peak = cfg.Balance
	return e
}

type event struct {
	t   time.Time
	sym string
	idx int
}

// Run 执行回测
func (e *Engine) Run() *Result {
	var evs []event
	for name, d := range e.data {
		for i, b := range d.Base {
			evs = append(evs, event{t: b.Time, sym: name, idx: i})
		}
	}
	sort.Slice(evs, func(a, b int) bool {
		if evs[a].t.Equal(evs[b].t) {
			return evs[a].sym < evs[b].sym
		}
		return evs[a].t.Before(evs[b].t)
	})

	res := &Result{Config: e.cfg, Symbols: []string{}, StartBal: e.cfg.Balance}
	if e.strat != nil {
		res.Strategy = e.strat.Name()
	}

	for _, v := range evs {
		d := e.data[v.sym]
		if v.idx >= len(d.Base) {
			continue
		}
		bar := d.Base[v.idx]
		e.last[v.sym] = bar // 记录最新可成交价，供盯市与强平使用（无前视）
		if res.From.IsZero() {
			res.From = bar.Time
		}
		res.To = bar.Time

		// 1) 成交挂单（用本 bar 开盘价）
		e.fillPending(v.sym, bar, v.idx)

		// 2) 盘中路径撮合 + 持仓管理
		if p := e.positions[v.sym]; p != nil {
			e.managePosition(p, d, bar, v.idx)
		}

		// 3) 盯市 & 强平
		e.markToMarket(bar.Time)
		e.checkStopOut(bar.Time)

		// 4) 收盘后评估信号（用已收盘 bar），下单到下根 bar 开盘
		if e.strat != nil && e.positions[v.sym] == nil {
			ctx := e.buildContext(v.sym, d, v.idx, bar.Time)
			for _, o := range e.strat.OnBarClose(ctx) {
				if o.Symbol == "" {
					o.Symbol = v.sym
				}
				e.pending[o.Symbol] = &o
			}
		}

		e.equity = append(e.equity, EquityPoint{
			Time: bar.Time, Balance: e.acct.Balance, Equity: e.acct.Equity,
			Margin: e.acct.Margin, OpenPos: len(e.positions),
			Drawdown: e.drawdownPct(),
		})
	}

	// 收尾：按最后价格平掉所有持仓
	for name, p := range e.positions {
		bar, ok := e.last[name]
		if !ok {
			continue
		}
		e.closePosition(p, e.exitPx(p, bar, bar.Close, name), bar.Time, p.EntryIdx, "END")
	}
	e.markToMarket(res.To)

	res.Trades = e.trades
	res.Equity = e.equity
	res.EndBal = e.acct.Balance
	res.EndEquity = e.acct.Equity
	res.PeakEquity = e.peak
	res.MaxDD, res.MaxDDPct = e.maxDrawdown()
	res.Rejected = e.rejected
	res.Skipped = e.skipped
	return res
}

func (e *Engine) buildContext(sym string, d *data.SymbolData, baseIdx int, now time.Time) *Context {
	s := e.symbols[sym]
	ctx := &Context{
		Now: now, BaseIdx: baseIdx, Sym: s, SD: d, Ind: e.ind[sym],
		Balance: e.acct.Balance, Equity: e.acct.Equity,
		FreeMargin: e.acct.FreeMargin(), PeakEquity: e.peak,
		DDPercent:     e.drawdownPct(),
		HasPosition:   e.positions[sym] != nil,
		OpenPositions: len(e.positions),
		LastEntryAgoM: -1,
	}
	if t, ok := e.lastEntry[sym]; ok {
		ctx.LastEntryAgoM = now.Sub(t).Minutes()
	}
	return ctx
}

// fillPending 在 bar 开盘执行挂单
func (e *Engine) fillPending(sym string, bar model.Bar, idx int) {
	o, ok := e.pending[sym]
	if !ok {
		return
	}
	delete(e.pending, sym)
	if o.Lots <= 0 {
		e.skipped++
		return
	}
	if e.cfg.MaxPositions > 0 && len(e.positions) >= e.cfg.MaxPositions {
		e.skipped++
		return
	}
	if _, exists := e.positions[sym]; exists {
		e.skipped++
		return
	}
	s := e.symbols[sym]
	spread := e.barSpread(s, bar)

	// 成交价：买入吃卖价(ask)，卖出吃买价(bid)；价格序列是 bid
	px := bar.Open
	if o.Side == model.SideBuy {
		px += spread
	}
	px += o.Side.Sign() * e.cfg.SlippagePoints * s.Point
	px = s.RoundPrice(px)

	lots := s.NormalizeLots(o.Lots)
	if lots <= 0 {
		e.skipped++
		return
	}
	// 保证金检查
	need := s.MarginRequired(lots, px, e.cfg.Leverage)
	if need > e.acct.FreeMargin() {
		e.rejected++
		return
	}

	comm := lots * s.CommissionPerLot
	e.acct.Balance -= comm
	e.seq++

	// 止损/止盈：绝对价优先，否则按距离换算
	sl, tp := o.SL, o.TP
	if sl == 0 && o.SLDist > 0 {
		sl = px - o.Side.Sign()*o.SLDist
	}
	if tp == 0 && o.TPDist > 0 {
		tp = px + o.Side.Sign()*o.TPDist
	}
	if sl != 0 {
		sl = s.RoundPrice(sl)
	}
	if tp != 0 {
		tp = s.RoundPrice(tp)
	}

	p := &Position{
		ID: e.seq, Symbol: sym, Sym: s, Side: o.Side, Lots: lots,
		EntryIdx: idx, EntryT: bar.Time, EntryPx: px,
		SL: sl, InitSL: sl, TP: tp, RiskUSD: o.RiskUSD, Tag: o.Tag,
		BestPx: px, WorstPx: px, LastExtreme: idx,
		LastSwapDay: -1, CommissionIn: comm,
	}
	e.positions[sym] = p
	e.lastEntry[sym] = bar.Time
}

func (e *Engine) barSpread(s model.Symbol, bar model.Bar) float64 {
	if bar.Spread > 0 {
		return bar.Spread * s.Point
	}
	return s.SpreadPrice()
}

// pathPoints 生成盘中价格路径（价格 = Bid）
func (e *Engine) pathPoints(bar model.Bar, side model.Side) []float64 {
	o, h, l, c := bar.Open, bar.High, bar.Low, bar.Close
	switch e.cfg.PathModel {
	case "neutral":
		return dedup([]float64{o, h, l, c})
	case "random":
		if e.rng.Intn(2) == 0 {
			return dedup([]float64{o, h, l, c})
		}
		return dedup([]float64{o, l, h, c})
	default: // adverse：先走不利方向（保守）
		if side == model.SideBuy {
			return dedup([]float64{o, l, h, c})
		}
		return dedup([]float64{o, h, l, c})
	}
}

func dedup(in []float64) []float64 {
	out := in[:0:0]
	for i, v := range in {
		if i > 0 && math.Abs(v-out[len(out)-1]) < 1e-12 {
			continue
		}
		out = append(out, v)
	}
	return out
}

// managePosition 盘中撮合：止损 / 止盈 / 跟踪 / 停滞 / 超时
func (e *Engine) managePosition(p *Position, d *data.SymbolData, bar model.Bar, idx int) {
	s := p.Sym
	spread := e.barSpread(s, bar)
	pts := e.pathPoints(bar, p.Side)
	n := len(pts)
	if n == 0 {
		return
	}
	p.BarsHeld++

	for i, px := range pts {
		frac := 1.0
		if n > 1 {
			frac = float64(i) / float64(n-1)
		}
		// 该时刻的成交价
		execPx := px
		if p.Side == model.SideSell {
			execPx = px + spread // 空单平仓吃卖价
		}
		// 更新极值（Bid 序列）：BestPx 始终指"最有利"方向
		if p.Side == model.SideBuy {
			if px > p.BestPx {
				p.BestPx = px
				p.LastExtreme = idx
			}
			if px < p.WorstPx {
				p.WorstPx = px
			}
		} else {
			if px < p.BestPx {
				p.BestPx = px
				p.LastExtreme = idx
			}
			if px > p.WorstPx {
				p.WorstPx = px
			}
		}

		// 止损 / 止盈（保守：同一 tick 内先判止损）
		if p.SL > 0 {
			if p.Side == model.SideBuy && px <= p.SL {
				e.closePosition(p, p.SL, bar.Time, idx, "SL")
				return
			}
			if p.Side == model.SideSell && execPx >= p.SL {
				e.closePosition(p, p.SL, bar.Time, idx, "SL")
				return
			}
		}
		if p.TP > 0 {
			if p.Side == model.SideBuy && px >= p.TP {
				e.closePosition(p, p.TP, bar.Time, idx, "TP")
				return
			}
			if p.Side == model.SideSell && execPx <= p.TP {
				e.closePosition(p, p.TP, bar.Time, idx, "TP")
				return
			}
		}

		// 跟踪止损
		if e.policy.TrailStartUSD > 0 {
			startPx := e.toPriceUnits(p, d, idx, e.policy.TrailStartUSD)
			distPx := e.toPriceUnits(p, d, idx, e.policy.TrailDistUSD)
			favPx := favorPrice(p, px)
			if favPx >= startPx {
				p.TrailArmed = true
			}
			if p.TrailArmed {
				var lvl float64
				if p.Side == model.SideBuy {
					lvl = p.BestPx - distPx
					if lvl > p.SL {
						p.SL = s.RoundPrice(lvl)
					}
				} else {
					lvl = p.BestPx + distPx
					if lvl < p.SL || p.SL == 0 {
						p.SL = s.RoundPrice(lvl)
					}
				}
			}
		}

		// 停滞离场（按 bar 计数近似）
		if e.policy.StallMinutes > 0 {
			minProfitPx := e.toPriceUnits(p, d, idx, e.policy.StallMinProfitUSD)
			if favorPrice(p, px) >= minProfitPx {
				elapsed := barTimeFraction(bar, frac, d.BaseTF)
				idleBars := float64(idx-p.LastExtreme) + elapsed/d.BaseTF.Seconds()
				if idleBars*d.BaseTF.Seconds() >= e.policy.StallMinutes*60 {
					e.closePosition(p, e.exitPx(p, bar, px, p.Symbol), bar.Time, idx, "STALL")
					return
				}
			}
		}
	}

	// 最长持仓
	if e.policy.MaxHoldMinutes > 0 {
		held := float64(p.BarsHeld) * d.BaseTF.Duration().Minutes()
		if held >= e.policy.MaxHoldMinutes {
			e.closePosition(p, e.exitPx(p, bar, bar.Close, p.Symbol), bar.Time, idx, "TIMEOUT")
			return
		}
	}

	// 库存费（跨结算时刻结算）
	if e.cfg.SwapHour >= 0 {
		e.applySwap(p, bar.Time)
	}
}

// barTimeFraction 把路径点的时间比例换算为该 bar 内的秒数
func barTimeFraction(bar model.Bar, frac float64, tf model.TF) float64 {
	return frac * tf.Seconds()
}

// favorPrice 当前浮盈（价格单位，正值表示有利方向）
func favorPrice(p *Position, bid float64) float64 {
	if p.Side == model.SideBuy {
		return bid - p.EntryPx
	}
	return p.EntryPx - bid
}

// toPriceUnits 按 TrailMode 把策略阈值换算成价格单位
func (e *Engine) toPriceUnits(p *Position, d *data.SymbolData, baseIdx int, v float64) float64 {
	switch e.policy.TrailMode {
	case "atr":
		inds := e.ind[p.Symbol]
		if inds == nil || len(inds.M5ATR) == 0 || baseIdx >= len(d.M5Idx) {
			return v
		}
		mi := d.M5Idx[baseIdx]
		if mi < 0 || mi >= len(inds.M5ATR) {
			return v
		}
		return v * inds.M5ATR[mi]
	case "money":
		perPx := p.Sym.MoneyPerPricePerLot() * p.Lots
		if perPx <= 0 {
			return v
		}
		return v / perPx
	default: // price
		return v
	}
}

func (e *Engine) applySwap(p *Position, now time.Time) {
	day := now.Year()*1000 + now.YearDay()
	if p.LastSwapDay == day {
		return
	}
	if now.Hour() < e.cfg.SwapHour {
		return
	}
	rate := p.Sym.SwapLongPerLot
	if p.Side == model.SideSell {
		rate = p.Sym.SwapShortPerLot
	}
	if rate != 0 {
		e.acct.Balance += rate * p.Lots
	}
	p.LastSwapDay = day
}

// closePosition 平仓。px 为最终成交价（调用方需按方向处理点差）
func (e *Engine) closePosition(p *Position, px float64, t time.Time, idx int, reason string) {
	s := p.Sym
	px = s.RoundPrice(px)
	gross := s.PnL(p.Lots, p.EntryPx, px, p.Side)
	commOut := p.Lots * s.CommissionPerLot
	e.acct.Balance += gross - commOut

	mfe, mae := excursion(p)

	tr := &Trade{
		ID: p.ID, Symbol: p.Symbol, Side: p.Side, Lots: p.Lots,
		EntryTime: p.EntryT, EntryPrice: p.EntryPx,
		ExitTime: t, ExitPrice: px, SL: p.SL, TP: p.TP, InitSL: p.InitSL,
		RiskUSD:    p.RiskUSD,
		GrossPnL:   gross,
		Commission: p.CommissionIn + commOut,
		NetPnL:     gross - p.CommissionIn - commOut,
		Reason:     reason,
		MFEPrice:   mfe,
		MAEPrice:   mae,
		BarsHeld:   p.BarsHeld,
		Tag:        p.Tag,
	}
	e.trades = append(e.trades, tr)
	delete(e.positions, p.Symbol)
}

// excursion 返回最大有利/不利偏移（价格单位，均为正数）
func excursion(p *Position) (mfe, mae float64) {
	if p.Side == model.SideBuy {
		return p.BestPx - p.EntryPx, p.EntryPx - p.WorstPx
	}
	return p.EntryPx - p.BestPx, p.WorstPx - p.EntryPx
}

// markToMarket 按各品种最新价盯市
func (e *Engine) markToMarket(now time.Time) {
	var floating, margin float64
	for name, p := range e.positions {
		bar, ok := e.last[name]
		if !ok {
			continue
		}
		px := bar.Close
		floating += p.Sym.PnL(p.Lots, p.EntryPx, px, p.Side)
		margin += p.Sym.MarginRequired(p.Lots, px, e.cfg.Leverage)
	}
	e.acct.Margin = margin
	e.acct.Equity = e.acct.Balance + floating
	if e.acct.Equity > e.peak {
		e.peak = e.acct.Equity
	}
	_ = now
}

// checkStopOut 保证金不足强制平仓
func (e *Engine) checkStopOut(now time.Time) {
	if e.cfg.StopOutPct <= 0 || e.acct.Margin <= 0 {
		return
	}
	if e.acct.MarginLevel() >= e.cfg.StopOutPct {
		return
	}
	// 平掉浮亏最大的持仓（用当前最新价，避免前视）
	var worst *Position
	var worstPnl float64
	for name, p := range e.positions {
		bar, ok := e.last[name]
		if !ok {
			continue
		}
		pnl := p.Sym.PnL(p.Lots, p.EntryPx, bar.Close, p.Side)
		if worst == nil || pnl < worstPnl {
			worst = p
			worstPnl = pnl
		}
	}
	if worst == nil {
		return
	}
	bar := e.last[worst.Symbol]
	e.closePosition(worst, e.exitPx(worst, bar, bar.Close, worst.Symbol), now, worst.EntryIdx, "STOPOUT")
}

// exitPx 计算平仓成交价（多头按 Bid 平、空头按 Ask 平）
func (e *Engine) exitPx(p *Position, bar model.Bar, bid float64, sym string) float64 {
	if p.Side == model.SideSell {
		return bid + e.barSpread(p.Sym, bar)
	}
	return bid
}

func (e *Engine) drawdownPct() float64 {
	if e.peak <= 0 {
		return 0
	}
	return (e.peak - e.acct.Equity) / e.peak * 100
}

func (e *Engine) maxDrawdown() (abs, pct float64) {
	peak := e.cfg.Balance
	for _, pt := range e.equity {
		if pt.Equity > peak {
			peak = pt.Equity
		}
		dd := peak - pt.Equity
		if dd > abs {
			abs = dd
		}
		if peak > 0 {
			p := dd / peak * 100
			if p > pct {
				pct = p
			}
		}
	}
	return
}

// Sizer 风险手数计算工具（供策略使用）
type Sizer struct{}

// LotsByRisk 按风险金额与止损距离计算手数
func LotsByRisk(s model.Symbol, riskUSD, slDistPrice float64) float64 {
	if slDistPrice <= 0 {
		return 0
	}
	lossPerLot := slDistPrice * s.MoneyPerPricePerLot()
	if lossPerLot <= 0 {
		return 0
	}
	return s.NormalizeLots(riskUSD / lossPerLot)
}

// LotsByRiskPct 按余额百分比风险计算手数
func LotsByRiskPct(s model.Symbol, balance, pct, slDistPrice float64) float64 {
	return LotsByRisk(s, balance*pct/100, slDistPrice)
}

var _ = fmt.Sprintf
