//+------------------------------------------------------------------+
//|                                       QuantumTitanReplica_v2.mq5  |
//|  逆向复刻 v2：基于信号 2362973 (Quantum Titan MT5) 108 笔实盘     |
//|  + 本机 XAUUSD M5/H1 行情取证                                     |
//|                                                                  |
//|  v1→v2 主要修复：                                                  |
//|   1) 止损单位：默认 10000 点 = $10（3位黄金），不再错为 $1         |
//|   2) 触发条件加 "宽幅方向K线" 形态确认（最高选择性）                |
//|   3) 每根 M5 K 线最多入场一次（防同一根 K 线内多次触发）            |
//|   4) 平仓后强制冷却（默认 60 秒）                                  |
//|   5) 提高动量阈值与突破缓冲默认值                                   |
//+------------------------------------------------------------------+
#property copyright "Reverse-engineered replica v2"
#property version   "2.00"

//============================ 枚举 ===========================
enum ENUM_TRIGGER_MODE { TRIGGER_BOTH=0,     // 突破 + 动量 必须同时满足
                         TRIGGER_BREAKOUT=1, // 仅突破 + K线形态
                         TRIGGER_VELOCITY=2  // 仅动量 + K线形态
                       };
enum ENUM_LOT_MODE    { LOT_RISKPCT=0,       // 余额百分比（风险档）
                        LOT_FIXEDUSD=1,      // 固定风险金额(USD)
                        LOT_FIXEDLOTS=2      // 固定手数
                      };

#include <Trade\Trade.mqh>
#include <Trade\PositionInfo.mqh>
#include <Trade\SymbolInfo.mqh>

//============================ 输入参数 ============================
input group "=== 品种与基础 ==="
input string InpComment          = "QTR2";     // 订单注释

input group "=== 入场：动量突破触发 ==="
input int    InpBreakoutBars     = 12;         // 突破回看K线数(M5, 12=1小时)
input int    InpBreakoutBuffer   = 50;         // 突破缓冲(点, 50点=0.5美元)
input double InpVelocityUSD      = 6.0;        // 近N分钟最小动量(美元)
input int    InpVelocityBars     = 1;          // 动量回看M5根数(1=约5分钟)
input ENUM_TRIGGER_MODE InpTriggerMode = TRIGGER_BOTH;

input group "=== 入场：宽幅方向K线（选择性核心） ==="
input bool   InpRequireWideBar   = true;       // 要求"宽幅方向K线"形态
input double InpMinBarRangeATR   = 1.2;        // K线范围 >= 此倍数 × M5 ATR
input int    InpBarClosePct      = 30;         // 收盘位置分位（顶部X%=多信号，底部X%=空信号）
input bool   InpRequireBreakoutClose = true;   // 要求K线收盘于前N根极值之外

input group "=== 入场：趋势/环境过滤 ==="
input bool   InpUseH1Trend       = true;       // H1趋势过滤(EMA20/EMA50)
input bool   InpUseM5Trend       = true;       // M5均线多头排列过滤
input int    InpMinADX           = 22;         // M5 ADX最低值(0=禁用)
input int    InpMaxSpreadPoints  = 350;        // 最大点差(点, 0=禁用)
input bool   InpOnePositionOnly  = true;       // 同一时间仅持一仓

input group "=== 冷却（防过频） ==="
input int    InpPerBarCooldown   = 1;          // 每个M5根K线最多入场次数(0=不限制)
input int    InpTradeCooldownSec = 60;         // 平仓后冷却(秒, 0=不限制)

input group "=== 出场 ==="
input int    InpStopLossPoints   = 10000;      // 固定止损(点, 3位黄金=10美元)
input double InpTP_ATRmult       = 0.40;       // 动态止盈 = 该倍数 x M5 ATR(0=禁用)
input double InpTrailStartUSD    = 1.2;        // 盈利达到该值后启动跟踪(美元)
input double InpTrailDistUSD     = 0.8;        // 跟踪距离(美元)
input int    InpStallSeconds     = 30;         // 停滞离场:无新极值超时(秒, 0=禁用)
input double InpStallMinProfitUSD= 0.3;        // 停滞离场最低浮盈(美元)
input int    InpStallMaxSeconds  = 1800;       // 最长持仓(秒, 0=禁用)

