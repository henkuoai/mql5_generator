// Package metrics 由回测结果计算绩效指标。
package metrics

import (
	"math"
	"sort"
	"time"

	"gobacktest/internal/engine"
	"gobacktest/internal/model"
)

// SymbolStat 分品种统计
type SymbolStat struct {
	Trades    int
	Wins      int
	NetPnL    float64
	WinRate   float64
	AvgPnL    float64
	Longs     int
	Shorts    int
	LongPnL   float64
	ShortPnL  float64
}

// MonthStat 分月统计
type MonthStat struct {
	Month     string
	Trades    int
	Wins      int
	NetPnL    float64
	ReturnPct float64
	EndEquity float64
}

// Stats 汇总绩效
type Stats struct {
	StartBalance float64
	EndBalance   float64
	EndEquity    float64
	NetProfit    float64
	ReturnPct    float64

	Trades   int
	Wins     int
	Losses   int
	WinRate  float64
	Longs    int
	Shorts   int
	GrossWin float64
	GrossLos float64

	ProfitFactor   float64
	AvgWin         float64
	AvgLoss        float64
	Expectancy     float64
	LargestWin     float64
	LargestLoss    float64
	MaxWinStreak   int
	MaxLossStreak  int
	AvgHoldMin     float64
	MedianHoldMin  float64
	Commission     float64
	Swap           float64
	RejectedOrders int

	MaxDD        float64
	MaxDDPct     float64
	RecoveryFact float64
	Sharpe       float64
	CalmarRatio  float64

	ByReason map[string]int
	BySymbol map[string]*SymbolStat
	Monthly  []MonthStat
}

// Compute 计算全部指标
func Compute(res *engine.Result) *Stats {
	st := &Stats{
		StartBalance:   res.StartBal,
		EndBalance:     res.EndBal,
		EndEquity:      res.EndEquity,
		ByReason:       map[string]int{},
		BySymbol:       map[string]*SymbolStat{},
		RejectedOrders: res.Rejected,
	}
	st.NetProfit = res.EndBal - res.StartBal
	if res.StartBal > 0 {
		st.ReturnPct = st.NetProfit / res.StartBal * 100
	}

	var holds []float64
	var winStreak, lossStreak int
	for _, t := range res.Trades {
		st.Trades++
		st.Commission += t.Commission
		st.Swap += t.Swap
		holds = append(holds, float64(t.BarsHeld)*5.0) // 近似：bar 数 × 5 分钟
		st.ByReason[t.Reason]++
		ss := st.BySymbol[t.Symbol]
		if ss == nil {
			ss = &SymbolStat{}
			st.BySymbol[t.Symbol] = ss
		}
		ss.Trades++
		ss.NetPnL += t.NetPnL
		if t.Side == model.SideBuy {
			ss.Longs++
			ss.LongPnL += t.NetPnL
			st.Longs++
		} else {
			ss.Shorts++
			ss.ShortPnL += t.NetPnL
			st.Shorts++
		}
		if t.NetPnL > 0 {
			st.Wins++
			ss.Wins++
			st.GrossWin += t.NetPnL
			winStreak++
			lossStreak = 0
			if winStreak > st.MaxWinStreak {
				st.MaxWinStreak = winStreak
			}
			if t.NetPnL > st.LargestWin {
				st.LargestWin = t.NetPnL
			}
		} else {
			st.Losses++
			st.GrossLos += -t.NetPnL
			lossStreak++
			winStreak = 0
			if lossStreak > st.MaxLossStreak {
				st.MaxLossStreak = lossStreak
			}
			if t.NetPnL < st.LargestLoss {
				st.LargestLoss = t.NetPnL
			}
		}
	}
	if st.Trades > 0 {
		st.WinRate = float64(st.Wins) / float64(st.Trades) * 100
		st.Expectancy = st.NetProfit / float64(st.Trades)
	}
	if st.Wins > 0 {
		st.AvgWin = st.GrossWin / float64(st.Wins)
	}
	if st.Losses > 0 {
		st.AvgLoss = -st.GrossLos / float64(st.Losses)
	}
	if st.GrossLos > 0 {
		st.ProfitFactor = st.GrossWin / st.GrossLos
	}
	if len(holds) > 0 {
		sort.Float64s(holds)
		var sum float64
		for _, h := range holds {
			sum += h
		}
		st.AvgHoldMin = sum / float64(len(holds))
		st.MedianHoldMin = holds[len(holds)/2]
	}
	for _, ss := range st.BySymbol {
		if ss.Trades > 0 {
			ss.WinRate = float64(ss.Wins) / float64(ss.Trades) * 100
			ss.AvgPnL = ss.NetPnL / float64(ss.Trades)
		}
	}

	// 回撤
	peak := res.StartBal
	for _, p := range res.Equity {
		if p.Equity > peak {
			peak = p.Equity
		}
		dd := peak - p.Equity
		if dd > st.MaxDD {
			st.MaxDD = dd
		}
		if peak > 0 {
			if pct := dd / peak * 100; pct > st.MaxDDPct {
				st.MaxDDPct = pct
			}
		}
	}
	if st.MaxDD > 0 {
		st.RecoveryFact = st.NetProfit / st.MaxDD
	}
	if st.MaxDDPct > 0 {
		st.CalmarRatio = st.ReturnPct / st.MaxDDPct
	}
	st.Sharpe = sharpe(res.Equity, res.StartBal)
	st.Monthly = monthly(res)
	return st
}

