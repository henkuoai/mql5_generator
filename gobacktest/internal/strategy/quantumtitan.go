// Package strategy 实现具体交易策略。
// 所有策略实现 engine.Strategy 接口：在每根基准 bar 收盘后被调用，返回下一根 bar 开盘执行的订单。
package strategy

import (
	"gobacktest/internal/engine"
	"gobacktest/internal/model"
)

// QTParams 量子泰坦（Quantum Titan）风格动量突破策略参数
type QTParams struct {
	// ===== 入场：突破 =====
	BreakoutBars         int     // 突破回看 M5 根数（6=30分钟, 12=1小时）
	BreakoutBufferPoints float64 // 突破缓冲（点）
	TriggerMode          string  // both | breakout | velocity

	// ===== 入场：动量 =====
	VelocityBars   int     // 动量回看 M5 根数（1≈5分钟）
	VelocityMinUSD float64 // 最小动量（价格单位，黄金即美元）

	// ===== 入场：宽幅方向K线 =====
	RequireWideBar bool
	MinBarRangeATR float64 // K线振幅 >= 该倍数 × ATR
	BarClosePct    float64 // 收盘位于 top/bottom 分位（%）

	// ===== 入场：环境过滤 =====
	UseM5Trend      bool
	UseH1Trend      bool
	MinADX          float64
	MaxSpreadPoints float64

	// ===== 出场 =====
	StopLossPoints float64 // 固定止损（点）
	TPATRMult      float64 // 止盈 = 该倍数 × ATR（0=不设止盈）

	// ===== 持仓管理 =====
	TrailStartUSD     float64
	TrailDistUSD      float64
	StallSeconds      float64
	StallMinProfitUSD float64
	MaxHoldMinutes    float64

	// ===== 仓位与风控 =====
	RiskPercent        float64
	UseFixedLots       bool
	FixedLots          float64
	MinIntervalMinutes float64
	MaxDDHaltPct       float64
	DDHaltSticky       bool // true=触发后永久停止（MQL5 版行为）；false=回撤恢复后自动重新交易
	AdaptiveRisk       bool
	DDTrigger          float64 // 回撤达到该值进入恢复模式
	DDRecoverBoostPct  float64 // 恢复模式风险%
	DDDeRiskPct        float64 // 创新高后回落到的风险%
}

// QTV1 复刻 EA v1 默认参数（触发宽松，交易频率高）
func QTV1() QTParams {
	return QTParams{
		BreakoutBars: 6, BreakoutBufferPoints: 30, TriggerMode: "both",
		VelocityBars: 1, VelocityMinUSD: 4.0,
		RequireWideBar: false, MinBarRangeATR: 1.0, BarClosePct: 30,
		UseM5Trend: true, UseH1Trend: true, MinADX: 20, MaxSpreadPoints: 350,
		StopLossPoints: 10000, TPATRMult: 0.35,
		TrailStartUSD: 1.2, TrailDistUSD: 0.8,
		StallSeconds: 25, StallMinProfitUSD: 0.3, MaxHoldMinutes: 20,
		RiskPercent: 5, MinIntervalMinutes: 1,
		// 注意：MQL5 版有 15% 最大回撤熔断（触发后永久停止，需手动重启）。
		// 回测里默认关闭，否则一旦触发后续行情全部不交易，曲线会变成一条直线。
		// 需要复现该行为时用 -ddhalt 15 并令 DDHaltSticky=true。
		MaxDDHaltPct: 0,
	}
}

// QTV2 复刻 EA v2 默认参数（加宽幅方向K线，选择性显著提升）
func QTV2() QTParams {
	p := QTV1()
	p.BreakoutBars = 12
	p.BreakoutBufferPoints = 50
	p.VelocityMinUSD = 6.0
	p.RequireWideBar = true
	p.MinBarRangeATR = 1.2
	p.BarClosePct = 30
	p.MinADX = 22
	p.TPATRMult = 0.40
	p.StallSeconds = 30
	p.MaxHoldMinutes = 30
	p.AdaptiveRisk = true
	p.DDTrigger = 8
	p.DDRecoverBoostPct = 9
	p.DDDeRiskPct = 3
	return p
}

