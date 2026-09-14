// Package report 输出控制台摘要、CSV 明细与自包含 HTML 报告。
package report

import (
	"encoding/csv"
	"fmt"
	"html"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gobacktest/internal/engine"
	"gobacktest/internal/metrics"
)

// Console 打印控制台摘要
func Console(res *engine.Result, st *metrics.Stats, stratStats map[string]int, topN int) {
	fmt.Println(strings.Repeat("=", 78))
	fmt.Printf("回测结果：%s\n", res.Strategy)
	fmt.Printf("品种：%s   杠杆：%.0f   路径模型：%s   起始资金：%.2f\n",
		strings.Join(res.Symbols, ", "), res.Config.Leverage, res.Config.PathModel, res.StartBal)
	fmt.Printf("区间：%s ~ %s\n", res.From.Format("2006-01-02 15:04"), res.To.Format("2006-01-02 15:04"))
	fmt.Println(strings.Repeat("-", 78))
	fmt.Printf("净利润        %12.2f      收益率      %8.2f%%\n", st.NetProfit, st.ReturnPct)
	fmt.Printf("期末余额      %12.2f      期末净值    %12.2f\n", st.EndBalance, st.EndEquity)
	fmt.Printf("交易次数      %12d      胜率        %8.2f%%  (%d胜/%d负)\n", st.Trades, st.WinRate, st.Wins, st.Losses)
	fmt.Printf("盈利因子      %12.2f      期望值/单   %12.2f\n", st.ProfitFactor, st.Expectancy)
	fmt.Printf("平均盈利      %12.2f      平均亏损    %12.2f\n", st.AvgWin, st.AvgLoss)
	fmt.Printf("最大盈利      %12.2f      最大亏损    %12.2f\n", st.LargestWin, st.LargestLoss)
	fmt.Printf("最大回撤      %12.2f      最大回撤%%   %8.2f%%\n", st.MaxDD, st.MaxDDPct)
	fmt.Printf("恢复因子      %12.2f      夏普(日)    %12.2f\n", st.RecoveryFact, st.Sharpe)
	fmt.Printf("连胜/连亏     %6d/%-6d  平均持仓    %8.1f 分钟\n", st.MaxWinStreak, st.MaxLossStreak, st.AvgHoldMin)
	fmt.Printf("手续费        %12.2f      多头/空头   %6d/%d\n", st.Commission, st.Longs, st.Shorts)
	if res.Rejected > 0 || res.Skipped > 0 {
		fmt.Printf("保证金拒单    %12d      跳过订单    %12d\n", res.Rejected, res.Skipped)
	}

	fmt.Println(strings.Repeat("-", 78))
	fmt.Println("分品种：")
	fmt.Printf("  %-10s %8s %8s %10s %10s %10s\n", "品种", "交易数", "胜率", "净盈亏", "均值/单", "多/空")
	var names []string
	for n := range st.BySymbol {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := st.BySymbol[n]
		fmt.Printf("  %-10s %8d %7.1f%% %10.2f %10.2f %6d/%d\n",
			n, s.Trades, s.WinRate, s.NetPnL, s.AvgPnL, s.Longs, s.Shorts)
	}

	fmt.Println(strings.Repeat("-", 78))
	fmt.Println("离场原因：")
	var reasons []string
	for r := range st.ByReason {
		reasons = append(reasons, r)
	}
	sort.Slice(reasons, func(a, b int) bool { return st.ByReason[reasons[a]] > st.ByReason[reasons[b]] })
	for _, r := range reasons {
		fmt.Printf("  %-12s %6d\n", metrics.ReasonLabel(r), st.ByReason[r])
	}

	if len(st.Monthly) > 0 {
		fmt.Println(strings.Repeat("-", 78))
		fmt.Println("分月表现：")
		fmt.Printf("  %-9s %8s %8s %12s %10s\n", "月份", "交易数", "胜率", "净盈亏", "月收益%")
		for _, m := range st.Monthly {
			wr := 0.0
			if m.Trades > 0 {
				wr = float64(m.Wins) / float64(m.Trades) * 100
			}
			fmt.Printf("  %-9s %8d %7.1f%% %12.2f %9.2f%%\n", m.Month, m.Trades, wr, m.NetPnL, m.ReturnPct)
		}
	}

	if len(stratStats) > 0 {
		fmt.Println(strings.Repeat("-", 78))
		fmt.Println("信号漏斗（策略内部计数，用于调参）：")
		type kv struct {
			k string
			v int
		}
		var arr []kv
		for k, v := range stratStats {
			arr = append(arr, kv{k, v})
		}
		sort.Slice(arr, func(a, b int) bool { return arr[a].v > arr[b].v })
		limit := len(arr)
		if topN > 0 && limit > topN {
			limit = topN
		}
		for _, e := range arr[:limit] {
			fmt.Printf("  %-14s %8d\n", e.k, e.v)
		}
	}
	fmt.Println(strings.Repeat("=", 78))
}

