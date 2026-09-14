#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""
从本机 MetaTrader 5 终端导出回测所需的 CSV 数据。

用法:
    python export_mt5.py                        # 导出默认品种的 M5 + M1
    python export_mt5.py --tf M5,M1             # 指定周期
    python export_mt5.py --symbols XAUUSD       # 指定品种
    python export_mt5.py --out ../data          # 指定输出目录
    python export_mt5.py --terminal "C:\\Program Files\\MetaTrader 5\\terminal64.exe"

输出文件: {out}/{SYMBOL}_{TF}.csv  表头 time,open,high,low,close,tick_volume,spread
注意: MT5 Python 接口只能读取"当前已登录终端"有的历史。数据不足时请先在
      MT5 里打开该品种的对应周期图表，让终端下载历史。
"""
from __future__ import annotations

import argparse
import os
import sys
from datetime import datetime

try:
    import MetaTrader5 as mt5
except ImportError:
    print("缺少 MetaTrader5 包: pip install MetaTrader5", file=sys.stderr)
    sys.exit(1)

DEFAULT_SYMBOLS = ["EURUSD", "EURGBP", "XAUUSD", "US500"]
TF_MAP = {
    "M1": mt5.TIMEFRAME_M1,
    "M5": mt5.TIMEFRAME_M5,
    "M15": mt5.TIMEFRAME_M15,
    "M30": mt5.TIMEFRAME_M30,
    "H1": mt5.TIMEFRAME_H1,
    # 一次性最多取多少根（MT5 终端本身的可用深度为准）
    # 通过 copy_rates_from_pos 逐段请求
}
MAX_BARS = 50000


def export_symbol(symbol: str, tf_name: str, out_dir: str, start: str = "", end: str = "") -> int:
    tf = TF_MAP[tf_name]
    if not mt5.symbol_select(symbol, True):
        print(f"  !! 无法选择品种 {symbol}", file=sys.stderr)
        return 0

    rates = mt5.copy_rates_from_pos(symbol, tf, 0, MAX_BARS)
    if rates is None or len(rates) == 0:
        print(f"  !! {symbol} {tf_name} 无数据 ({mt5.last_error()})", file=sys.stderr)
        return 0

    path = os.path.join(out_dir, f"{symbol}_{tf_name}.csv")
    n = 0
    with open(path, "w", encoding="utf-8", newline="") as f:
        f.write("time,open,high,low,close,tick_volume,spread\n")
        for r in rates:
            t = datetime.fromtimestamp(r["time"])
            if start:
                if t < datetime.fromisoformat(start):
                    continue
            if end:
                if t > datetime.fromisoformat(end):
                    continue
            f.write("%s,%.5f,%.5f,%.5f,%.5f,%d,%d\n" % (
                t.strftime("%Y.%m.%d %H:%M:%S"),
                r["open"], r["high"], r["low"], r["close"],
                r["tick_volume"], r["spread"],
            ))
            n += 1
    print(f"  {symbol:8s} {tf_name:3s} -> {n:6d} 根  {path}")
    return n


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--terminal", default=r"C:\Program Files\MetaTrader 5\terminal64.exe")
    ap.add_argument("--symbols", default=",".join(DEFAULT_SYMBOLS))
    ap.add_argument("--tf", default="M5,M1")
    ap.add_argument("--out", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "data"))
    ap.add_argument("--start", default="", help="起始日期 YYYY-MM-DD（可选）")
    ap.add_argument("--end", default="", help="结束日期 YYYY-MM-DD（可选）")
    args = ap.parse_args()

    out_dir = os.path.abspath(args.out)
    os.makedirs(out_dir, exist_ok=True)

    if not mt5.initialize(args.terminal):
        print("MT5 初始化失败:", mt5.last_error(), file=sys.stderr)
        return 1
    try:
        ai = mt5.account_info()
        if ai:
            print(f"终端账户: {ai.login} @ {ai.server}  货币 {ai.currency}  余额 {ai.balance}")
        else:
            print("终端未登录账户（仍可导出离线历史）")

        symbols = [s.strip() for s in args.symbols.split(",") if s.strip()]
        tfs = [t.strip().upper() for t in args.tf.split(",") if t.strip()]
        total = 0
        for s in symbols:
            si = mt5.symbol_info(s)
            if si is not None:
                print(f"品种 {s}: digits={si.digits} point={si.point} contract={si.trade_contract_size} spread={si.spread}")
            for tf in tfs:
                total += export_symbol(s, tf, out_dir, args.start, args.end)
        print(f"完成，共 {total} 根K线 -> {out_dir}")
    finally:
        mt5.shutdown()
    return 0


if __name__ == "__main__":
    sys.exit(main())