// QT 量子泰坦风格策略实现
type QT struct {
	p      QTParams
	stats  map[string]int
	riskSt string
	halted bool
}

// NewQT 创建策略
func NewQT(p QTParams) *QT {
	if p.TriggerMode == "" {
		p.TriggerMode = "both"
	}
	if p.VelocityBars < 1 {
		p.VelocityBars = 1
	}
	if p.BreakoutBars < 2 {
		p.BreakoutBars = 2
	}
	if p.MinIntervalMinutes < 0 {
		p.MinIntervalMinutes = 0
	}
	return &QT{p: p, stats: map[string]int{}, riskSt: "base"}
}

func (s *QT) Name() string { return "quantum-titan" }

func (s *QT) Policy() engine.ManagePolicy {
	return engine.ManagePolicy{
		// 忠实复刻 MT5 版：跟踪/停滞阈值按"价格单位"（黄金即美元）
		TrailMode:         "price",
		TrailStartUSD:     s.p.TrailStartUSD,
		TrailDistUSD:      s.p.TrailDistUSD,
		StallMinutes:      s.p.StallSeconds / 60.0,
		StallMinProfitUSD: s.p.StallMinProfitUSD,
		MaxHoldMinutes:    s.p.MaxHoldMinutes,
	}
}

func (s *QT) Stats() map[string]int { return s.stats }

func (s *QT) reject(reason string) {
	s.stats[reason]++
}

