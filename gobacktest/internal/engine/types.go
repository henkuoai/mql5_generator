package engine

import (
	"time"

	"gobacktest/internal/data"
	"gobacktest/internal/model"
)

// Config 回测引擎全局配置
type Config struct {
	Balance        float64 // 起始入金（账户货币 USD）
	Leverage       float64 // 杠杆倍数（如 500）
	PathModel      string  // 盘中路径假设：neutral | adverse | random
	Seed           int64   // random 模式的随机种子（可复现）
	RandomRuns     int     // random 模式的重复次数（取均值/分位）
	SlippagePoints float64 // 每次成交的滑点（点）
	StopOutPct     float64 // 保证金水平低于该值强平（%），0 表示不启用
	MaxPositions   int     // 最大同时持仓数（0=不限）
	SwapHour       int     // 每日结算小时（-1=关闭库存费）
	Verbose        bool
}

// DefaultConfig 常用默认值：500 倍杠杆
func DefaultConfig() Config {
	return Config{
		Balance:        10000,
		Leverage:       500,
		PathModel:      "adverse",
		Seed:           42,
		RandomRuns:     20,
		SlippagePoints: 0,
		StopOutPct:     20,
		MaxPositions:   0,
		SwapHour:       -1,
	}
}

// Order 策略产生的开仓意图。
// 推荐用 SLDist/TPDist（价格距离），由引擎按实际成交价换算成绝对止损/止盈；
// 若策略需要结构位（如前高/前低），可直接填绝对价格 SL/TP（优先于距离）。
type Order struct {
	Symbol  string
	Side    model.Side
	Lots    float64
	SLDist  float64 // 止损距离（价格单位）
	TPDist  float64 // 止盈距离（价格单位）
	SL      float64 // 绝对止损价（可选，优先）
	TP      float64 // 绝对止盈价（可选，优先）
	RiskUSD float64 // 记录用：本单计划风险
	Tag     string  // 记录用：信号标签
}

// ManagePolicy 持仓管理策略（由策略提供）。
// TrailStartUSD/TrailDistUSD/StallMinProfitUSD 的"单位"由 TrailMode 决定，
// 以适配黄金专用策略（价格单位）与多品种通用策略（ATR 倍数 / 金额）。
type ManagePolicy struct {
	TrailMode         string  // price（价格单位，默认，忠实复刻 MT5 EA）| atr（ATR倍数）| money（USD金额）
	TrailStartUSD     float64 // 浮盈达到该阈值后启动跟踪止损（0=关闭）
	TrailDistUSD      float64 // 跟踪距离
	StallMinutes      float64 // 无新极值持续该时长即离场（0=关闭）
	StallMinProfitUSD float64 // 停滞离场的最低浮盈要求
	MaxHoldMinutes    float64 // 最长持仓（0=不限）
}

// Context 每个基准 bar 收盘时提供给策略的上下文
type Context struct {
	Now            time.Time
	BaseIdx        int
	Sym            model.Symbol
	SD             *data.SymbolData
	Ind            *data.Indicators
	Balance        float64
	Equity         float64
	FreeMargin     float64
	PeakEquity     float64
	DDPercent      float64
	HasPosition    bool
	OpenPositions  int
	LastEntryAgoM  float64 // 距上次开仓的分钟数（-1 表示从未开仓）
	EnteredThisBar bool
}

// Strategy 策略接口
type Strategy interface {
	Name() string
	OnBarClose(ctx *Context) []Order
	Policy() ManagePolicy
}

// Position 持仓
type Position struct {
	ID       int
	Symbol   string
	Sym      model.Symbol
	Side     model.Side
	Lots     float64
	EntryIdx int
	EntryT   time.Time
	EntryPx  float64
	SL       float64
	InitSL   float64
	TP       float64
	RiskUSD  float64
	Tag      string

	BestPx       float64 // 最有利价格（Bid）
	WorstPx      float64 // 最不利价格（Bid）
	LastExtreme  int     // 最近一次刷新极值的 bar 索引
	LastSwapDay  int
	CommissionIn float64
	BarsHeld     int
	TrailArmed   bool
}

// Trade 已完成交易
type Trade struct {
	ID         int
	Symbol     string
	Side       model.Side
	Lots       float64
	EntryTime  time.Time
	EntryPrice float64
	ExitTime   time.Time
	ExitPrice  float64
	SL         float64
	TP         float64
	InitSL     float64
	RiskUSD    float64
	GrossPnL   float64
	Commission float64
	Swap       float64
	NetPnL     float64
	Reason     string // SL / TP / TRAIL / STALL / TIMEOUT / STOPOUT / END
	MFEPrice   float64
	MAEPrice   float64
	BarsHeld   int
	Tag        string
}

// EquityPoint 净值曲线采样点
type EquityPoint struct {
	Time     time.Time
	Balance  float64
	Equity   float64
	Margin   float64
	OpenPos  int
	Drawdown float64 // 相对峰值百分比
}

// Account 账户状态
type Account struct {
	Balance  float64
	Equity   float64
	Margin   float64
	Leverage float64
}

// FreeMargin 可用保证金
func (a *Account) FreeMargin() float64 {
	return a.Equity - a.Margin
}

// MarginLevel 保证金水平（%）
func (a *Account) MarginLevel() float64 {
	if a.Margin <= 0 {
		return 1e9
	}
	return a.Equity / a.Margin * 100
}

// Result 回测输出
type Result struct {
	Config    Config
	Strategy  string
	Symbols   []string
	From, To  time.Time
	Trades    []*Trade
	Equity    []EquityPoint
	StartBal  float64
	EndBal    float64
	EndEquity float64
	PeakEquity float64
	MaxDD     float64
	MaxDDPct  float64
	Rejected  int // 因保证金不足被拒的订单数
	Skipped   int // 因手数过小等原因被跳过的订单数
}