// sharpe 基于每日收益的年化夏普
func sharpe(curve []engine.EquityPoint, startBal float64) float64 {
	if len(curve) < 10 {
		return 0
	}
	dayEnd := map[string]float64{}
	var order []string
	for _, p := range curve {
		k := p.Time.Format("2006-01-02")
		if _, ok := dayEnd[k]; !ok {
			order = append(order, k)
		}
		dayEnd[k] = p.Equity
	}
	sort.Strings(order)
	var rets []float64
	prev := startBal
	for _, k := range order {
		v := dayEnd[k]
		if prev > 0 {
			rets = append(rets, v/prev-1)
		}
		prev = v
	}
	if len(rets) < 5 {
		return 0
	}
	var mean float64
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))
	var v float64
	for _, r := range rets {
		d := r - mean
		v += d * d
	}
	sd := math.Sqrt(v / float64(len(rets)-1))
	if sd == 0 {
		return 0
	}
	return mean / sd * math.Sqrt(252)
}

func monthly(res *engine.Result) []MonthStat {
	type agg struct {
		trades, wins int
		pnl          float64
		startEq      float64
		endEq        float64
	}
	m := map[string]*agg{}
	var keys []string
	// 用净值曲线确定月初/月末净值
	for _, p := range res.Equity {
		k := p.Time.Format("2006-01")
		a := m[k]
		if a == nil {
			a = &agg{startEq: p.Equity}
			m[k] = a
			keys = append(keys, k)
		}
		if a.startEq == 0 {
			a.startEq = p.Equity
		}
		a.endEq = p.Equity
	}
	for _, t := range res.Trades {
		k := t.ExitTime.Format("2006-01")
		a := m[k]
		if a == nil {
			a = &agg{}
			m[k] = a
			keys = append(keys, k)
		}
		a.trades++
		a.pnl += t.NetPnL
		if t.NetPnL > 0 {
			a.wins++
		}
	}
	sort.Strings(keys)
	var out []MonthStat
	for _, k := range keys {
		a := m[k]
		ms := MonthStat{Month: k, Trades: a.trades, Wins: a.wins, NetPnL: a.pnl, EndEquity: a.endEq}
		base := a.startEq
		if base == 0 {
			base = res.StartBal
		}
		if base > 0 {
			ms.ReturnPct = a.pnl / base * 100
		}
		out = append(out, ms)
	}
	return out
}

// ReasonLabel 离场原因中文化
func ReasonLabel(r string) string {
	switch r {
	case "SL":
		return "止损"
	case "TP":
		return "止盈"
	case "TRAIL":
		return "跟踪止损"
	case "STALL":
		return "停滞离场"
	case "TIMEOUT":
		return "超时离场"
	case "STOPOUT":
		return "强制平仓"
	case "END":
		return "回测结束"
	}
	return r
}

// Duration 格式化
func Duration(d time.Duration) string {
	return d.Round(time.Minute).String()
}
