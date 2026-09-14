package strategy

import (
	"gobacktest/internal/engine"
	"gobacktest/internal/model"
)

// EMAParams 均线交叉策略参数（作为基准对照，不是量子泰坦）
type EMAParams struct {
	FastEMA      int
	SlowEMA      int
	StopLossPts  float64
	TPPts        float64
	RiskPercent  float64
	MaxHoldMin   float64
	TrailStartUSD float64
	TrailDistUSD  float64
	UseFixedLots bool
	FixedLots    float64
}

// DefaultEMA 默认参数
func DefaultEMA() EMAParams {
	return EMAParams{
		FastEMA: 20, SlowEMA: 50,
		StopLossPts: 3000, TPPts: 6000,
		RiskPercent: 1.0, MaxHoldMin: 240,
	}
}

// EMA 均线交叉策略
type EMA struct {
	p     EMAParams
	stats map[string]int
}

// NewEMA 创建策略
func NewEMA(p EMAParams) *EMA {
	if p.FastEMA <= 0 {
		p.FastEMA = 20
	}
	if p.SlowEMA <= 0 {
		p.SlowEMA = 50
	}
	return &EMA{p: p, stats: map[string]int{}}
}

func (s *EMA) Name() string { return "ema-cross" }

func (s *EMA) Stats() map[string]int { return s.stats }

func (s *EMA) Policy() engine.ManagePolicy {
	return engine.ManagePolicy{
		TrailStartUSD:  s.p.TrailStartUSD,
		TrailDistUSD:   s.p.TrailDistUSD,
		MaxHoldMinutes: s.p.MaxHoldMin,
	}
}

// OnBarClose 金叉做多 / 死叉做空
func (s *EMA) OnBarClose(ctx *engine.Context) []engine.Order {
	sd := ctx.SD
	idx := sd.M5Idx[ctx.BaseIdx]
	if idx < s.p.SlowEMA+5 {
		return nil
	}
	f, fPrev := ctx.Ind.M5EMA20[idx], ctx.Ind.M5EMA20[idx-1]
	sl, slPrev := ctx.Ind.M5EMA50[idx], ctx.Ind.M5EMA50[idx-1]
	if f == 0 || sl == 0 {
		return nil
	}
	var side model.Side
	switch {
	case fPrev <= slPrev && f > sl:
		side = model.SideBuy
	case fPrev >= slPrev && f < sl:
		side = model.SideSell
	default:
		return nil
	}
	sym := ctx.Sym
	slDist := s.p.StopLossPts * sym.Point
	lots := 0.0
	if s.p.UseFixedLots {
		lots = sym.NormalizeLots(s.p.FixedLots)
	} else {
		lots = engine.LotsByRiskPct(sym, ctx.Balance, s.p.RiskPercent, slDist)
	}
	if lots <= 0 {
		return nil
	}
	s.stats["signals"]++
	return []engine.Order{{
		Symbol: sym.Name, Side: side, Lots: lots,
		SLDist: slDist, TPDist: s.p.TPPts * sym.Point, Tag: "EMA",
	}}
}
