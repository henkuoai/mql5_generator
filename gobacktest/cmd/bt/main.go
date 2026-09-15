// Command bt 是一个多品种外汇/黄金/指数回测器（Go 实现）。
//
// 用法示例：
//
//	bt -data ./data -symbols EURUSD,EURGBP,XAUUSD,US500 -tf M5 -strategy qt-v2 \
//	   -balance 10000 -leverage 500 -path adverse -out ./out
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gobacktest/internal/data"
	"gobacktest/internal/engine"
	"gobacktest/internal/metrics"
	"gobacktest/internal/model"
	"gobacktest/internal/report"
	"gobacktest/internal/strategy"
)

func main() {
	var (
		dataDir   = flag.String("data", "./data", "历史数据目录（CSV：SYMBOL_TF.csv）")
		symbolsCS = flag.String("symbols", "EURUSD,EURGBP,XAUUSD,US500", "品种列表（逗号分隔，all=全部默认）")
		tfStr     = flag.String("tf", "M5", "基准周期：M1 或 M5")
		stratName = flag.String("strategy", "qt-v2", "策略：qt-v1 | qt-v2 | qt | ema")
		balance   = flag.Float64("balance", 10000, "起始资金")
		leverage  = flag.Float64("leverage", 500, "杠杆倍数")
		pathModel = flag.String("path", "adverse", "盘中路径模型：neutral | adverse | random")
		seed      = flag.Int64("seed", 42, "random 模式随机种子")
		slippage  = flag.Float64("slip", 0, "每次成交滑点（点）")
		stopOut   = flag.Float64("stopout", 20, "强平保证金水平(%)，0=关闭")
		maxPos    = flag.Int("maxpos", 0, "最大同时持仓数，0=不限")
		fromStr   = flag.String("from", "", "起始日期 YYYY-MM-DD")
		toStr     = flag.String("to", "", "结束日期 YYYY-MM-DD")
		outDir    = flag.String("out", "./out", "报告输出目录")
		quiet     = flag.Bool("quiet", false, "只打印摘要")
		funnelN   = flag.Int("funnel", 18, "信号漏斗显示条目数")
		riskPct   = flag.Float64("risk", 0, "覆盖策略风险%（0=用策略默认）")
		ddHalt    = flag.Float64("ddhalt", -1, "覆盖最大回撤熔断%（0=关闭，-1=用策略默认）")
		minIvl    = flag.Float64("mininterval", -1, "覆盖最小开仓间隔分钟（-1=用策略默认）")
		maxHold   = flag.Float64("maxhold", -1, "覆盖最长持仓分钟（-1=用策略默认）")
	)
	flag.Parse()

	// ---- 品种 ----
	var names []string
	if strings.EqualFold(*symbolsCS, "all") {
		for n := range model.DefaultSymbols {
			names = append(names, n)
		}
	} else {
		for _, s := range strings.Split(*symbolsCS, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				names = append(names, s)
			}
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		fatal("未指定品种")
	}

	tf, err := parseTF(*tfStr)
	if err != nil {
		fatal(err.Error())
	}

	// ---- 载入数据 ----
	syms := map[string]model.Symbol{}
	sdMap := map[string]*data.SymbolData{}
	indMap := map[string]*data.Indicators{}

	qtp := stratParams(*stratName)
	breakout := 6
	atrP, adxP := 14, 14
	if qtp != nil {
		breakout = qtp.BreakoutBars
	}

	from, to := parseDate(*fromStr), parseDate(*toStr)

	for _, n := range names {
		sym, ok := model.DefaultSymbols[n]
		if !ok {
			fmt.Printf("!! 未知品种 %s，跳过（可在 model/symbols.go 中添加规格）\n", n)
			continue
		}
		file := filepath.Join(*dataDir, fmt.Sprintf("%s_%s.csv", n, tf.Label()))
		bars, err := data.LoadBars(file)
		if err != nil {
			fmt.Printf("!! 载入 %s 失败：%v（跳过）\n", file, err)
			continue
		}
		// 时间过滤
		filtered := bars[:0]
		for _, b := range bars {
			if !from.IsZero() && b.Time.Before(from) {
				continue
			}
			if !to.IsZero() && b.Time.After(to.Add(24*time.Hour)) {
				continue
			}
			filtered = append(filtered, b)
		}
		bars = filtered
		if len(bars) == 0 {
			fmt.Printf("!! %s 在指定区间内无数据\n", n)
			continue
		}
		sym.Name = n
		syms[n] = sym
		sd := data.BuildSymbolData(sym, bars, tf, breakout)
		sdMap[n] = sd
		indMap[n] = data.ComputeIndicators(sd, breakout, atrP, adxP)
		fmt.Printf("载入 %-8s %s: %6d 根 (%s ~ %s)\n", n, tf.Label(), len(bars),
			bars[0].Time.Format("2006-01-02"), bars[len(bars)-1].Time.Format("2006-01-02"))
		if sd.Clamped {
			fmt.Printf("   !! 基准周期 %s 比策略信号周期(M5)更粗，无法由 %s 推导出 M5：\n"+
				"      信号将直接在 %s K线上计算，结果不等价于原 M5 策略。精细回测请用 -tf M1 或 M5。\n",
				tf.Label(), tf.Label(), tf.Label())
		}
	}
	if len(sdMap) == 0 {
		fatal("没有可用数据。请先用 scripts/export_mt5.py 导出 CSV。")
	}

	// ---- 策略 ----
	params, applyOverrides := buildQTParams(*stratName)
	var strat engine.Strategy
	switch {
	case params != nil:
		applyOverrides(params, *riskPct, *ddHalt, *minIvl, *maxHold)
		strat = strategy.NewQT(*params)
	default:
		ep := strategy.DefaultEMA()
		if *riskPct > 0 {
			ep.RiskPercent = *riskPct
		}
		if *maxHold > 0 {
			ep.MaxHoldMin = *maxHold
		}
		strat = strategy.NewEMA(ep)
	}

	cfg := engine.DefaultConfig()
	cfg.Balance = *balance
	cfg.Leverage = *leverage
	cfg.PathModel = *pathModel
	cfg.Seed = *seed
	cfg.SlippagePoints = *slippage
	cfg.StopOutPct = *stopOut
	cfg.MaxPositions = *maxPos
	cfg.SwapHour = -1

	eng := engine.New(cfg, syms, sdMap, indMap, strat)
	res := eng.Run()
	res.Symbols = sortedKeys(sdMap)

	st := metrics.Compute(res)
	var sstats map[string]int
	if sr, ok := strat.(interface{ Stats() map[string]int }); ok {
		sstats = sr.Stats()
	}

	report.Console(res, st, sstats, *funnelN)
	if *quiet {
		return
	}

	// ---- 输出文件 ----
	base := fmt.Sprintf("%s_%s_%s", *stratName, tf.Label(), time.Now().Format("20060102-150405"))
	tradesPath := filepath.Join(*outDir, base+"_trades.csv")
	eqPath := filepath.Join(*outDir, base+"_equity.csv")
	htmlPath := filepath.Join(*outDir, base+"_report.html")
	if err := report.TradesCSV(tradesPath, res); err != nil {
		fmt.Println("写 trades.csv 失败:", err)
	}
	if err := report.EquityCSV(eqPath, res); err != nil {
		fmt.Println("写 equity.csv 失败:", err)
	}
	if err := report.HTML(htmlPath, res, st, sstats); err != nil {
		fmt.Println("写 report.html 失败:", err)
	}
	fmt.Println("输出：")
	fmt.Println("  ", tradesPath)
	fmt.Println("  ", eqPath)
	fmt.Println("  ", htmlPath)
}