input group "=== 仓位与风险等级 ==="
input ENUM_LOT_MODE InpLotMode   = LOT_RISKPCT;
input double InpRiskPercent      = 5.0;        // 基础风险(余额百分比)
input double InpFixedRiskUSD     = 3000.0;     // 固定风险金额(USD)
input double InpFixedLots        = 3.0;        // 固定手数
input bool   InpAdaptiveRisk     = true;       // 回撤自适应风险等级
input double InpDDRecoverBoost   = 9.0;        // 恢复模式风险%
input double InpDDTrigger        = 8.0;        // 触发恢复的回撤%
input double InpDDDeRiskPct      = 3.0;        // 新高后回落风险%
input double InpMaxDDHalt        = 15.0;       // 最大回撤熔断%

//============================ 全局变量 ============================
CTrade         trade;
CPositionInfo  pos;
CSymbolInfo    sym;

int      hEmaH1Fast=INVALID_HANDLE, hEmaH1Slow=INVALID_HANDLE;
int      hEmaM5Fast=INVALID_HANDLE, hEmaM5Slow=INVALID_HANDLE;
int      hAtrM5=INVALID_HANDLE, hAdxM5=INVALID_HANDLE;

datetime g_lastExtremeTime=0;      // 浮盈最近一次创新高的时间戳
double   g_equityPeak=0.0;
int      g_halted=0;
string   g_riskState="base";

// 冷却状态
datetime g_lastBarTime=0;          // 最近一次入场所在的 M5 K 线时间
int      g_signalsThisBar=0;       // 当前 M5 K 线已用入场次数
datetime g_lastCloseTime=0;        // 上次平仓时间（用于冷却）

//+------------------------------------------------------------------+
int OnInit()
  {
   if(!sym.Name(_Symbol)) return INIT_FAILED;
   sym.RefreshRates();

   trade.SetExpertMagicNumber(20260913);
   trade.SetDeviationInPoints(50);
   trade.SetTypeFillingBySymbol(_Symbol);

   hEmaH1Fast = iMA(_Symbol, PERIOD_H1, 20, 0, MODE_EMA, PRICE_CLOSE);
   hEmaH1Slow = iMA(_Symbol, PERIOD_H1, 50, 0, MODE_EMA, PRICE_CLOSE);
   hEmaM5Fast = iMA(_Symbol, PERIOD_M5, 20, 0, MODE_EMA, PRICE_CLOSE);
   hEmaM5Slow = iMA(_Symbol, PERIOD_M5, 50, 0, MODE_EMA, PRICE_CLOSE);
   hAtrM5     = iATR(_Symbol, PERIOD_M5, 14);
   hAdxM5     = iADX(_Symbol, PERIOD_M5, 14);
   if(hEmaH1Fast==INVALID_HANDLE || hEmaH1Slow==INVALID_HANDLE ||
      hEmaM5Fast==INVALID_HANDLE || hEmaM5Slow==INVALID_HANDLE ||
      hAtrM5==INVALID_HANDLE || hAdxM5==INVALID_HANDLE)
      return INIT_FAILED;

   g_equityPeak = AccountInfoDouble(ACCOUNT_EQUITY);
   return INIT_SUCCEEDED;
  }
//+------------------------------------------------------------------+
void OnDeinit(const int reason)
  {
   if(hEmaH1Fast!=INVALID_HANDLE) IndicatorRelease(hEmaH1Fast);
   if(hEmaH1Slow!=INVALID_HANDLE) IndicatorRelease(hEmaH1Slow);
   if(hEmaM5Fast!=INVALID_HANDLE) IndicatorRelease(hEmaM5Fast);
   if(hEmaM5Slow!=INVALID_HANDLE) IndicatorRelease(hEmaM5Slow);
   if(hAtrM5!=INVALID_HANDLE)     IndicatorRelease(hAtrM5);
   if(hAdxM5!=INVALID_HANDLE)     IndicatorRelease(hAdxM5);
  }
//+------------------------------------------------------------------+
double IndValue(int handle, int shift)
  {
   double buf[1];
   if(CopyBuffer(handle, 0, shift, 1, buf) != 1) return EMPTY_VALUE;
   return buf[0];
  }
//+------------------------------------------------------------------+
// 工具：近 N 根已收盘 M5 K 线的最高/最低 + 当前未收盘K线的状态
// 返回 true 且给出 priorHigh/priorLow（不含当前未收盘K线）
// 同时给出当前未收盘K线的 high/so far、low/so far、open
//+------------------------------------------------------------------+
bool MarketContext(int priorBars,
                   double &priorHi, double &priorLo,
                   double &curOpen, double &curHi, double &curLo)
  {
   MqlRates r[];
   int n = CopyRates(_Symbol, PERIOD_M5, 0, priorBars+1, r); // 0 = 含当前K线
   if(n < priorBars+1) return false;
   priorHi = -DBL_MAX; priorLo = DBL_MAX;
   // 索引 0=当前K线, 1..priorBars=已收盘K线
   curOpen = r[0].open;
   curHi   = r[0].high;
   curLo   = r[0].low;
   for(int i = 1; i <= priorBars; i++)
     {
      priorHi = MathMax(priorHi, r[i].high);
      priorLo = MathMin(priorLo, r[i].low);
     }
   return true;
  }
