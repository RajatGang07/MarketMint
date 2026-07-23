package intraday

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/gangrajat/groww-paper-trading/backend/internal/instruments"
	"github.com/gangrajat/groww-paper-trading/backend/internal/marketdata"
)

// Pick is one ranked intraday idea, sized and ready to execute.
type Pick struct {
	Symbol string `json:"symbol"`
	Name   string `json:"name"`
	Rank   int    `json:"rank"`

	// Status of today's signal:
	//   ACTIVE     — triggered and still running (trade it / manage it)
	//   TARGET     — already hit the 2R target today
	//   TRAIL      — trailing stop took it out (usually in profit)
	//   STOP       — initial stop hit
	//   SQUARE_OFF — exited at the 15:15 square-off
	Status string `json:"status"`

	EntryTime time.Time `json:"entry_time"`
	Entry     float64   `json:"entry"`
	Stop      float64   `json:"stop"` // current level: initial or trailed
	Target    float64   `json:"target"`
	TrailBy   float64   `json:"trail_by"`
	RiskShare float64   `json:"risk_per_share"`
	ORHigh    float64   `json:"or_high"`
	ORLow     float64   `json:"or_low"`
	VWAP      float64   `json:"vwap"`
	RVOL      float64   `json:"rvol"`
	LastPrice float64   `json:"last_price"`

	// Exit report when the trade already finished today.
	ExitTime *time.Time `json:"exit_time,omitempty"`
	Exit     float64    `json:"exit,omitempty"`
	ResultR  float64    `json:"result_r,omitempty"`

	// Sizing against the per-trade risk budget (ACTIVE signals only).
	Quantity      int64   `json:"quantity,omitempty"`
	Capital       float64 `json:"capital_required,omitempty"`
	MaxLoss       float64 `json:"max_loss,omitempty"`
	ProfitAt2R    float64 `json:"profit_at_target,omitempty"`
	CapitalCapped bool    `json:"capital_capped,omitempty"`

	// This symbol's own history with the rule (last ~20 sessions).
	History Stats `json:"history"`
}

// Result is the scanner payload.
type Result struct {
	AsOf         time.Time `json:"as_of"`
	SessionDate  string    `json:"session_date"`
	SessionOpen  bool      `json:"session_open"`
	SessionNote  string    `json:"session_note"`
	Universe     int       `json:"universe_size"`
	WithData     int       `json:"with_data"`
	Triggered    int       `json:"triggered_today"`
	RiskPerTrade float64   `json:"risk_per_trade"`
	Picks        []Pick    `json:"picks"`
	// Aggregate backtest of the identical rule across the whole universe.
	Backtest     Stats    `json:"backtest"`
	BacktestDays int      `json:"backtest_sessions"`
	PriceSource  string   `json:"price_source"`
	Caveats      []string `json:"caveats"`
	Rule         string   `json:"rule"`
}

// Scanner fans the ORB rule over the F&O universe.
type Scanner struct {
	market   marketdata.Provider
	universe *instruments.Store
	log      *slog.Logger

	mu     sync.Mutex
	cached *Result
	stamp  time.Time
	barsMu sync.Mutex
	bars   map[string]barsEntry
}

type barsEntry struct {
	days    []Day
	fetched time.Time
}

func NewScanner(market marketdata.Provider, universe *instruments.Store, log *slog.Logger) *Scanner {
	return &Scanner{
		market:   market,
		universe: universe,
		log:      log,
		bars:     make(map[string]barsEntry),
	}
}

// Scan runs (or serves from a short cache) the universe sweep.
func (s *Scanner) Scan(ctx context.Context, riskPerTrade, availableCash float64, topN int) (*Result, error) {
	s.mu.Lock()
	cached, stamp := s.cached, s.stamp
	s.mu.Unlock()

	if cached == nil || time.Since(stamp) > 5*time.Minute {
		fresh, err := s.sweep(ctx)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.cached, s.stamp = fresh, time.Now()
		s.mu.Unlock()
		cached = fresh
	}

	// Size per request; the cached sweep is sizing-free.
	out := *cached
	out.RiskPerTrade = riskPerTrade
	picks := make([]Pick, len(cached.Picks))
	copy(picks, cached.Picks)

	for i := range picks {
		if picks[i].Status != "ACTIVE" || picks[i].RiskShare <= 0 {
			continue
		}
		qty := math.Floor(riskPerTrade / picks[i].RiskShare)
		capped := false
		// One intraday idea should not eat the whole account.
		if maxCap := availableCash * 0.25; qty*picks[i].Entry > maxCap {
			qty = math.Floor(maxCap / picks[i].Entry)
			capped = true
		}
		if qty < 1 {
			continue
		}
		picks[i].Quantity = int64(qty)
		picks[i].Capital = round2(qty * picks[i].Entry)
		picks[i].MaxLoss = round2(qty * picks[i].RiskShare)
		picks[i].ProfitAt2R = round2(qty * picks[i].RiskShare * targetR)
		picks[i].CapitalCapped = capped
	}
	if topN > 0 && len(picks) > topN {
		picks = picks[:topN]
	}
	out.Picks = picks
	return &out, nil
}

