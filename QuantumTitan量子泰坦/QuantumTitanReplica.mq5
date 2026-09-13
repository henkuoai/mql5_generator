//+------------------------------------------------------------------+
//|                                        QuantumTitanReplica.mq5   |
//|  逆向复刻框架：基于对 MQL5 信号 2362973 (Quantum Titan MT5)       |
//|  108 笔实盘订单 + XAUUSD M5/H1 行情的量化取证分析                  |
//|                                                                  |
//|  推断出的核心逻辑（详见分析报告）：                                |
//|   1. 仅交易 XAUUSD，tick 级驱动的动量突破剥头皮                    |
//|   2. 入场：价格突破近30分钟极值 + 近5分钟动量≥4美元 + H1趋势同向  |
//|   3. 止损：固定 1000 点（10美元），后期改 500 点                  |
//|   4. 止盈：动态（约 0.3~0.4 倍 M5 ATR）+ 紧跟踪 + 停滞离场        |
//|   5. 仓位：风险金额 / 止损距离；风险等级随回撤状态自动升降         |
//|   6. 单仓位运行，无网格、无马丁、无加仓摊平                       |
//+------------------------------------------------------------------+
#property copyright "Reverse-engineered replica framework"
#property version   "1.00"

//============================ 枚举（须在 input 之前声明） ==========
enum ENUM_TRIGGER_MODE { TRIGGER_BOTH=0,     // 突破+动量 同时满足
                         TRIGGER_BREAKOUT=1, // 仅突破
                         TRIGGER_VELOCITY=2  // 仅动量
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
input string InpComment          = "QTR";      // 订单注释

input group "=== 入场：动量突破触发 ==="
input int    InpBreakoutBars     = 6;          // 突破回看K线数(M5, 6=30分钟)
input int    InpBreakoutBuffer   = 30;         // 突破缓冲(点, 30点=0.3美元)
input double InpVelocityUSD      = 4.0;        // 近5分钟最小动量(美元)
input int    InpVelocityBars     = 1;          // 动量回看M5根数(1=约5分钟)
input ENUM_TRIGGER_MODE InpTriggerMode = TRIGGER_BOTH; // 触发模式：突破+动量 / 仅突破 / 仅动量

input group "=== 入场：趋势/环境过滤 ==="
input bool   InpUseH1Trend       = true;       // H1趋势过滤(EMA20/EMA50)
input bool   InpUseM5Trend       = true;       // M5均线多头排列过滤
input int    InpMinADX           = 20;         // M5 ADX最低值(0=禁用)
input int    InpMaxSpreadPoints  = 350;        // 最大点差(点, 0=禁用)
input bool   InpOnePositionOnly  = true;       // 同一时间仅持一仓

input group "=== 出场 ==="
input int    InpStopLossPoints   = 1000;       // 固定止损(点, 1000=10美元)
input double InpTP_ATRmult       = 0.35;       // 动态止盈 = 该倍数 x M5 ATR(0=禁用)
input double InpTrailStartUSD    = 1.2;        // 盈利达到该值后启动跟踪(美元)
input double InpTrailDistUSD     = 0.8;        // 跟踪距离(美元)
input int    InpStallSeconds     = 25;         // 停滞离场:无新极值超时(秒, 0=禁用)
input double InpStallMinProfitUSD= 0.3;        // 停滞离场最低浮盈(美元)
input int    InpStallMaxSeconds  = 1200;       // 最长持仓(秒, 0=禁用)

input group "=== 仓位与风险等级 ==="
input ENUM_LOT_MODE InpLotMode   = LOT_RISKPCT; // 仓位模式：余额百分比 / 固定风险金额 / 固定手数
input double InpRiskPercent      = 5.0;        // 基础风险(余额百分比)
input double InpFixedRiskUSD     = 3000.0;     // 固定风险金额(USD, LOT_FIXEDUSD模式)
input double InpFixedLots        = 3.0;        // 固定手数(LOT_FIXEDLOTS模式)
input bool   InpAdaptiveRisk     = true;       // 回撤自适应风险等级
input double InpDDRecoverBoost   = 9.0;        // 回撤>=InpDDTrigger时使用的风险%(恢复模式)
input double InpDDTrigger        = 8.0;        // 触发恢复模式的回撤%(相对净值峰值)
input double InpDDDeRiskPct      = 3.0;        // 创净值新高后回落到的风险%
input double InpMaxDDHalt        = 15.0;       // 最大回撤熔断%(超过则停止开新仓)

//============================ 全局变量 ============================
CTrade         trade;
CPositionInfo  pos;
CSymbolInfo    sym;

int      hEmaH1Fast=INVALID_HANDLE, hEmaH1Slow=INVALID_HANDLE;
int      hEmaM5Fast=INVALID_HANDLE, hEmaM5Slow=INVALID_HANDLE;
int      hAtrM5=INVALID_HANDLE, hAdxM5=INVALID_HANDLE;

datetime g_lastExtremeTime=0;      // 最近一次浮盈创新高的服务器时间
double   g_equityPeak=0.0;         // 净值峰值(风险等级用)
int      g_halted=0;               // 熔断标志
string   g_riskState="base";       // base / recover / derisk

//+------------------------------------------------------------------+
int OnInit()
  {
   if(!sym.Name(_Symbol))
      return INIT_FAILED;
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
//| 工具：读取指标最近一根已收盘K线的值                              |
//+------------------------------------------------------------------+
double IndValue(int handle, int shift)
  {
   double buf[1];
   if(CopyBuffer(handle, 0, shift, 1, buf) != 1)
      return EMPTY_VALUE;
   return buf[0];
  }
//+------------------------------------------------------------------+
//| 工具：近N根已收盘M5K线的最高/最低价                              |
//+------------------------------------------------------------------+
bool RangeOfLastBars(int bars, double &hi, double &lo)
  {
   MqlRates r[];
   int n = CopyRates(_Symbol, PERIOD_M5, 1, bars, r); // 从第1根起=排除当前未收盘K线
   if(n < bars)
      return false;
   hi = -DBL_MAX; lo = DBL_MAX;
   for(int i=0; i<n; i++)
     {
      hi = MathMax(hi, r[i].high);
      lo = MathMin(lo, r[i].low);
     }
   return true;
  }
//+------------------------------------------------------------------+
//| 工具：近5分钟动量（当前价 - N根M5前的收盘价）                    |
//+------------------------------------------------------------------+
double VelocityUSD(int barsBack)
  {
   MqlRates r[];
   if(CopyRates(_Symbol, PERIOD_M5, barsBack, 1, r) != 1)
      return 0.0;
   return sym.Bid() - r[0].close;
  }
//+------------------------------------------------------------------+
//| 当前风险等级（回撤自适应）                                       |
//+------------------------------------------------------------------+
double CurrentRiskPercent()
  {
   if(!InpAdaptiveRisk)
      return InpRiskPercent;

   double eq    = AccountInfoDouble(ACCOUNT_EQUITY);
   g_equityPeak = MathMax(g_equityPeak, eq);
   double ddPct = (g_equityPeak>0) ? (g_equityPeak-eq)/g_equityPeak*100.0 : 0.0;

   // 创新高后 -> 降风险锁收益
   if(ddPct <= 0.5 && g_riskState=="recover")
      g_riskState = "derisk";
   // 深回撤 -> 恢复模式（更高风险）
   if(ddPct >= InpDDTrigger)
      g_riskState = "recover";
   else if(ddPct <= 0.5 && g_riskState!="recover")
      g_riskState = (g_riskState=="derisk") ? "derisk" : "base";

   if(g_riskState=="recover") return MathMin(InpDDRecoverBoost, 10.0);
   if(g_riskState=="derisk")  return InpDDDeRiskPct;
   return InpRiskPercent;
  }
//+------------------------------------------------------------------+
//| 手数计算：风险金额 / 止损距离                                     |
//+------------------------------------------------------------------+
double CalcLots(double slDistancePrice)
  {
   double riskMoney;
   if(InpLotMode==LOT_FIXEDLOTS)
      return NormalizeLots(InpFixedLots);

   if(InpLotMode==LOT_FIXEDUSD)
      riskMoney = InpFixedRiskUSD;
   else
     {
      double riskPct = CurrentRiskPercent();
      riskMoney = AccountInfoDouble(ACCOUNT_BALANCE) * riskPct / 100.0;
     }

   // 1手的止损金额 = 止损价格距离 x 合约大小(黄金=100oz)
   double perLotLoss = slDistancePrice * sym.ContractSize();
   if(perLotLoss <= 0)
      return 0.0;
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
//| 是否允许开新仓（风控熔断 / 单仓位 / 点差）                       |
//+------------------------------------------------------------------+
bool CanOpen()
  {
   if(g_halted)
      return false;

   if(InpOnePositionOnly && PositionsTotal()>0)
      for(int i=0; i<PositionsTotal(); i++)
         if(pos.SelectByIndex(i) && pos.Symbol()==_Symbol)
            return false;

   if(InpMaxSpreadPoints>0 && (int)SymbolInfoInteger(_Symbol, SYMBOL_SPREAD) > InpMaxSpreadPoints)
      return false;

   // 最大回撤熔断
   double eq = AccountInfoDouble(ACCOUNT_EQUITY);
   g_equityPeak = MathMax(g_equityPeak, eq);
   double ddPct = (g_equityPeak>0) ? (g_equityPeak-eq)/g_equityPeak*100.0 : 0.0;
   if(InpMaxDDHalt>0 && ddPct >= InpMaxDDHalt)
     {
      g_halted = 1;
      Print("最大回撤熔断触发, dd=", DoubleToString(ddPct,2), "%");
      return false;
     }
   return true;
  }
//+------------------------------------------------------------------+
//| 入场信号检查                                                     |
//+------------------------------------------------------------------+
int EntrySignal()
  {
   double bid = sym.Bid(), ask = sym.Ask();

   // --- 突破条件：价格越过近N根M5K线极值+缓冲 ---
   double hi, lo;
   bool hasRange = RangeOfLastBars(InpBreakoutBars, hi, lo);
   double buffer = InpBreakoutBuffer * sym.Point();
   bool brkUp = hasRange && ask > hi + buffer;
   bool brkDn = hasRange && bid < lo - buffer;

   // --- 动量条件：近5分钟价格推进 ---
   double vel = VelocityUSD(InpVelocityBars);
   bool velUp = (vel >=  InpVelocityUSD);
   bool velDn = (vel <= -InpVelocityUSD);

   bool buyTrig, sellTrig;
   switch(InpTriggerMode)
     {
      case TRIGGER_BREAKOUT: buyTrig=brkUp;  sellTrig=brkDn;  break;
      case TRIGGER_VELOCITY: buyTrig=velUp;  sellTrig=velDn;  break;
      default:               buyTrig=(brkUp&&velUp); sellTrig=(brkDn&&velDn); break;
     }
   if(!buyTrig && !sellTrig)
      return 0;

   // --- H1 趋势过滤：价格与快慢均线同侧 ---
   if(InpUseH1Trend)
     {
      double eF = IndValue(hEmaH1Fast, 1), eS = IndValue(hEmaH1Slow, 1);
      if(eF==EMPTY_VALUE || eS==EMPTY_VALUE) return 0;
      if(buyTrig  && !(bid>eF && eF>eS)) buyTrig=false;
      if(sellTrig && !(bid<eF && eF<eS)) sellTrig=false;
     }

   // --- M5 均线排列过滤 ---
   if(InpUseM5Trend)
     {
      double eF = IndValue(hEmaM5Fast, 1), eS = IndValue(hEmaM5Slow, 1);
      if(eF==EMPTY_VALUE || eS==EMPTY_VALUE) return 0;
      if(buyTrig  && !(bid>eF && eF>eS)) buyTrig=false;
      if(sellTrig && !(bid<eF && eF<eS)) sellTrig=false;
     }

   // --- ADX 强度过滤 ---
   if(InpMinADX>0)
     {
      double adx = IndValue(hAdxM5, 0); // ADX主线buffer 0
      if(adx==EMPTY_VALUE || adx < InpMinADX)
         return 0;
     }

   if(buyTrig)  return  1;
   if(sellTrig) return -1;
   return 0;
  }
//+------------------------------------------------------------------+
//| 开仓                                                             |
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
      PrintFormat("开仓 %s %.2f手 SL=%.2f TP=%.2f 风险档=%s",
                  dir>0?"BUY":"SELL", lots, sl, tp, g_riskState);
  }
//+------------------------------------------------------------------+
//| 持仓管理：跟踪止损 + 停滞离场 + 超时离场                         |
//+------------------------------------------------------------------+
void ManagePosition()
  {
   if(!pos.Select(_Symbol))
      return;
   if(pos.Magic()!=20260913)   // 只管理本EA的仓位
      return;

   long   type      = pos.PositionType();
   double open      = pos.PriceOpen();
   double cur       = (type==POSITION_TYPE_BUY) ? sym.Bid() : sym.Ask();
   double profitUSD = (type==POSITION_TYPE_BUY) ? (cur-open) : (open-cur);
   double point     = sym.Point();
   datetime now     = TimeCurrent();

   // --- 停滞检测：浮盈创阶段新高则刷新时间戳 ---
   static double lastBest = 0.0;
   static datetime lastOpen = 0;
   if(pos.Time()!=lastOpen)  // 新仓
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

   // --- 停滞离场：曾有浮盈但价格停滞 ---
   if(InpStallSeconds>0 && profitUSD >= InpStallMinProfitUSD &&
      (now - g_lastExtremeTime) >= InpStallSeconds)
     {
      trade.PositionClose(_Symbol);
      Print("停滞离场 profit=", DoubleToString(profitUSD,2));
      return;
     }

   // --- 超时离场 ---
   if(InpStallMaxSeconds>0 && (now - pos.Time()) >= InpStallMaxSeconds)
     {
      trade.PositionClose(_Symbol);
      Print("超时离场");
      return;
     }

   // --- 跟踪止损：浮盈达到启动线后，SL跟到 利润-跟踪距离 ---
   if(InpTrailStartUSD>0 && profitUSD >= InpTrailStartUSD)
     {
      double trailLevel = (type==POSITION_TYPE_BUY)
                          ? cur - InpTrailDistUSD
                          : cur + InpTrailDistUSD;
      double curSL = pos.StopLoss();
      bool better = (type==POSITION_TYPE_BUY) ? (trailLevel > curSL) : (trailLevel < curSL || curSL==0);
      if(better)
         trade.PositionModify(_Symbol,
                              NormalizeDouble(trailLevel, sym.Digits()),
                              pos.TakeProfit());
     }
  }
//+------------------------------------------------------------------+
void OnTick()
  {
   if(!sym.RefreshRates())
      return;

   ManagePosition();

   if(!CanOpen())
      return;

   int sig = EntrySignal();
   if(sig!=0)
      OpenPosition(sig);
  }
//+------------------------------------------------------------------+