//+------------------------------------------------------------------+
double VelocityUSD(int barsBack)
  {
   MqlRates r[];
   if(CopyRates(_Symbol, PERIOD_M5, barsBack, 1, r) != 1) return 0.0;
   return sym.Bid() - r[0].close;
  }
//+------------------------------------------------------------------+
double CurrentRiskPercent()
  {
   if(!InpAdaptiveRisk) return InpRiskPercent;
   double eq = AccountInfoDouble(ACCOUNT_EQUITY);
   g_equityPeak = MathMax(g_equityPeak, eq);
   double ddPct = (g_equityPeak>0) ? (g_equityPeak-eq)/g_equityPeak*100.0 : 0.0;

   if(ddPct <= 0.5 && g_riskState=="recover") g_riskState = "derisk";
   if(ddPct >= InpDDTrigger) g_riskState = "recover";
   else if(ddPct <= 0.5 && g_riskState!="recover")
      g_riskState = (g_riskState=="derisk") ? "derisk" : "base";

   if(g_riskState=="recover") return MathMin(InpDDRecoverBoost, 10.0);
   if(g_riskState=="derisk")  return InpDDDeRiskPct;
   return InpRiskPercent;
  }
//+------------------------------------------------------------------+
double CalcLots(double slDistancePrice)
  {
   double riskMoney;
   if(InpLotMode==LOT_FIXEDLOTS) return NormalizeLots(InpFixedLots);
   if(InpLotMode==LOT_FIXEDUSD)  riskMoney = InpFixedRiskUSD;
   else
     {
      double riskPct = CurrentRiskPercent();
      riskMoney = AccountInfoDouble(ACCOUNT_BALANCE) * riskPct / 100.0;
     }
   double perLotLoss = slDistancePrice * sym.ContractSize();
   if(perLotLoss <= 0) return 0.0;
   return NormalizeLots(riskMoney / perLotLoss);
  }
//+------------------------------------------------------------------+
double NormalizeLots(double lots)
  {
   double minL = sym.LotsMin(), maxL = sym.LotsMax(), step = sym.LotsStep();
   lots = MathFloor(lots/step)*step;
   return MathMin(MathMax(lots, minL), maxL);
  }
//+------------------------------------------------------------------+
bool CanOpen()
  {
   if(g_halted) return false;

   // 单仓位
   if(InpOnePositionOnly && PositionsTotal()>0)
      for(int i=0; i<PositionsTotal(); i++)
         if(pos.SelectByIndex(i) && pos.Symbol()==_Symbol && pos.Magic()==20260913)
            return false;

   // 点差
   if(InpMaxSpreadPoints>0 && (int)SymbolInfoInteger(_Symbol, SYMBOL_SPREAD) > InpMaxSpreadPoints)
      return false;

   // 平仓后冷却
   if(InpTradeCooldownSec>0 && g_lastCloseTime>0 &&
      (TimeCurrent() - g_lastCloseTime) < InpTradeCooldownSec)
      return false;

   // 每根 M5 K 线冷却
   if(InpPerBarCooldown>0)
     {
      datetime barT = iTime(_Symbol, PERIOD_M5, 0);
      if(barT != g_lastBarTime)
        {
         g_lastBarTime = barT;
         g_signalsThisBar = 0;
        }
      if(g_signalsThisBar >= InpPerBarCooldown)
         return false;
     }

   // 最大回撤熔断
   double eq = AccountInfoDouble(ACCOUNT_EQUITY);
   g_equityPeak = MathMax(g_equityPeak, eq);
   double ddPct = (g_equityPeak>0) ? (g_equityPeak-eq)/g_equityPeak*100.0 : 0.0;
   if(InpMaxDDHalt>0 && ddPct >= InpMaxDDHalt)
     {
      g_halted = 1;
      Print("最大回撤熔断 dd=", DoubleToString(ddPct,2), "%");
      return false;
     }
   return true;
  }
