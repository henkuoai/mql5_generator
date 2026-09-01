# mql5_generator

一个用于 MT5 外汇交易的单页 MQL5 策略 GUI 生成器。你可以在网页里可视化编辑策略 JSON，配置指标、止盈止损、仓位、频率限制和风控过滤，然后导出可直接交给 MetaEditor 编译的 `.mq5` EA 源码。

## 功能特性

- **JSON + 可视化双向编辑**：左侧编辑 JSON，右侧实时渲染策略表单；修改右侧字段时也会同步回 JSON。
- **多种开单策略**：支持布林带、RSI、MACD、双均线、Stochastic、Williams %R、CCI、MFI。
- **多因子信号**：可组合多个指标因子，设置 BUY/SELL 投票方向和最少票数阈值。
- **仓位模式**：固定手数，或按账户余额百分比计算风险仓位。
- **风控与过滤**：点差上限、交易时段、最大持仓数、滑动窗口频率限制。
- **ATR 辅助过滤**：可启用 ATR 止损/止盈过滤、趋势 EMA 和 ADX 趋势强度过滤。
- **一键导出**：生成单文件、自包含的 MQL5 源码，可复制或下载为 `.mq5`。
- **暗色主题**：支持亮色/暗色切换，主题偏好保存在浏览器本地。

## 快速开始

1. 打开仓库中的 `mql5_generator.html`。
2. 在顶部选择一个示例策略，或点击 **打开 .json** 加载自己的配置。
3. 在 **策略编辑** 标签中检查或调整参数。
4. 点击 **生成 MQL5 源码**。
5. 切换到 **MQL5 源码** 标签，点击 **复制源码** 或 **下载 .mq5**。
6. 使用 MetaEditor 打开 `.mq5`，按 `F7` 编译。

页面使用 Tailwind CDN 渲染样式。首次打开建议保持网络可用；如果页面样式异常，请检查浏览器是否能访问 `https://cdn.tailwindcss.com`。

## 网页教程

### 1. 配置策略

- **EA 名称**：作为导出的 `.mq5` 文件名；只允许字母、数字和下划线。
- **加载示例**：快速体验布林带、RSI、MACD、均线交叉等模板。
- **打开 .json**：加载现有策略配置。
- **清空**：从空配置开始。

### 2. 编辑核心字段

- **开单策略**：选择指标、周期、指标参数、止盈点数、止损点数、Magic Number 和订单注释。
- **仓位控制**：选择 `fixed_lots` 固定手数，或 `risk_pct` 按账户余额风险百分比。
- **频率限制**：限制“每 N 小时最多 M 笔交易”。
- **风控过滤**：设置最大点差、交易时段和最大同时持仓数量。
- **辅助过滤器**：按需启用 ATR 止损/止盈、趋势 EMA、ADX 强度等条件。
- **多因子信号**：组合多个指标条件；每个因子投票 BUY 或 SELL，只有达到最少票数才开仓。

左侧 JSON 修改后会自动解析；解析成功时页面会显示同步时间，失败时会显示错误信息。

### 3. 生成与导出

1. 点击 **生成 MQL5 源码**。
2. 页面会自动切换到 **MQL5 源码** 标签。
3. 检查生成结果，点击 **复制源码** 或 **下载 .mq5**。

生成器会把当前 JSON 参数硬编码进 MQL5 源码，因此 EA 运行时不依赖 JSON 文件。

### 4. 编译与运行

1. 打开 MetaTrader 5，进入 **文件 → 打开数据文件夹**。
2. 将 `.mq5` 复制到 `MQL5\Experts\` 目录。
3. 用 MetaEditor 打开该文件，按 `F7` 编译。
4. 回到 MT5 的 Navigator/导航器，刷新后找到生成的 EA。
5. 先在 **Strategy Tester/策略测试器** 中回测。
6. 确认无误后挂到图表，并确保 MT5 的 **Algo Trading/自动交易** 已启用。

## 配置结构概览

常用顶层字段如下：

```json
{
  meta: {
    version: 2.0,
    description: 策略说明
  },
  strategy: {
    indicator: RSI,
    timeframe: PERIOD_M15,
    params: {
      period: 14,
      applied_price: PRICE_CLOSE,
      buy_below: 30,
      sell_above: 70
    },
    take_profit_points: 60,
    stop_loss_points: 30,
    magic_number: 100001,
    comment: rsi_reversal
  },
  position: {
    mode: risk_pct,
    lots: 0.01,
    risk_percent: 1.0
  },
  rate_limit: {
    window_hours: 6,
    max_trades: 1
  },
  filters: {
    aux_enabled: true,
    use_atr_stops: true,
    atr_period: 14,
    adx_min_strength: 25,
    trend_ema_period: 200
  },
  filter: {
    max_spread_points: 30,
    trading_hours: {
      start: 08:00,
      end: 22:00
    },
    max_open_positions: 3
  }
}
```

### 多因子配置

```json
{
  strategy: {
    factors_enabled: true,
    min_votes_buy: 2,
    min_votes_sell: 2,
    factors: [
      {
        name: RSI超卖,
        indicator: RSI,
        params: { period: 14, applied_price: PRICE_CLOSE },
        op: <,
        value: 30,
        direction: BUY
      },
      {
        name: 价格在EMA200上方,
        indicator: EMA,
        params: { period: 200, method: MODE_EMA },
        op: price_above_ema,
        direction: BUY
      }
    ]
  }
}
```

`strategy.factors` 中的每个因子都会投票 `+1`（BUY）或 `-1`（SELL）；BUY 票数达到 `min_votes_buy`，或 SELL 票数达到 `min_votes_sell` 时，才允许开对应方向的仓位。

## 项目结构

```text
.
├── mql5_generator.html   # 生成器页面、JSON 编辑器、MQL5 模板生成逻辑
├── README.md             # 使用说明
└── LICENSE               # 开源许可
```

## 注意事项

- 生成器的默认参数仅用于演示，不构成任何投资建议。
- 外汇交易存在风险；实盘前请先完成历史回测、模拟账户测试和参数压力测试。
- 导出的 EA 将参数硬编码进源码，修改策略后需要重新生成、编译并替换图表上的 EA。
