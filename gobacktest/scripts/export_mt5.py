#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""
从本机 MetaTrader 5 终端导出回测所需的 CSV 数据（尽量拉满可用历史）。

用法:
    python export_mt5.py                          # 默认品种，导出所有周期
    python export_mt5.py --tf M30,H1,D1           # 指定周期
    python export_mt5.py --symbols US500,XAUUSD,EURGBP
    python export_mt5.py --symbols all --tf all
    python export_mt5.py --earliest 2005-01-01    # 更早的起始日期（触发终端补历史）

输出: {out}/{SYMBOL}_{TF}.csv   表头 time,open,high,low,close,tick_volume,spread

取数策略（MT5 Python 接口的坑）:
  1) copy_rates_range(sym, TF, ...) 对 M1 等周期常返回 Invalid params，不用它
  2) 优先 copy_rates_from(sym, TF, earliest, 大count) —— 尽量往前拉
  3) 再用 copy_rates_from_pos(sym, TF, 0, 50000) 取最近一段做补充
  4) 两者合并去重，按时间排序
  实际深度取决于终端本地已下载的历史；若某周期偏短，先在 MT5 打开该品种该周期图表。
"""
from __future__ import annotations

import argparse
import os
import sys
import time
from datetime import datetime

try:
    import MetaTrader5 as mt5
except ImportError:
    print("缺少 MetaTrader5 包: pip install MetaTrader5", file=sys.stderr)
    sys.exit(1)

DEFAULT_SYMBOLS = ["EURUSD", "EURGBP", "XAUUSD", "US500"]
ALL_TF = ["M1", "M5", "M15", "M30", "H1", "H4", "D1"]
TF_MAP = {
    "M1": mt5.TIMEFRAME_M1,
    "M5": mt5.TIMEFRAME_M5,
    "M15": mt5.TIMEFRAME_M15,
    "M30": mt5.TIMEFRAME_M30,
    "H1": mt5.TIMEFRAME_H1,
    "H4": mt5.TIMEFRAME_H4,
    "D1": mt5.TIMEFRAME_D1,
}
POS_COUNT = 50000       # from_pos 的安全上限（更大容易让终端阻塞）
FROM_COUNT = 1000000    # from(earliest) 的请求上限


def fetch_rates(symbol: str, tf: int, earliest: datetime, deep: bool = False):
    """取历史K线。

    默认只走 copy_rates_from_pos(0, POS_COUNT) —— 实测每个周期稳定返回约 5 万根，
    M30≈4年 / H1≈8年 / D1≈数十年，足够回测且不会让终端卡住。

    --deep 会额外尝试 copy_rates_from(earliest, 大count) 触发终端补更早的历史，
    但实测该调用可能让终端阻塞很久（尤其 D1/H4），仅在你确实需要更深历史时使用。
    """
    frames = {}
    if deep:
        r = mt5.copy_rates_from(symbol, tf, earliest, FROM_COUNT)
        if r is not None and len(r):
            for row in r:
                frames[int(row["time"])] = row
    r2 = mt5.copy_rates_from_pos(symbol, tf, 0, POS_COUNT)
    if r2 is not None and len(r2):
        for row in r2:
            frames[int(row["time"])] = row
    return [frames[k] for k in sorted(frames)]


def export_symbol(symbol: str, tf_name: str, out_dir: str, earliest: datetime,
                  start: str = "", end: str = "", deep: bool = False) -> int:
    tf = TF_MAP[tf_name]
    if not mt5.symbol_select(symbol, True):
        print(f"  !! 无法选择品种 {symbol}", file=sys.stderr)
        return 0
    rates = fetch_rates(symbol, tf, earliest, deep)
    if not rates:
        print(f"  !! {symbol} {tf_name} 无数据 ({mt5.last_error()})", file=sys.stderr)
        return 0

    path = os.path.join(out_dir, f"{symbol}_{tf_name}.csv")
    n = 0
    first = last = None
    with open(path, "w", encoding="utf-8", newline="") as f:
        f.write("time,open,high,low,close,tick_volume,spread\n")
        for r in rates:
            t = datetime.fromtimestamp(r["time"])
            if start and t < datetime.fromisoformat(start):
                continue
            if end and t > datetime.fromisoformat(end):
                continue
            f.write("%s,%.5f,%.5f,%.5f,%.5f,%d,%d\n" % (
                t.strftime("%Y.%m.%d %H:%M:%S"),
                r["open"], r["high"], r["low"], r["close"],
                r["tick_volume"], r["spread"],
            ))
            if first is None:
                first = t
            last = t
            n += 1
    print(f"  {symbol:8s} {tf_name:4s} -> {n:7d} 根  {first:%Y-%m-%d} ~ {last:%Y-%m-%d}   {path}")
    return n


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--terminal", default=r"C:\Program Files\MetaTrader 5\terminal64.exe")
    ap.add_argument("--symbols", default=",".join(DEFAULT_SYMBOLS), help="逗号分隔，或 all")
    ap.add_argument("--tf", default=",".join(ALL_TF), help="逗号分隔，或 all")
    ap.add_argument("--out", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "data"))
    ap.add_argument("--earliest", default="2005-01-01", help="历史起始日期 YYYY-MM-DD（仅 --deep 时使用）")
    ap.add_argument("--deep", action="store_true",
                    help="额外尝试向更早日期补历史（可能让终端阻塞很久，慎用）")
    ap.add_argument("--start", default="", help="只保留该日期之后的数据")
    ap.add_argument("--end", default="", help="只保留该日期之前的数据")
    args = ap.parse_args()

    out_dir = os.path.abspath(args.out)
    os.makedirs(out_dir, exist_ok=True)

    if not mt5.initialize(args.terminal):
        print("MT5 初始化失败:", mt5.last_error(), file=sys.stderr)
        return 1
    try:
        ai = mt5.account_info()
        if ai:
            print(f"终端账户: {ai.login} @ {ai.server}  货币 {ai.currency}")
        else:
            print("终端未登录账户（仍可导出离线历史）")

        symbols = DEFAULT_SYMBOLS if args.symbols.strip().lower() == "all" else \
            [s.strip().upper() for s in args.symbols.split(",") if s.strip()]
        tfs = ALL_TF if args.tf.strip().lower() == "all" else \
            [t.strip().upper() for t in args.tf.split(",") if t.strip()]
        for t in tfs:
            if t not in TF_MAP:
                print(f"!! 不支持的周期 {t}", file=sys.stderr)
                return 1

        earliest = datetime.fromisoformat(args.earliest)
        total = 0
        for s in symbols:
            si = mt5.symbol_info(s)
            if si is not None:
                print(f"品种 {s}: digits={si.digits} point={si.point} "
                      f"contract={si.trade_contract_size} spread={si.spread}")
            for tf in tfs:
                total += export_symbol(s, tf, out_dir, earliest, args.start, args.end, args.deep)
                sys.stdout.flush()
                time.sleep(0.6)  # 给终端留出喘息时间，避免 MCP 通道堵塞
        print(f"完成，共 {total} 根K线 -> {out_dir}")
    finally:
        mt5.shutdown()
    return 0


if __name__ == "__main__":
    sys.exit(main())
