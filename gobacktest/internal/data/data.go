// Package data 负责历史数据加载、周期聚合与指标预计算。
package data

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"gobacktest/internal/ind"
	"gobacktest/internal/model"
)

var timeLayouts = []string{
	"2006.01.02 15:04:05",
	"2006.01.02 15:04",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04",
	"2006/01/02 15:04:05",
}

func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间: %q", s)
}

// LoadBars 读取 CSV 格式的K线。
// 表头支持 time/date, open, high, low, close, volume/tick_volume, spread（列序可任意）。
// 无表头时按 time,open,high,low,close,volume,spread 顺序解析。
func LoadBars(path string) ([]model.Bar, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.Comma = ','
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true

	var bars []model.Bar
	col := map[string]int{}
	first := true
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(rec) == 0 {
			continue
		}
		if first {
			first = false
			if isHeader(rec) {
				for i, c := range rec {
					col[strings.ToLower(strings.TrimSpace(c))] = i
				}
				continue
			}
			// 无表头，按默认列序
			col = map[string]int{
				"time": 0, "open": 1, "high": 2, "low": 3, "close": 4,
				"tick_volume": 5, "spread": 6,
			}
		}
		bar, err := parseBar(rec, col)
		if err != nil {
			continue // 跳过坏行
		}
		bars = append(bars, bar)
	}
	return bars, nil
}

func isHeader(rec []string) bool {
	for _, c := range rec {
		l := strings.ToLower(strings.TrimSpace(c))
		if l == "time" || l == "date" || l == "datetime" {
			return true
		}
	}
	return false
}

func getCol(rec []string, col map[string]int, names ...string) (string, bool) {
	for _, n := range names {
		if i, ok := col[n]; ok && i < len(rec) {
			return rec[i], true
		}
	}
	return "", false
}

func parseBar(rec []string, col map[string]int) (model.Bar, error) {
	var b model.Bar
	ts, ok := getCol(rec, col, "time", "date", "datetime")
	if !ok {
		return b, fmt.Errorf("缺少时间列")
	}
	t, err := parseTime(ts)
	if err != nil {
		return b, err
	}
	b.Time = t
	fields := []struct {
		dst   *float64
		names []string
	}{
		{&b.Open, []string{"open"}},
		{&b.High, []string{"high"}},
		{&b.Low, []string{"low"}},
		{&b.Close, []string{"close"}},
	}
	for _, f := range fields {
		s, ok := getCol(rec, col, f.names...)
		if !ok {
			return b, fmt.Errorf("缺少列 %v", f.names)
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return b, err
		}
		*f.dst = v
	}
	if s, ok := getCol(rec, col, "tick_volume", "volume"); ok {
		if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			b.Volume = int64(v)
		}
	}
	if s, ok := getCol(rec, col, "spread"); ok {
		if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			b.Spread = v
		}
	}
	return b, nil
}

// Aggregate 把低周期K线聚合为高周期K线（按时间向下取整分组）。
func Aggregate(bars []model.Bar, from, to model.TF) []model.Bar {
	if len(bars) == 0 || to <= from {
		return bars
	}
	dur := to.Duration()
	var out []model.Bar
	var cur model.Bar
	var have bool
	for _, b := range bars {
		key := b.Time.Truncate(dur)
		if !have || !key.Equal(cur.Time) {
			if have {
				out = append(out, cur)
			}
			cur = model.Bar{
				Time: key, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close,
				Volume: b.Volume, Spread: b.Spread,
			}
			have = true
			continue
		}
		if b.High > cur.High {
			cur.High = b.High
		}
		if b.Low < cur.Low {
			cur.Low = b.Low
		}
		cur.Close = b.Close
		cur.Volume += b.Volume
	}
	if have {
		out = append(out, cur)
	}
	return out
}

// BuildH1OfBase 对每个 base 索引 i，返回"在该 bar 开盘时刻已收盘的最后一根 H1 bar"的索引。
// 避免未来函数：H1 bar 只有在 base bar 开盘时间 >= H1 收盘时间时才可用。
func BuildH1OfBase(base, h1 []model.Bar) []int {
	out := make([]int, len(base))
	j := -1
	for i, b := range base {
		for j+1 < len(h1) && !h1[j+1].Time.Add(60*time.Minute).After(b.Time) {
			j++
		}
		out[i] = j
	}
	return out
}