// TradesCSV 写出成交明细
func TradesCSV(path string, res *engine.Result) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	_ = w.Write([]string{"id", "symbol", "side", "lots", "entry_time", "entry_price",
		"exit_time", "exit_price", "sl", "tp", "reason", "gross", "commission", "net",
		"mfe_price", "mae_price", "bars_held", "tag"})
	for _, t := range res.Trades {
		_ = w.Write([]string{
			strconv.Itoa(t.ID), t.Symbol, t.Side.String(), f2(t.Lots),
			t.EntryTime.Format("2006-01-02 15:04:05"), f5(t.EntryPrice),
			t.ExitTime.Format("2006-01-02 15:04:05"), f5(t.ExitPrice),
			f5(t.SL), f5(t.TP), t.Reason, f2(t.GrossPnL), f2(t.Commission), f2(t.NetPnL),
			f5(t.MFEPrice), f5(t.MAEPrice), strconv.Itoa(t.BarsHeld), t.Tag,
		})
	}
	return nil
}

// EquityCSV 写出净值曲线
func EquityCSV(path string, res *engine.Result) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	_ = w.Write([]string{"time", "balance", "equity", "margin", "open_positions", "drawdown_pct"})
	for _, p := range res.Equity {
		_ = w.Write([]string{
			p.Time.Format("2006-01-02 15:04:05"), f2(p.Balance), f2(p.Equity),
			f2(p.Margin), strconv.Itoa(p.OpenPos), f4(p.Drawdown),
		})
	}
	return nil
}

