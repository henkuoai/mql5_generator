package model

// 常用品种的默认规格（基于 Exness-MT5 真实账户实测的 symbol_info）。
// 若你的经纪商点差/合约不同，可用 symbols.json 覆盖。
var DefaultSymbols = map[string]Symbol{
	"EURUSD": {
		Name: "EURUSD", Kind: KindFX, Digits: 5, Point: 0.00001,
		ContractSize: 100000, SpreadPoints: 5, CommissionPerLot: 3.5, QuoteToUSD: 1.0,
		MinLot: 0.01, MaxLot: 200, LotStep: 0.01,
	},
	"EURGBP": {
		Name: "EURGBP", Kind: KindFX, Digits: 5, Point: 0.00001,
		ContractSize: 100000, SpreadPoints: 4, CommissionPerLot: 3.5,
		// 盈亏与名义价值都以 GBP 计，需换算成 USD（GBPUSD 近似汇率，可覆盖）
		QuoteToUSD: 1.34,
		MinLot:     0.01, MaxLot: 200, LotStep: 0.01,
	},
	"XAUUSD": {
		Name: "XAUUSD", Kind: KindMetal, Digits: 3, Point: 0.001,
		ContractSize: 100, SpreadPoints: 90, CommissionPerLot: 3.5, QuoteToUSD: 1.0,
		MinLot: 0.01, MaxLot: 200, LotStep: 0.01,
	},
	"US500": {
		Name: "US500", Kind: KindIndex, Digits: 2, Point: 0.01,
		ContractSize: 1, SpreadPoints: 3, CommissionPerLot: 0.0, QuoteToUSD: 1.0,
		MinLot: 0.01, MaxLot: 500, LotStep: 0.01,
	},
	// 便于校验/扩展
	"GBPUSD": {
		Name: "GBPUSD", Kind: KindFX, Digits: 5, Point: 0.00001,
		ContractSize: 100000, SpreadPoints: 6, CommissionPerLot: 3.5, QuoteToUSD: 1.0,
		MinLot: 0.01, MaxLot: 200, LotStep: 0.01,
	},
}