// SymbolData 单个品种的全部回测数据与预计算指标
type SymbolData struct {
	Symbol   model.Symbol
	Base     []model.Bar // 基准流（M1/M5/.../D1）
	BaseTF   model.TF
	M5       []model.Bar
	H1       []model.Bar
	Clamped  bool // true 表示基准周期比目标周期更粗，M5/H1 直接沿用基准序列
	M5Idx    []int // Base 索引 -> M5 索引（最后一根已收盘 M5）
	H1Idx    []int // Base 索引 -> H1 索引（最后一根已收盘 H1）
	M5OfBase []int // Base 索引 -> 该 base bar 所属的 M5 bar 索引（用于聚合指标对齐）
}

// BuildSymbolData 由基准流构建品种数据。
// 说明：无法由粗周期推导出更细的周期。当基准周期比目标周期更粗时（例如用 H1 做基准、
// 目标却是 M5），直接沿用基准序列作为该目标序列（Clamped=true），并在控制台提示。
func BuildSymbolData(sym model.Symbol, base []model.Bar, baseTF model.TF, breakoutBars int) *SymbolData {
	sd := &SymbolData{Symbol: sym, Base: base, BaseTF: baseTF}
	var clamped bool
	sd.M5 = buildTF(base, baseTF, model.M5, &clamped)
	// H1 的来源是 sd.M5，其实际周期为 max(baseTF, M5)
	srcTF := baseTF
	if srcTF < model.M5 {
		srcTF = model.M5
	}
	sd.H1 = buildTF(sd.M5, srcTF, model.H1, &clamped)
	sd.Clamped = clamped
	sd.M5Idx = alignClosed(sd.M5, base, model.M5)
	sd.H1Idx = alignClosed(sd.H1, base, model.H1)
	return sd
}

// buildTF 生成目标周期序列：基准更细则聚合，否则沿用基准（无法由粗推细）
func buildTF(base []model.Bar, baseTF, target model.TF, clamped *bool) []model.Bar {
	if baseTF == target {
		return base
	}
	if baseTF > target {
		if clamped != nil {
			*clamped = true
		}
		return base
	}
	return Aggregate(base, baseTF, target)
}

// alignClosed 对每个 base bar 返回最后一根"已收盘"的 higher bar 索引。
func alignClosed(higher, base []model.Bar, tf model.TF) []int {
	out := make([]int, len(base))
	dur := tf.Duration()
	j := -1
	for i, b := range base {
		for j+1 < len(higher) && !higher[j+1].Time.Add(dur).After(b.Time) {
			j++
		}
		out[i] = j
	}
	return out
}

// Indicators 预计算指标容器（按 M5 与 H1 序列各自对齐）
type Indicators struct {
	M5EMA20 []float64
	M5EMA50 []float64
	M5ATR   []float64
	M5ADX   []float64
	M5HiN   []float64 // 近 N 根最高（含当前）
	M5LoN   []float64 // 近 N 根最低（含当前）
	H1EMA20 []float64
	H1EMA50 []float64
}

// ComputeIndicators 预计算指标
func ComputeIndicators(sd *SymbolData, breakoutBars, atrPeriod, adxPeriod int) *Indicators {
	if breakoutBars < 1 {
		breakoutBars = 1
	}
	if atrPeriod < 1 {
		atrPeriod = 14
	}
	if adxPeriod < 1 {
		adxPeriod = 14
	}
	m5closes := closes(sd.M5)
	h1closes := closes(sd.H1)
	return &Indicators{
		M5EMA20: ind.EMA(m5closes, 20),
		M5EMA50: ind.EMA(m5closes, 50),
		M5ATR:   ind.ATR(sd.M5, atrPeriod),
		M5ADX:   ind.ADX(sd.M5, adxPeriod),
		M5HiN:   ind.RollingMaxHigh(sd.M5, breakoutBars),
		M5LoN:   ind.RollingMinLow(sd.M5, breakoutBars),
		H1EMA20: ind.EMA(h1closes, 20),
		H1EMA50: ind.EMA(h1closes, 50),
	}
}

func closes(bars []model.Bar) []float64 {
	out := make([]float64, len(bars))
	for i, b := range bars {
		out[i] = b.Close
	}
	return out
}