// HTML 生成自包含 HTML 报告
func HTML(path string, res *engine.Result, st *metrics.Stats, stratStats map[string]int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8">`)
	b.WriteString("<title>Go Backtest 报告 - " + html.EscapeString(res.Strategy) + "</title>")
	b.WriteString(`<style>
body{font-family:"Segoe UI","Microsoft YaHei",system-ui,sans-serif;margin:0;background:#f4f6f8;color:#2c3e50;line-height:1.6}
.wrap{max-width:1000px;margin:0 auto;padding:28px}
h1{font-size:24px;border-bottom:3px solid #6c5ce7;padding-bottom:8px}
h2{font-size:18px;margin-top:32px;color:#6c5ce7}
table{border-collapse:collapse;width:100%;font-size:13px;margin:10px 0}
th,td{border:1px solid #e3e6ea;padding:6px 10px;text-align:center}
th{background:#f0f2f5}
.pos{color:#c0392b;font-weight:600}.neg{color:#1e8449;font-weight:600}
.kpi{display:flex;flex-wrap:wrap;gap:10px;margin:14px 0}
.kpi div{flex:1;min-width:130px;background:#fff;border-radius:8px;padding:10px;text-align:center;box-shadow:0 1px 3px rgba(0,0,0,.08)}
.kpi b{display:block;font-size:18px;color:#6c5ce7}
.kpi span{font-size:12px;color:#5d6d7e}
.muted{color:#7f8c8d;font-size:12px}
.card{background:#fff;border-radius:10px;padding:16px;margin:12px 0;box-shadow:0 1px 4px rgba(0,0,0,.08)}
</style></head><body><div class="wrap">`)
	fmt.Fprintf(&b, "<h1>Go 回测报告 · %s</h1>", html.EscapeString(res.Strategy))
	fmt.Fprintf(&b, `<p class="muted">品种 %s ｜ 杠杆 %.0f× ｜ 路径模型 %s ｜ 区间 %s ~ %s</p>`,
		html.EscapeString(strings.Join(res.Symbols, ", ")), res.Config.Leverage, res.Config.PathModel,
		res.From.Format("2006-01-02"), res.To.Format("2006-01-02"))

	// KPI
	b.WriteString(`<div class="kpi">`)
	kpi := func(v, label string, good bool) {
		cl := ""
		if v != "" {
			if good {
				cl = "pos"
			} else {
				cl = "neg"
			}
		}
		fmt.Fprintf(&b, `<div><b class="%s">%s</b><span>%s</span></div>`, cl, v, label)
	}
	kpi(fmt.Sprintf("%.2f", st.NetProfit), "净利润", st.NetProfit >= 0)
	kpi(fmt.Sprintf("%.2f%%", st.ReturnPct), "收益率", st.ReturnPct >= 0)
	kpi(strconv.Itoa(st.Trades), "交易次数", false)
	kpi(fmt.Sprintf("%.1f%%", st.WinRate), "胜率", false)
	kpi(fmt.Sprintf("%.2f", st.ProfitFactor), "盈利因子", st.ProfitFactor >= 1)
	kpi(fmt.Sprintf("%.2f%%", st.MaxDDPct), "最大回撤", false)
	kpi(fmt.Sprintf("%.2f", st.Sharpe), "夏普(日)", st.Sharpe > 0)
	kpi(fmt.Sprintf("%.2f", st.Expectancy), "期望值/单", st.Expectancy >= 0)
	b.WriteString(`</div>`)

	// 净值曲线
	b.WriteString(`<div class="card"><h2>净值曲线</h2>`)
	b.WriteString(equitySVG(res))
	b.WriteString(`</div>`)

	// 分品种
	b.WriteString(`<div class="card"><h2>分品种</h2><table><tr><th>品种</th><th>交易</th><th>胜率</th><th>净盈亏</th><th>均值/单</th><th>多/空</th></tr>`)
	var names []string
	for n := range st.BySymbol {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := st.BySymbol[n]
		cls := "pos"
		if s.NetPnL < 0 {
			cls = "neg"
		}
		fmt.Fprintf(&b, "<tr><td>%s</td><td>%d</td><td>%.1f%%</td><td class=\"%s\">%.2f</td><td>%.2f</td><td>%d/%d</td></tr>",
			html.EscapeString(n), s.Trades, s.WinRate, cls, s.NetPnL, s.AvgPnL, s.Longs, s.Shorts)
	}
	b.WriteString(`</table></div>`)

	// 分月
	if len(st.Monthly) > 0 {
		b.WriteString(`<div class="card"><h2>分月表现</h2><table><tr><th>月份</th><th>交易</th><th>胜率</th><th>净盈亏</th><th>月收益</th></tr>`)
		for _, m := range st.Monthly {
			wr := 0.0
			if m.Trades > 0 {
				wr = float64(m.Wins) / float64(m.Trades) * 100
			}
			cls := "pos"
			if m.NetPnL < 0 {
				cls = "neg"
			}
			fmt.Fprintf(&b, "<tr><td>%s</td><td>%d</td><td>%.1f%%</td><td class=\"%s\">%.2f</td><td>%.2f%%</td></tr>",
				m.Month, m.Trades, wr, cls, m.NetPnL, m.ReturnPct)
		}
		b.WriteString(`</table></div>`)
	}

	// 离场原因
	b.WriteString(`<div class="card"><h2>离场原因分布</h2><table><tr><th>原因</th><th>次数</th></tr>`)
	var reasons []string
	for r := range st.ByReason {
		reasons = append(reasons, r)
	}
	sort.Slice(reasons, func(a, b int) bool { return st.ByReason[reasons[a]] > st.ByReason[reasons[b]] })
	for _, r := range reasons {
		fmt.Fprintf(&b, "<tr><td>%s</td><td>%d</td></tr>", metrics.ReasonLabel(r), st.ByReason[r])
	}
	b.WriteString(`</table></div>`)

	// 信号漏斗
	if len(stratStats) > 0 {
		b.WriteString(`<div class="card"><h2>信号漏斗（调参用）</h2><table><tr><th>环节</th><th>次数</th></tr>`)
		type kv struct {
			k string
			v int
		}
		var arr []kv
		for k, v := range stratStats {
			arr = append(arr, kv{k, v})
		}
		sort.Slice(arr, func(a, b int) bool { return arr[a].v > arr[b].v })
		for _, e := range arr {
			fmt.Fprintf(&b, "<tr><td>%s</td><td>%d</td></tr>", html.EscapeString(e.k), e.v)
		}
		b.WriteString(`</table></div>`)
	}

	fmt.Fprintf(&b, `<p class="muted">起始资金 %.2f ｜ 期末余额 %.2f ｜ 期末净值 %.2f ｜ 手续费 %.2f ｜ 保证金拒单 %d</p>`,
		st.StartBalance, st.EndBalance, st.EndEquity, st.Commission, st.RejectedOrders)
	b.WriteString(`</div></body></html>`)
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// equitySVG 生成内联 SVG 净值曲线
func equitySVG(res *engine.Result) string {
	pts := res.Equity
	// 按天降采样，避免点数过多
	var sample []engine.EquityPoint
	lastDay := ""
	for _, p := range pts {
		d := p.Time.Format("2006-01-02")
		if d != lastDay {
			sample = append(sample, p)
			lastDay = d
		}
	}
	if len(sample) > 1 {
		sample[len(sample)-1] = pts[len(pts)-1]
	}
	if len(sample) < 2 {
		return "<p class='muted'>净值点不足</p>"
	}
	const W, H = 940, 320
	const L, R, T, B = 70, 20, 20, 40
	minV, maxV := math.Inf(1), math.Inf(-1)
	for _, p := range sample {
		minV = math.Min(minV, p.Equity)
		maxV = math.Max(maxV, p.Equity)
	}
	if maxV == minV {
		maxV = minV + 1
	}
	pad := (maxV - minV) * 0.08
	minV -= pad
	maxV += pad
	x := func(i int) float64 { return L + float64(i)*(W-L-R)/float64(len(sample)-1) }
	y := func(v float64) float64 { return T + (H-T-B)*(1-(v-minV)/(maxV-minV)) }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" style="width:100%%;height:auto;background:#fff;font-family:system-ui">`, W, H)
	// 网格
	for i := 0; i <= 4; i++ {
		v := minV + (maxV-minV)*float64(i)/4
		yy := y(v)
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="#eef1f4"/>`, L, yy, W-R, yy)
		fmt.Fprintf(&b, `<text x="6" y="%.1f" font-size="11" fill="#7f8c8d">%.0f</text>`, yy+4, v)
	}
	// 起始资金基准线
	base := y(res.StartBal)
	fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="#bdc3c7" stroke-dasharray="4,4"/>`, L, base, W-R, base)
	// 曲线
	var path strings.Builder
	for i, p := range sample {
		if i == 0 {
			fmt.Fprintf(&path, "M %.1f %.1f", x(i), y(p.Equity))
		} else {
			fmt.Fprintf(&path, " L %.1f %.1f", x(i), y(p.Equity))
		}
	}
	fmt.Fprintf(&b, `<path d="%s" fill="none" stroke="#6c5ce7" stroke-width="2"/>`, path.String())
	// 标注
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" fill="#7f8c8d">%s</text>`, L, H-12, sample[0].Time.Format("2006-01-02"))
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" fill="#7f8c8d" text-anchor="end">%s</text>`,
		W-R, H-12, sample[len(sample)-1].Time.Format("2006-01-02"))
	last := sample[len(sample)-1].Equity
	fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="3" fill="#6c5ce7"/>`, x(len(sample)-1), y(last))
	b.WriteString(`</svg>`)
	return b.String()
}

func f2(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
func f4(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }
func f5(v float64) string { return strconv.FormatFloat(v, 'f', 5, 64) }
