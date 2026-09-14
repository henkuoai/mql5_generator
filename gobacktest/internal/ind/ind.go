// Package ind 提供增量型技术指标计算（与 MT5 内置指标口径一致）。
package ind

import (
	"math"

	"gobacktest/internal/model"
)

// EMA 指数移动平均。前 period-1 根用 SMA 播种，之后递推。
func EMA(vals []float64, period int) []float64 {
	out := make([]float64, len(vals))
	if period <= 0 || len(vals) == 0 {
		return out
	}
	alpha := 2.0 / float64(period+1)
	var sum float64
	for i, v := range vals {
		if i < period {
			sum += v
			if i == period-1 {
				out[i] = sum / float64(period)
			}
			continue
		}
		out[i] = out[i-1] + alpha*(v-out[i-1])
	}
	return out
}

// SMA 简单移动平均
func SMA(vals []float64, period int) []float64 {
	out := make([]float64, len(vals))
	if period <= 0 {
		return out
	}
	var sum float64
	for i, v := range vals {
		sum += v
		if i >= period {
			sum -= vals[i-period]
		}
		if i >= period-1 {
			out[i] = sum / float64(period)
		}
	}
	return out
}

// ATR Wilder 平滑真实波幅
func ATR(bars []model.Bar, period int) []float64 {
	out := make([]float64, len(bars))
	if period <= 0 || len(bars) == 0 {
		return out
	}
	tr := make([]float64, len(bars))
	for i := range bars {
		if i == 0 {
			tr[i] = bars[i].High - bars[i].Low
			continue
		}
		pc := bars[i-1].Close
		tr[i] = math.Max(bars[i].High-bars[i].Low,
			math.Max(math.Abs(bars[i].High-pc), math.Abs(bars[i].Low-pc)))
	}
	var sum float64
	for i := 0; i < len(bars) && i < period; i++ {
		sum += tr[i]
	}
	if len(bars) >= period {
		out[period-1] = sum / float64(period)
	}
	for i := period; i < len(bars); i++ {
		out[i] = (out[i-1]*float64(period-1) + tr[i]) / float64(period)
	}
	return out
}

// ADX Wilder 平均趋向指数（返回 ADX 主线）
func ADX(bars []model.Bar, period int) []float64 {
	adx, _, _ := ADXFull(bars, period)
	return adx
}

// ADXFull 返回 ADX / +DI / -DI
func ADXFull(bars []model.Bar, period int) (adx, plusDI, minusDI []float64) {
	n := len(bars)
	adx = make([]float64, n)
	plusDI = make([]float64, n)
	minusDI = make([]float64, n)
	if period <= 0 || n == 0 {
		return
	}
	tr := make([]float64, n)
	plusDM := make([]float64, n)
	minusDM := make([]float64, n)
	for i := 1; i < n; i++ {
		up := bars[i].High - bars[i-1].High
		dn := bars[i-1].Low - bars[i].Low
		if up > dn && up > 0 {
			plusDM[i] = up
		}
		if dn > up && dn > 0 {
			minusDM[i] = dn
		}
		pc := bars[i-1].Close
		tr[i] = math.Max(bars[i].High-bars[i].Low,
			math.Max(math.Abs(bars[i].High-pc), math.Abs(bars[i].Low-pc)))
	}
	// Wilder 平滑
	sTR := make([]float64, n)
	sP := make([]float64, n)
	sM := make([]float64, n)
	var t, p, m float64
	for i := 1; i < n; i++ {
		if i <= period {
			t += tr[i]
			p += plusDM[i]
			m += minusDM[i]
			if i == period {
				sTR[i], sP[i], sM[i] = t, p, m
			}
			continue
		}
		sTR[i] = sTR[i-1] - sTR[i-1]/float64(period) + tr[i]
		sP[i] = sP[i-1] - sP[i-1]/float64(period) + plusDM[i]
		sM[i] = sM[i-1] - sM[i-1]/float64(period) + minusDM[i]
	}
	dx := make([]float64, n)
	for i := period; i < n; i++ {
		if sTR[i] == 0 {
			continue
		}
		plusDI[i] = 100 * sP[i] / sTR[i]
		minusDI[i] = 100 * sM[i] / sTR[i]
		sum := plusDI[i] + minusDI[i]
		if sum == 0 {
			continue
		}
		dx[i] = 100 * math.Abs(plusDI[i]-minusDI[i]) / sum
	}
	// ADX = DX 的 Wilder 平滑
	var acc float64
	cnt := 0
	for i := period; i < n; i++ {
		cnt++
		if cnt <= period {
			acc += dx[i]
			if cnt == period {
				adx[i] = acc / float64(period)
			}
			continue
		}
		adx[i] = (adx[i-1]*float64(period-1) + dx[i]) / float64(period)
	}
	return
}

// RollingMaxHigh 窗口最大值：out[i] = max(high[i-p+1 .. i])
func RollingMaxHigh(bars []model.Bar, period int) []float64 {
	out := make([]float64, len(bars))
	if period <= 0 {
		return out
	}
	for i := range bars {
		if i < period-1 {
			continue
		}
		mx := bars[i].High
		for j := i - period + 1; j < i; j++ {
			if bars[j].High > mx {
				mx = bars[j].High
			}
		}
		out[i] = mx
	}
	return out
}

// RollingMinLow 窗口最小值：out[i] = min(low[i-p+1 .. i])
func RollingMinLow(bars []model.Bar, period int) []float64 {
	out := make([]float64, len(bars))
	if period <= 0 {
		return out
	}
	for i := range bars {
		if i < period-1 {
			continue
		}
		mn := bars[i].Low
		for j := i - period + 1; j < i; j++ {
			if bars[j].Low < mn {
				mn = bars[j].Low
			}
		}
		out[i] = mn
	}
	return out
}

// StdDev 滚动标准差
func StdDev(vals []float64, period int) []float64 {
	out := make([]float64, len(vals))
	if period <= 0 {
		return out
	}
	for i := range vals {
		if i < period-1 {
			continue
		}
		var sum float64
		for j := i - period + 1; j <= i; j++ {
			sum += vals[j]
		}
		mean := sum / float64(period)
		var v float64
		for j := i - period + 1; j <= i; j++ {
			d := vals[j] - mean
			v += d * d
		}
		out[i] = math.Sqrt(v / float64(period))
	}
	return out
}
