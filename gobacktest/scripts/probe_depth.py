#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""探测各品种各周期的最大可用历史深度（按起始日期请求，可触发终端下载更早历史）。"""
import sys
import time
import MetaTrader5 as mt5
import pandas as pd
from datetime import datetime

TFS = [
    (mt5.TIMEFRAME_M15, "M15"),
    (mt5.TIMEFRAME_M30, "M30"),
    (mt5.TIMEFRAME_H1, "H1"),
    (mt5.TIMEFRAME_H4, "H4"),
    (mt5.TIMEFRAME_D1, "D1"),
]
SYMBOLS = ["EURUSD", "EURGBP", "XAUUSD", "US500"]
STARTS = [datetime(2005, 1, 1), datetime(2015, 1, 1), datetime(2020, 1, 1)]


def main():
    if not mt5.initialize(r"C:\Program Files\MetaTrader 5\terminal64.exe"):
        print("init failed", mt5.last_error())
        return 1
    for name in SYMBOLS:
        mt5.symbol_select(name, True)
    print(f"{'品种':8s} {'周期':4s} {'根数':>8s}  {'最早':>12s}  {'最新':>12s}", flush=True)
    for name in SYMBOLS:
        for tf, tfn in TFS:
            best = None
            # 先用 from_pos 拿最近 50000 根
            r = mt5.copy_rates_from_pos(name, tf, 0, 50000)
            if r is not None and len(r):
                best = r
            # 再尝试更早的起始日期，取更多
            for st in STARTS:
                r2 = mt5.copy_rates_from(name, tf, st, 500000)
                if r2 is not None and len(r2) > (0 if best is None else len(best)):
                    best = r2
                time.sleep(0.05)
            if best is None or len(best) == 0:
                print(f"{name:8s} {tfn:4s} {'空':>8s}", flush=True)
                continue
            d = pd.DataFrame(best)
            t0 = pd.to_datetime(d.time.min(), unit="s")
            t1 = pd.to_datetime(d.time.max(), unit="s")
            print(f"{name:8s} {tfn:4s} {len(d):8d}  {t0:%Y-%m-%d}  {t1:%Y-%m-%d}", flush=True)
    mt5.shutdown()
    return 0


if __name__ == "__main__":
    sys.exit(main())
