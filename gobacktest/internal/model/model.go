// Package model 定义回测引擎的核心数据结构：K线、周期、品种规格。
package model

import (
	"fmt"
	"math"
	"time"
)

// Side 交易方向
type Side int

const (
	SideNone Side = iota
	SideBuy
	SideSell
)

func (s Side) String() string {
	switch s {
	case SideBuy:
		return "BUY"
	case SideSell:
		return "SELL"
	}
	return "NONE"
}

// Sign 返回方向系数：Buy=+1, Sell=-1
func (s Side) Sign() float64 {
	if s == SideSell {
		return -1
	}
	return 1
}

// Kind 品种类型，决定保证金/名义价值的换算方式
type Kind string

const (
	KindFX    Kind = "fx"    // 外汇（含交叉盘）
	KindMetal Kind = "metal" // 贵金属
	KindIndex Kind = "index" // 指数
	KindCFD   Kind = "cfd"
)

// Symbol 品种合约规格（与 MT5 symbol_info 对齐）
type Symbol struct {
	Name             string  `json:"name"`
	Kind             Kind    `json:"kind"`
	Digits           int     `json:"digits"`
	Point            float64 `json:"point"`
	ContractSize     float64 `json:"contract_size"`
	SpreadPoints     float64 `json:"spread_points"`       // 默认点差（点）
	CommissionPerLot float64 `json:"commission_per_lot"`  // 单边手续费 USD/手
	QuoteToUSD       float64 `json:"quote_to_usd"`        // 计价货币 -> USD 汇率
	MinLot           float64 `json:"min_lot"`
	MaxLot           float64 `json:"max_lot"`
	LotStep          float64 `json:"lot_step"`
	SwapLongPerLot   float64 `json:"swap_long_per_lot"`   // 每日多单库存费 USD/手
	SwapShortPerLot  float64 `json:"swap_short_per_lot"`  // 每日空单库存费 USD/手
}

// MoneyPerPricePerLot 1 手、价格变动 1.0 个货币单位时的盈亏（USD）
func (s Symbol) MoneyPerPricePerLot() float64 {
	return s.ContractSize * s.QuoteToUSD
}

// MoneyPerPointPerLot 1 手、价格变动 1 个 point 时的盈亏（USD）
func (s Symbol) MoneyPerPointPerLot() float64 {
	return s.MoneyPerPricePerLot() * s.Point
}

// SpreadPrice 点差换算为价格单位
func (s Symbol) SpreadPrice() float64 {
	return s.SpreadPoints * s.Point
}

// PnL 计算平仓盈亏（USD），不含手续费
func (s Symbol) PnL(lots, entry, exit float64, side Side) float64 {
	d := (exit - entry) * side.Sign()
	return d * s.MoneyPerPricePerLot() * lots
}

// NotionalUSD 名义价值（USD），用于保证金计算
func (s Symbol) NotionalUSD(lots, price float64) float64 {
	return lots * s.ContractSize * price * s.QuoteToUSD
}

// MarginRequired 所需保证金（USD）
func (s Symbol) MarginRequired(lots, price, leverage float64) float64 {
	if leverage <= 0 {
		leverage = 1
	}
	return s.NotionalUSD(lots, price) / leverage
}

// NormalizeLots 按最小手/步长规范化手数，并夹在 [MinLot, MaxLot]
func (s Symbol) NormalizeLots(lots float64) float64 {
	if lots <= 0 {
		return 0
	}
	step := s.LotStep
	if step <= 0 {
		step = 0.01
	}
	lots = math.Floor(lots/step+1e-9) * step
	if lots < s.MinLot {
		// 不足最小手：向上取到最小手（小账户常见），由调用方决定是否接受
		lots = s.MinLot
	}
	if s.MaxLot > 0 && lots > s.MaxLot {
		lots = s.MaxLot
	}
	return roundTo(lots, 2)
}

// RoundPrice 按品种精度取整价格
func (s Symbol) RoundPrice(p float64) float64 {
	return roundTo(p, s.Digits)
}

func roundTo(v float64, digits int) float64 {
	pow := math.Pow10(digits)
	return math.Round(v*pow) / pow
}

// Bar 一根K线（价格为 Bid 序列）
type Bar struct {
	Time   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume int64
	Spread float64 // 该K线的点差（点）
}

func (b Bar) String() string {
	return fmt.Sprintf("%s O=%.5f H=%.5f L=%.5f C=%.5f",
		b.Time.Format("2006-01-02 15:04"), b.Open, b.High, b.Low, b.Close)
}

// TF 时间周期（分钟）
type TF int

const (
	M1  TF = 1
	M5  TF = 5
	M15 TF = 15
	M30 TF = 30
	H1  TF = 60
	H4  TF = 240
	D1  TF = 1440
)

func (t TF) Duration() time.Duration {
	return time.Duration(int(t)) * time.Minute
}

func (t TF) Label() string {
	switch t {
	case M1:
		return "M1"
	case M5:
		return "M5"
	case M15:
		return "M15"
	case M30:
		return "M30"
	case H1:
		return "H1"
	case H4:
		return "H4"
	case D1:
		return "D1"
	}
	return fmt.Sprintf("%dm", int(t))
}

func (t TF) Seconds() float64 { return float64(int(t)) * 60 }