//+------------------------------------------------------------------+
// 入场信号：组合条件
//+------------------------------------------------------------------+
int EntrySignal()
  {
   double bid = sym.Bid(), ask = sym.Ask();

   // ---- 价格上下文：前 N 根 M5 K 线极值 + 当前未收盘 K 线状态 ----
   double priorHi=0, priorLo=0, curOpen=0, curHi=0, curLo=0;
   if(!MarketContext(InpBreakoutBars, priorHi, priorLo, curOpen, curHi, curLo))
      return 0;
   double buffer = InpBreakoutBuffer * sym.Point();

   // 当前未收盘 K 线的实时范围
   double curRange = curHi - curLo;
   double curPos = (curRange > 0) ? (ask - curLo) / curRange : 0.5;

   // 突破条件：当前 K 线 high 已越过前 N 根极值 + 缓冲
   bool brkUp = (curHi > priorHi + buffer);
   bool brkDn = (curLo < priorLo - buffer);

   // ---- 动量条件 ----
   double vel = VelocityUSD(InpVelocityBars);
   bool velUp = (vel >=  InpVelocityUSD);
   bool velDn = (vel <= -InpVelocityUSD);

   // ---- "宽幅方向K线"形态：选择性核心 ----
   // 当前未收盘 K 线：范围 >= ATR(M5) × 倍数 且 价格在分位一侧
   double atr = IndValue(hAtrM5, 1);
   bool wideBar = false, dirBar = false;
   if(atr != EMPTY_VALUE && atr > 0)
     {
      wideBar = (curRange >= atr * InpMinBarRangeATR);
      double topPct   = InpBarClosePct / 100.0;
      double botPct   = topPct;
      dirBar = wideBar && (curPos >= 1.0 - topPct || curPos <= botPct);
     }

   // 是否要求K线形态
   bool needBar = InpRequireWideBar || InpRequireBreakoutClose;

   // ---- 组合触发 ----
   bool buyTrig=false, sellTrig=false;
   switch(InpTriggerMode)
     {
      case TRIGGER_BREAKOUT:
         buyTrig  = brkUp;
         sellTrig = brkDn;
         break;
      case TRIGGER_VELOCITY:
         buyTrig  = velUp;
         sellTrig = velDn;
         break;
      default: // BOTH
         buyTrig  = brkUp && velUp;
         sellTrig = brkDn && velDn;
         break;
     }
   if(needBar)
     {
      buyTrig  = buyTrig  && dirBar && curPos >= 1.0 - InpBarClosePct/100.0;
      sellTrig = sellTrig && dirBar && curPos <=    InpBarClosePct/100.0;
     }
   if(!buyTrig && !sellTrig)
      return 0;

   // ---- H1 趋势过滤 ----
   if(InpUseH1Trend)
     {
      double eF = IndValue(hEmaH1Fast, 1), eS = IndValue(hEmaH1Slow, 1);
      if(eF==EMPTY_VALUE || eS==EMPTY_VALUE) return 0;
      if(buyTrig  && !(bid>eF && eF>eS)) buyTrig=false;
      if(sellTrig && !(bid<eF && eF<eS)) sellTrig=false;
     }
   // ---- M5 均线排列 ----
   if(InpUseM5Trend)
     {
      double eF = IndValue(hEmaM5Fast, 1), eS = IndValue(hEmaM5Slow, 1);
      if(eF==EMPTY_VALUE || eS==EMPTY_VALUE) return 0;
      if(buyTrig  && !(bid>eF && eF>eS)) buyTrig=false;
      if(sellTrig && !(bid<eF && eF<eS)) sellTrig=false;
     }
   // ---- ADX ----
   if(InpMinADX>0)
     {
      double adx = IndValue(hAdxM5, 0);
      if(adx==EMPTY_VALUE || adx < InpMinADX) return 0;
     }

   if(buyTrig)  return  1;
   if(sellTrig) return -1;
   return 0;
  }
