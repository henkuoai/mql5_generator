# mql5_generator

A single-page MQL5 strategy GUI generator for MT5 forex trading. Visually edit your strategy JSON in the browser, configure indicators, take-profit / stop-loss, position sizing, frequency limits and risk filters, then export a ready-to-compile `.mq5` EA source file for MetaEditor.

## Features

- **Bi-directional JSON + visual editing** — Edit JSON on the left, see the strategy form rendered live on the right. Changes made in the form are synced back to JSON.
- **Multiple entry strategies** — Bollinger Bands, RSI, MACD, dual moving averages, Stochastic, Williams %R, CCI and MFI.
- **Multi-factor signals** — Combine multiple indicator factors, configure BUY/SELL vote direction and a minimum-vote threshold.
- **Position sizing** — Fixed lot size, or risk-based sizing as a percentage of account balance.
- **Risk control & filters** — Max spread, trading hours, max open positions, sliding-window frequency limit.
- **ATR-based auxiliary filters** — Optional ATR stop-loss / take-profit filter, trend EMA and ADX trend-strength filter.
- **One-click export** — Generates a single-file, self-contained MQL5 source that you can copy or download as `.mq5`.
- **Dark theme** — Light / dark mode toggle with the preference saved in the browser's local storage.

## Quick Start

1. Open `mql5_generator.html` from the repository.
2. Pick an example strategy from the top, or click **Open .json** to load your own configuration.
3. Review or adjust the parameters in the **Strategy Editor** tab.
4. Click **Generate MQL5 Source**.
5. Switch to the **MQL5 Source** tab, then click **Copy Source** or **Download .mq5**.
6. Open the `.mq5` file in MetaEditor and press `F7` to compile.

The page uses a local copy of the Tailwind Play CDN script (`vendor/tailwindcss.js`) for styling, so it works fully offline — no network required.

## Web Tutorial

### 1. Configure the Strategy

- **EA Name** — Used as the exported `.mq5` file name; letters, digits and underscores only.
- **Load Example** — Quickly try templates such as Bollinger Bands, RSI, MACD or moving-average crossover.
- **Open .json** — Load an existing strategy configuration.
- **Clear** — Start from an empty configuration.

### 2. Edit Core Fields

- **Entry Strategy** — Select the indicator, timeframe, indicator parameters, take-profit points, stop-loss points, Magic Number and order comment.
- **Position Control** — Choose `fixed_lots` for a fixed lot size, or `risk_pct` to risk a percentage of account balance.
- **Frequency Limit** — Cap trades to "at most M trades per N hours".
- **Risk Filters** — Configure max spread, trading hours and max simultaneous open positions.
- **Auxiliary Filters** — Optionally enable ATR stop-loss / take-profit, trend EMA, ADX strength and similar conditions.
- **Multi-factor Signals** — Combine multiple indicator conditions; each factor votes BUY or SELL, and a trade is only opened once the minimum vote count is reached.

JSON edits on the left are parsed automatically. On a successful parse the page shows the sync time; on failure it shows the error message.

### 3. Generate & Export

1. Click **Generate MQL5 Source**.
2. The page automatically switches to the **MQL5 Source** tab.
3. Review the generated output, then click **Copy Source** or **Download .mq5**.

The generator hard-codes the current JSON parameters into the MQL5 source, so the EA does not depend on the JSON file at runtime.

### 4. Compile & Run

1. Open MetaTrader 5 and go to **File → Open Data Folder**.
2. Copy the `.mq5` file into the `MQL5\Experts\` directory.
3. Open the file in MetaEditor and press `F7` to compile.
4. Return to the MT5 Navigator, refresh it, then locate the generated EA.
5. Backtest it first in the **Strategy Tester**.
6. Once you are confident, attach it to a chart and make sure **Algo Trading** is enabled in MT5.

## Configuration Overview

Common top-level fields:

```json
{
  meta: {
    version: 2.0,
    description: Strategy description
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
      start: "08:00",
      end: "22:00"
    },
    max_open_positions: 3
  }
}
```

### Multi-factor Configuration

```json
{
  strategy: {
    factors_enabled: true,
    min_votes_buy: 2,
    min_votes_sell: 2,
    factors: [
      {
        name: "RSI Oversold",
        indicator: RSI,
        params: { period: 14, applied_price: PRICE_CLOSE },
        op: "<",
        value: 30,
        direction: BUY
      },
      {
        name: "Price Above EMA200",
        indicator: EMA,
        params: { period: 200, method: MODE_EMA },
        op: price_above_ema,
        direction: BUY
      }
    ]
  }
}
```

Each factor in `strategy.factors` casts a vote of `+1` (BUY) or `-1` (SELL). A BUY position is opened only when the BUY vote count reaches `min_votes_buy`, and a SELL position is opened only when the SELL vote count reaches `min_votes_sell`.

## Project Structure

```text
.
├── mql5_generator.html   # Generator page, JSON editor, MQL5 template generation logic
├── vendor/
│   └── tailwindcss.js    # Local copy of the Tailwind Play CDN script
├── README.md             # English documentation
├── README.cn.md          # Chinese documentation
└── LICENSE               # Open source license
```

## Notes

- The generator's default parameters are for demonstration only and do not constitute investment advice.
- Forex trading carries risk. Always run historical backtests, demo-account tests and parameter stress tests before going live.
- The exported EA hard-codes its parameters into the source. After modifying a strategy, you must regenerate, recompile and replace the EA on the chart.