// OnBarClose 信号评估
func (s *QT) OnBarClose(ctx *engine.Context) []engine.Order {
	const warmup = 120 // M5 根数
	sd := ctx.SD
	idx := sd.M5Idx[ctx.BaseIdx]
	if idx < warmup {
		s.reject("warmup")
		return nil
	}
	ind := ctx.Ind
	atr := ind.M5ATR[idx]
	if atr <= 0 {
		s.reject("no_atr")
		return nil
	}
	sym := ctx.Sym
	bar := sd.M5[idx]

	// ---- 动量 ----
	vel := bar.Close - sd.M5[idx-s.p.VelocityBars].Close
	velUp := vel >= s.p.VelocityMinUSD
	velDn := vel <= -s.p.VelocityMinUSD

	// ---- 突破 ----
	buf := s.p.BreakoutBufferPoints * sym.Point
	brkUp, brkDn := false, false
	if idx >= 1 {
		brkUp = bar.High > ind.M5HiN[idx-1]+buf
		brkDn = bar.Low < ind.M5LoN[idx-1]-buf
	}

	// ---- 触发组合 ----
	var up, dn bool
	switch s.p.TriggerMode {
	case "breakout":
		up, dn = brkUp, brkDn
	case "velocity":
		up, dn = velUp, velDn
	default:
		up, dn = brkUp && velUp, brkDn && velDn
	}
	if !up && !dn {
		s.reject("trigger")
		return nil
	}

	// ---- 宽幅方向K线 ----
	if s.p.RequireWideBar {
		rng := bar.High - bar.Low
		if rng < atr*s.p.MinBarRangeATR {
			s.reject("narrow_bar")
			return nil
		}
		pos := (bar.Close - bar.Low) / rng
		th := s.p.BarClosePct / 100.0
		if up && pos < 1-th {
			s.reject("close_pos")
			return nil
		}
		if dn && pos > th {
			s.reject("close_pos")
			return nil
		}
	}

	// ---- 趋势过滤 ----
	if s.p.UseM5Trend {
		e20, e50 := ind.M5EMA20[idx], ind.M5EMA50[idx]
		if e20 == 0 || e50 == 0 {
			s.reject("m5_ema")
			return nil
		}
		if up && !(bar.Close > e20 && e20 > e50) {
			s.reject("m5_trend")
			return nil
		}
		if dn && !(bar.Close < e20 && e20 < e50) {
			s.reject("m5_trend")
			return nil
		}
	}
	if s.p.UseH1Trend {
		hi := sd.H1Idx[ctx.BaseIdx]
		if hi < 50 {
			s.reject("h1_warmup")
			return nil
		}
		e20, e50 := ind.H1EMA20[hi], ind.H1EMA50[hi]
		if e20 == 0 || e50 == 0 {
			s.reject("h1_ema")
			return nil
		}
		if up && !(bar.Close > e20 && e20 > e50) {
			s.reject("h1_trend")
			return nil
		}
		if dn && !(bar.Close < e20 && e20 < e50) {
			s.reject("h1_trend")
			return nil
		}
	}

	// ---- ADX ----
	if s.p.MinADX > 0 && ind.M5ADX[idx] < s.p.MinADX {
		s.reject("adx")
		return nil
	}

	// ---- 点差 ----
	if s.p.MaxSpreadPoints > 0 && bar.Spread > s.p.MaxSpreadPoints {
		s.reject("spread")
		return nil
	}

	// ---- 冷却 ----
	if s.p.MinIntervalMinutes > 0 && ctx.LastEntryAgoM >= 0 && ctx.LastEntryAgoM < s.p.MinIntervalMinutes {
		s.reject("cooldown")
		return nil
	}

	// ---- 回撤熔断 ----
	if s.p.MaxDDHaltPct > 0 {
		if ctx.DDPercent >= s.p.MaxDDHaltPct {
			s.halted = true
			s.reject("dd_halt")
			return nil
		}
		// 非粘性熔断：回撤恢复到阈值一半以下时重新允许交易
		if s.halted && !s.p.DDHaltSticky && ctx.DDPercent < s.p.MaxDDHaltPct/2 {
			s.halted = false
			s.stats["dd_resume"]++
		}
		if s.halted {
			s.reject("dd_halt")
			return nil
		}
	}

	// ---- 仓位 ----
	slDist := s.p.StopLossPoints * sym.Point
	if slDist <= 0 {
		s.reject("no_sl")
		return nil
	}
	var lots float64
	riskUSD := 0.0
	if s.p.UseFixedLots {
		lots = sym.NormalizeLots(s.p.FixedLots)
		riskUSD = lots * slDist * sym.MoneyPerPricePerLot()
	} else {
		riskPct := s.effectiveRiskPct(ctx)
		riskUSD = ctx.Balance * riskPct / 100
		lots = engine.LotsByRisk(sym, riskUSD, slDist)
	}
	if lots <= 0 {
		s.reject("lots_zero")
		return nil
	}

	side := model.SideBuy
	if dn {
		side = model.SideSell
	}
	tpDist := 0.0
	if s.p.TPATRMult > 0 {
		tpDist = atr * s.p.TPATRMult
	}
	tag := "QT"
	if dn {
		tag = "QT-S"
	} else {
		tag = "QT-B"
	}
	s.stats["signals"]++
	return []engine.Order{{
		Symbol: ctx.Sym.Name, Side: side, Lots: lots,
		SLDist: slDist, TPDist: tpDist, RiskUSD: riskUSD, Tag: tag,
	}}
}

// effectiveRiskPct 回撤自适应风险档（复刻原 EA 的自动风险等级思想）
func (s *QT) effectiveRiskPct(ctx *engine.Context) float64 {
	if !s.p.AdaptiveRisk {
		return s.p.RiskPercent
	}
	dd := ctx.DDPercent
	// 创新高 -> 降档锁利
	if dd <= 0.5 && s.riskSt == "recover" {
		s.riskSt = "derisk"
	}
	// 深回撤 -> 升档恢复
	if dd >= s.p.DDTrigger {
		s.riskSt = "recover"
	} else if dd <= 0.5 && s.riskSt != "recover" {
		if s.riskSt != "derisk" {
			s.riskSt = "base"
		}
	}
	switch s.riskSt {
	case "recover":
		if s.p.DDRecoverBoostPct > 0 {
			return s.p.DDRecoverBoostPct
		}
	case "derisk":
		if s.p.DDDeRiskPct > 0 {
			return s.p.DDDeRiskPct
		}
	}
	return s.p.RiskPercent
}