//+------------------------------------------------------------------+
void OpenPosition(int dir)
  {
   double slDist = InpStopLossPoints * sym.Point();
   double atr    = IndValue(hAtrM5, 1);
   double tpDist = (InpTP_ATRmult>0 && atr!=EMPTY_VALUE) ? atr*InpTP_ATRmult : 0.0;

   double lots = CalcLots(slDist);
   if(lots <= 0) return;

   double sl = 0.0, tp = 0.0;
   if(dir>0)
     {
      double ask = sym.Ask();
      sl = NormalizeDouble(ask - slDist, sym.Digits());
      tp = (tpDist>0) ? NormalizeDouble(ask + tpDist, sym.Digits()) : 0.0;
      trade.Buy(lots, _Symbol, ask, sl, tp, InpComment);
     }
   else
     {
      double bid = sym.Bid();
      sl = NormalizeDouble(bid + slDist, sym.Digits());
      tp = (tpDist>0) ? NormalizeDouble(bid - tpDist, sym.Digits()) : 0.0;
      trade.Sell(lots, _Symbol, bid, sl, tp, InpComment);
     }

   g_lastExtremeTime = TimeCurrent();
   if(trade.ResultRetcode()==TRADE_RETCODE_DONE)
     {
      g_signalsThisBar++;
      PrintFormat("开仓 %s %.2f手 SL=%.2f TP=%.2f 风险档=%s 状态=%s",
                  dir>0?"BUY":"SELL", lots, sl, tp, g_riskState,
                  g_halted?"HALTED":"OK");
     }
  }
//+------------------------------------------------------------------+
void ManagePosition()
  {
   if(!pos.Select(_Symbol)) return;
   if(pos.Magic()!=20260913) return;

   long   type  = pos.PositionType();
   double open  = pos.PriceOpen();
   double cur   = (type==POSITION_TYPE_BUY) ? sym.Bid() : sym.Ask();
   double profitUSD = (type==POSITION_TYPE_BUY) ? (cur-open) : (open-cur);
   datetime now = TimeCurrent();

   //  检测本EA的平仓事件以更新冷却（用 OnTradeTransaction 更稳，
   //  但简单起见，每次看到无仓且 g_lastCloseTime=0 时不处理）
   static double lastBest = 0.0;
   static datetime lastOpen = 0;
   if(pos.Time()!=lastOpen)
     {
      lastBest = 0.0;
      lastOpen = pos.Time();
      g_lastExtremeTime = now;
     }
   if(profitUSD > lastBest + 0.05)
     {
      lastBest = profitUSD;
      g_lastExtremeTime = now;
     }

   // 停滞离场
   if(InpStallSeconds>0 && profitUSD >= InpStallMinProfitUSD &&
      (now - g_lastExtremeTime) >= InpStallSeconds)
     {
      trade.PositionClose(_Symbol);
      g_lastCloseTime = now;
      return;
     }
   // 超时离场
   if(InpStallMaxSeconds>0 && (now - pos.Time()) >= InpStallMaxSeconds)
     {
      trade.PositionClose(_Symbol);
      g_lastCloseTime = now;
      return;
     }
   // 跟踪止损
   if(InpTrailStartUSD>0 && profitUSD >= InpTrailStartUSD)
     {
      double trailLevel = (type==POSITION_TYPE_BUY) ? cur - InpTrailDistUSD
                                                    : cur + InpTrailDistUSD;
      double curSL = pos.StopLoss();
      bool better = (type==POSITION_TYPE_BUY) ? (trailLevel > curSL)
                                              : (trailLevel < curSL || curSL==0);
      if(better)
         trade.PositionModify(_Symbol,
                              NormalizeDouble(trailLevel, sym.Digits()),
                              pos.TakeProfit());
     }
  }
//+------------------------------------------------------------------+
void OnTick()
  {
   if(!sym.RefreshRates()) return;
   ManagePosition();
   if(!CanOpen()) return;
   int sig = EntrySignal();
   if(sig!=0) OpenPosition(sig);
  }
//+------------------------------------------------------------------+
//| 处理交易事件：本EA的平仓到达时刷新冷却时间                       |
//+------------------------------------------------------------------+
void OnTrade()
  {
   // 只在没有持仓且刚刚出现一笔 OUT 交易时设置冷却
   for(int i=PositionsTotal()-1; i>=0; i--)
      if(pos.SelectByIndex(i) && pos.Symbol()==_Symbol && pos.Magic()==20260913)
         return; // 仍有本EA仓位，未平

   datetime from = TimeCurrent() - 60;
   datetime to   = TimeCurrent();
   if(!HistorySelect(from, to)) return;
   int total = HistoryDealsTotal();
   for(int i = total - 1; i >= 0; i--)
     {
      ulong ticket = HistoryDealGetTicket(i);
      if(ticket == 0) continue;
      if(HistoryDealGetInteger(ticket, DEAL_MAGIC) != 20260913) continue;
      long entry = HistoryDealGetInteger(ticket, DEAL_ENTRY);
      if(entry != DEAL_ENTRY_OUT) continue;
      g_lastCloseTime = TimeCurrent();
      return;
     }
  }