// sweep fetches bars for every symbol, detects today's signals and backtests
// the rule per symbol and in aggregate.
func (s *Scanner) sweep(ctx context.Context) (*Result, error) {
	insts := s.universe.Universe()

	res := &Result{
		AsOf:     time.Now(),
		Universe: len(insts),
		Rule: "ORB long: 5m close above the 09:15–09:30 high, volume ≥1.5× session average, above VWAP; " +
			"stop = max(OR low, entry−1.5×ATR); target 2R; stop trails the high by 1R; square-off 15:15.",
		Caveats: []string{
			"One setup, long-only, one trade per symbol per day.",
			"Backtest fills are conservative (stop assumed before target inside a bar) but carry no brokerage/slippage.",
			"Outside market hours this is a replay of the most recent session — entries shown are stale, for study.",
		},
	}
	if chain, ok := s.market.(*marketdata.Chain); ok {
		res.PriceSource = chain.Active()
	}

	now := time.Now().In(istZone)
	mins := now.Hour()*60 + now.Minute()
	weekday := now.Weekday() != time.Saturday && now.Weekday() != time.Sunday
	res.SessionOpen = weekday && mins >= sessionOpen && mins < sessionClose
	if res.SessionOpen {
		res.SessionNote = "Market open — ACTIVE signals are live."
	} else {
		res.SessionNote = "Market closed — showing the most recent session as a replay; do not chase these entries."
	}

	type row struct {
		inst instruments.Instrument
		days []Day
	}
	var (
		rows []row
		mu   sync.Mutex
		wg   sync.WaitGroup
		sem  = make(chan struct{}, 6)
	)
	for _, inst := range insts {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(inst instruments.Instrument) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			days, err := s.sessionBars(ctx, inst.TradingSymbol)
			if err != nil || len(days) < 5 {
				return
			}
			mu.Lock()
			rows = append(rows, row{inst, days})
			mu.Unlock()
		}(inst)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].inst.TradingSymbol < rows[j].inst.TradingSymbol })
	res.WithData = len(rows)

	var allR []float64
	sessions := map[string]bool{}
	var picks []Pick

	for _, r := range rows {
		latest := r.days[len(r.days)-1]
		res.SessionDate = maxString(res.SessionDate, latest.Date)

		histStats, rs := Backtest(r.days, latest.Date)
		allR = append(allR, rs...)
		for _, d := range r.days {
			if d.Date != latest.Date {
				sessions[d.Date] = true
			}
		}

		sig, ok := Detect(latest)
		if !ok {
			continue
		}
		res.Triggered++

		status := "ACTIVE"
		var exitTime *time.Time
		if sig.Exited {
			status = sig.ExitKind
			t := sig.ExitTime
			exitTime = &t
		}

		last := latest.Bars[len(latest.Bars)-1].Close
		picks = append(picks, Pick{
			Symbol:    r.inst.TradingSymbol,
			Name:      r.inst.Name,
			Status:    status,
			EntryTime: sig.EntryTime,
			Entry:     sig.Entry,
			Stop:      sig.Stop,
			Target:    sig.Target,
			TrailBy:   sig.TrailBy,
			RiskShare: sig.Risk,
			ORHigh:    sig.ORHigh,
			ORLow:     sig.ORLow,
			VWAP:      sig.VWAP,
			RVOL:      sig.RVOL,
			LastPrice: round2(last),
			ExitTime:  exitTime,
			Exit:      sig.Exit,
			ResultR:   sig.ResultR,
			History:   histStats,
		})
	}

	res.Backtest, _ = statsOf(allR), allR
	res.BacktestDays = len(sessions)

	// Rank: running trades first, then the symbol's own expectancy with the
	// rule, then breakout conviction (relative volume).
	sort.SliceStable(picks, func(i, j int) bool {
		ai, aj := picks[i].Status == "ACTIVE", picks[j].Status == "ACTIVE"
		if ai != aj {
			return ai
		}
		if picks[i].History.AvgR != picks[j].History.AvgR {
			return picks[i].History.AvgR > picks[j].History.AvgR
		}
		return picks[i].RVOL > picks[j].RVOL
	})
	for i := range picks {
		picks[i].Rank = i + 1
	}
	res.Picks = picks
	return res, nil
}

// sessionBars fetches ~1 month of 5m bars (the deepest window the public feed
// serves at that interval) and splits them into sessions. Cached for 5
// minutes so a rescan during market hours picks up fresh bars.
func (s *Scanner) sessionBars(ctx context.Context, symbol string) ([]Day, error) {
	s.barsMu.Lock()
	if hit, ok := s.bars[symbol]; ok && time.Since(hit.fetched) < 5*time.Minute {
		s.barsMu.Unlock()
		return hit.days, nil
	}
	s.barsMu.Unlock()

	end := time.Now()
	candles, err := s.market.Candles(ctx, marketdata.CandleRequest{
		Exchange:        "NSE",
		Segment:         "CASH",
		Symbol:          symbol,
		IntervalMinutes: 5,
		Start:           end.AddDate(0, 0, -29),
		End:             end,
	})
	if err != nil {
		return nil, err
	}
	days := SplitDays(candles)

	s.barsMu.Lock()
	s.bars[symbol] = barsEntry{days: days, fetched: time.Now()}
	s.barsMu.Unlock()
	return days, nil
}

func maxString(a, b string) string {
	if b > a {
		return b
	}
	return a
}