// stratParams 返回用于确定指标窗口的参数（nil 表示非 qt 策略）
func stratParams(name string) *strategy.QTParams {
	p, _ := buildQTParams(name)
	return p
}

// buildQTParams 构造策略参数并返回覆盖函数
func buildQTParams(name string) (*strategy.QTParams, func(p *strategy.QTParams, risk, ddHalt, minIvl, maxHold float64)) {
	var p strategy.QTParams
	switch strings.ToLower(name) {
	case "qt", "qt-v1", "qtv1":
		p = strategy.QTV1()
	case "qt-v2", "qtv2":
		p = strategy.QTV2()
	default:
		return nil, nil
	}
	apply := func(pp *strategy.QTParams, risk, ddHalt, minIvl, maxHold float64) {
		if risk > 0 {
			pp.RiskPercent = risk
		}
		if ddHalt >= 0 {
			pp.MaxDDHaltPct = ddHalt
		}
		if minIvl >= 0 {
			pp.MinIntervalMinutes = minIvl
		}
		if maxHold > 0 {
			pp.MaxHoldMinutes = maxHold
		}
	}
	return &p, apply
}

func parseTF(s string) (model.TF, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "M1":
		return model.M1, nil
	case "M5":
		return model.M5, nil
	case "M15":
		return model.M15, nil
	case "M30":
		return model.M30, nil
	case "H1":
		return model.H1, nil
	case "H4":
		return model.H4, nil
	case "D1":
		return model.D1, nil
	}
	return 0, fmt.Errorf("不支持的周期: %s（可用 M1/M5/M15/M30/H1/H4/D1）", s)
}

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, l := range []string{"2006-01-02", "2006.01.02", "2006/01/02"} {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func sortedKeys(m map[string]*data.SymbolData) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "错误:", msg)
	os.Exit(1)
}

var _ = strconv.Itoa
