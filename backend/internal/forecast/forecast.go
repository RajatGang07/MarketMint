// Package forecast produces multi-horizon directional leans for one stock:
// which way price is more likely to move over the next ~15 minutes, by the
// close, and into the next session — with every driver shown and every
// weakness admitted.
//
// This is NOT price prediction. Short-horizon direction is mostly noise, and
// the honest framing is a probability barely away from 50% plus the reasons.
// The engine combines four evidence streams:
//
//	technical  daily bars: momentum, trend persistence, RSI, distance to MAs.
//	flow       5-minute bars: position vs VWAP, up-volume share, close
//	           location, short momentum — a *proxy* for order-book pressure,
//	           since no depth feed is wired.
//	volatility ATR regime, which scales confidence rather than direction.
//	news       recent headline sentiment (see the news package).
//
// The seconds horizon is included because users ask for it, and it answers
// with the truth: at that scale price is indistinguishable from noise on
// minute bars, so it reports ~50% with zero confidence.
package forecast

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/gangrajat/groww-paper-trading/backend/internal/instruments"
	"github.com/gangrajat/groww-paper-trading/backend/internal/marketdata"
	"github.com/gangrajat/groww-paper-trading/backend/internal/news"
)

// Horizon identifies one forecast window.
type Horizon string

const (
	HorizonSeconds Horizon = "seconds"  // ~10s–1m
	HorizonIntra   Horizon = "intraday" // ~15 minutes
	HorizonClose   Horizon = "close"    // rest of today's session
	HorizonNextDay Horizon = "next_day" // next session / short swing
)

// Driver is one piece of evidence behind a lean, in plain language.
type Driver struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
	// Score is this driver's directional vote, -1 (down) .. +1 (up).
	Score float64 `json:"score"`
	// Weight is how much the vote counted in this horizon.
	Weight float64 `json:"weight"`
}

// Lean is the forecast for one horizon.
type Lean struct {
	Horizon Horizon `json:"horizon"`
	Label   string  `json:"label"`
	// Direction is "up", "down" or "flat" (used when the lean is negligible).
	Direction string `json:"direction"`
	// ProbabilityUp is the estimated chance the price is higher at the end
	// of the horizon, in percent. Values hug 50 by design.
	ProbabilityUp float64 `json:"probability_up"`
	// Confidence is "none", "low", "medium" — never "high"; nothing at these
	// horizons deserves it.
	Confidence string   `json:"confidence"`
	Drivers    []Driver `json:"drivers,omitempty"`
	Note       string   `json:"note,omitempty"`
}

// Result is the full payload for one symbol.
type Result struct {
	Symbol      string      `json:"symbol"`
	Name        string      `json:"name,omitempty"`
	AsOf        time.Time   `json:"as_of"`
	LastPrice   float64     `json:"last_price"`
	ChangePct   float64     `json:"change_pct"`
	SessionOpen bool        `json:"session_open"`
	Leans       []Lean      `json:"leans"`
	News        news.Result `json:"news"`
	PriceSource string      `json:"price_source,omitempty"`
	Caveats     []string    `json:"caveats"`
}

// Engine runs forecasts. Stateless beyond its dependencies.
type Engine struct {
	market   marketdata.Provider
	universe *instruments.Store
	news     *news.Fetcher
	log      *slog.Logger
}

func New(market marketdata.Provider, universe *instruments.Store, newsFetcher *news.Fetcher, log *slog.Logger) *Engine {
	return &Engine{market: market, universe: universe, news: newsFetcher, log: log}
}

// istZone matches the exchange clock (same convention as package intraday).
var istZone = time.FixedZone("IST", 5*3600+1800)

func sessionOpen(now time.Time) bool {
	t := now.In(istZone)
	if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	mins := t.Hour()*60 + t.Minute()
	return mins >= 9*60+15 && mins < 15*60+30
}

// Run builds the forecast for one symbol.
func (e *Engine) Run(ctx context.Context, exchange, symbol string) (Result, error) {
	now := time.Now()

	quote, err := e.market.Quote(ctx, exchange, "CASH", symbol)
	if err != nil {
		return Result{}, fmt.Errorf("quote: %w", err)
	}
	last, _ := quote.LastPrice.Float64()
	if last <= 0 {
		return Result{}, fmt.Errorf("no price for %s", symbol)
	}
	changePct, _ := quote.ChangePct().Float64()

	name := ""
	if inst, ok := e.universe.Lookup(exchange, symbol); ok {
		name = inst.Name
	}

	// Daily bars: one year, for the technical picture.
	daily, err := e.bars(ctx, exchange, symbol, 1440, now.AddDate(-1, 0, 0), now)
	if err != nil {
		e.log.Warn("forecast: daily bars unavailable", "symbol", symbol, "err", err)
	}
	// 5m bars: the last five days, for the intraday flow proxy.
	intra, err := e.bars(ctx, exchange, symbol, 5, now.AddDate(0, 0, -5), now)
	if err != nil {
		e.log.Warn("forecast: intraday bars unavailable", "symbol", symbol, "err", err)
	}

	tech := analyzeDaily(daily, last)
	flow := analyzeIntraday(intra, now)
	nw := e.news.ForSymbol(ctx, symbol, name)

	open := sessionOpen(now)
	res := Result{
		Symbol:      symbol,
		Name:        name,
		AsOf:        now,
		LastPrice:   last,
		ChangePct:   changePct,
		SessionOpen: open,
		News:        nw,
		Leans: []Lean{
			secondsLean(),
			intradayLean(flow, nw, open),
			closeLean(tech, flow, nw, open),
			nextDayLean(tech, nw),
		},
		Caveats: caveats(tech, flow, open),
	}
	if chain, ok := e.market.(*marketdata.Chain); ok {
		res.PriceSource = chain.Active()
	}
	return res, nil
}

func (e *Engine) bars(ctx context.Context, exchange, symbol string, interval int, start, end time.Time) ([]bar, error) {
	candles, err := e.market.Candles(ctx, marketdata.CandleRequest{
		Exchange: exchange, Segment: "CASH", Symbol: symbol,
		IntervalMinutes: interval, Start: start, End: end,
	})
	if err != nil {
		return nil, err
	}
	out := make([]bar, 0, len(candles))
	for _, c := range candles {
		b := bar{Time: c.Time}
		b.Open, _ = c.Open.Float64()
		b.High, _ = c.High.Float64()
		b.Low, _ = c.Low.Float64()
		b.Close, _ = c.Close.Float64()
		b.Volume, _ = c.Volume.Float64()
		if b.Close > 0 {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out, nil
}

// ---------------------------------------------------------------------------
// Feature extraction
// ---------------------------------------------------------------------------

type bar struct {
	Time                           time.Time
	Open, High, Low, Close, Volume float64
}

// techView is the daily-bar picture.
type techView struct {
	ok               bool
	rsi14            float64
	momentum20       float64 // 20-session simple return
	momentum60       float64
	trendPersistence float64 // share of last 60 closes above SMA20
	aboveSMA20       bool
	aboveSMA50       bool
	atrPct           float64 // ATR14 / close — the volatility regime
}

func analyzeDaily(bars []bar, last float64) techView {
	n := len(bars)
	if n < 65 || last <= 0 {
		return techView{}
	}
	closes := make([]float64, n)
	for i, b := range bars {
		closes[i] = b.Close
	}
	end := n - 1

	v := techView{ok: true}
	v.rsi14 = rsi(closes, end, 14)
	if p := closes[end-20]; p > 0 {
		v.momentum20 = last/p - 1
	}
	if p := closes[end-60]; p > 0 {
		v.momentum60 = last/p - 1
	}
	above := 0
	for i := end - 59; i <= end; i++ {
		if closes[i] > sma(closes, i, 20) {
			above++
		}
	}
	v.trendPersistence = float64(above) / 60
	v.aboveSMA20 = last > sma(closes, end, 20)
	v.aboveSMA50 = last > sma(closes, end, 50)
	if a := atr(bars, end, 14); a > 0 {
		v.atrPct = a / last
	}
	return v
}

// flowView is the intraday order-flow proxy, computed on today's 5m bars.
type flowView struct {
	ok         bool
	vsVWAP     float64 // (last - vwap) / vwap
	upVolShare float64 // volume on up-bars / total, 0..1
	closeLoc   float64 // mean close-location-value of last bars, -1..1
	mom15      float64 // return over the last three 5m bars
	barsToday  int
}

func analyzeIntraday(bars []bar, now time.Time) flowView {
	today := now.In(istZone).Format("2006-01-02")
	var day []bar
	for _, b := range bars {
		t := b.Time.In(istZone)
		mins := t.Hour()*60 + t.Minute()
		if t.Format("2006-01-02") == today && mins >= 9*60+15 && mins < 15*60+30 {
			day = append(day, b)
		}
	}
	if len(day) < 4 {
		return flowView{}
	}

	v := flowView{ok: true, barsToday: len(day)}

	var pv, vol, upVol float64
	var clvSum float64
	clvN := 0
	for _, b := range day {
		typ := (b.High + b.Low + b.Close) / 3
		pv += typ * b.Volume
		vol += b.Volume
		if b.Close >= b.Open {
			upVol += b.Volume
		}
		if r := b.High - b.Low; r > 0 {
			clvSum += ((b.Close - b.Low) - (b.High - b.Close)) / r
			clvN++
		}
	}
	last := day[len(day)-1].Close
	v.upVolShare = 0.5 // no volume data reads as balanced, not bearish
	if vol > 0 {
		vwap := pv / vol
		if vwap > 0 {
			v.vsVWAP = (last - vwap) / vwap
		}
		v.upVolShare = upVol / vol
	}
	if clvN > 0 {
		v.closeLoc = clvSum / float64(clvN)
	}
	if p := day[len(day)-4].Close; p > 0 {
		v.mom15 = last/p - 1
	}
	return v
}

// ---------------------------------------------------------------------------
// Leans
// ---------------------------------------------------------------------------

// squash turns a weighted score into a probability that hugs 50%. The 12-point
// ceiling is deliberate: nothing on these horizons justifies 90% claims.
func squash(score float64) float64 {
	return 50 + 12*math.Tanh(score)
}

func direction(p float64) string {
	switch {
	case p >= 52:
		return "up"
	case p <= 48:
		return "down"
	default:
		return "flat"
	}
}

func secondsLean() Lean {
	return Lean{
		Horizon:       HorizonSeconds,
		Label:         "Next 10s – 1 min",
		Direction:     "flat",
		ProbabilityUp: 50,
		Confidence:    "none",
		Note: "Not predictable from this data. Second-scale moves are driven by order-book " +
			"microstructure (queue positions, cancellations, spread) that this platform does not " +
			"receive — and is essentially a coin flip even for firms that do. Shown so the limit is " +
			"explicit, not hidden.",
	}
}

func intradayLean(f flowView, nw news.Result, open bool) Lean {
	l := Lean{Horizon: HorizonIntra, Label: "Next ~15 minutes"}
	if !f.ok {
		l.Direction, l.ProbabilityUp, l.Confidence = "flat", 50, "none"
		l.Note = "Not enough of today's 5-minute bars to read intraday flow."
		if !open {
			l.Note = "Market closed — intraday flow resets at the next open."
		}
		return l
	}

	l.Drivers = []Driver{
		{Name: "Price vs VWAP", Weight: 0.35, Score: clampScore(f.vsVWAP * 400),
			Detail: fmt.Sprintf("Trading %+.2f%% vs today's volume-weighted average price. %s", f.vsVWAP*100, vwapWord(f.vsVWAP))},
		{Name: "Buy/sell volume proxy", Weight: 0.25, Score: clampScore((f.upVolShare - 0.5) * 4),
			Detail: fmt.Sprintf("%.0f%% of today's volume traded on rising 5-min bars. No order-book depth feed is wired, so this volume split is the closest available proxy for buy vs sell pressure.", f.upVolShare*100)},
		{Name: "Close location", Weight: 0.15, Score: clampScore(f.closeLoc * 1.5),
			Detail: fmt.Sprintf("Bars are closing %s their ranges (CLV %+.2f) — %s.", clvWord(f.closeLoc), f.closeLoc, clvMeaning(f.closeLoc))},
		{Name: "15-min momentum", Weight: 0.15, Score: clampScore(f.mom15 * 300),
			Detail: fmt.Sprintf("%+.2f%% over the last three 5-min bars.", f.mom15*100)},
		newsDriver(nw, 0.10),
	}
	score := weightedScore(l.Drivers)
	l.ProbabilityUp = squash(score)
	l.Direction = direction(l.ProbabilityUp)
	l.Confidence = "low"
	if !open {
		l.Confidence = "none"
		l.Note = "Market closed — this reads the last session's tape and goes stale at the open."
	}
	return l
}

func closeLean(t techView, f flowView, nw news.Result, open bool) Lean {
	l := Lean{Horizon: HorizonClose, Label: "By today's close"}
	if !t.ok && !f.ok {
		l.Direction, l.ProbabilityUp, l.Confidence = "flat", 50, "none"
		l.Note = "Not enough history to form a view."
		return l
	}
	if f.ok {
		l.Drivers = append(l.Drivers,
			Driver{Name: "Price vs VWAP", Weight: 0.25, Score: clampScore(f.vsVWAP * 400),
				Detail: fmt.Sprintf("Trading %+.2f%% vs VWAP; institutional flow tends to defend this line into the close.", f.vsVWAP*100)},
			Driver{Name: "Buy/sell volume proxy", Weight: 0.20, Score: clampScore((f.upVolShare - 0.5) * 4),
				Detail: fmt.Sprintf("%.0f%% of volume on rising bars (proxy — no depth feed).", f.upVolShare*100)},
		)
	}
	if t.ok {
		l.Drivers = append(l.Drivers,
			Driver{Name: "Daily trend", Weight: 0.20, Score: trendScore(t),
				Detail: trendDetail(t)},
			Driver{Name: "RSI(14)", Weight: 0.15, Score: rsiScore(t.rsi14),
				Detail: fmt.Sprintf("RSI %.0f — %s.", t.rsi14, rsiWord(t.rsi14))},
		)
	}
	l.Drivers = append(l.Drivers, newsDriver(nw, 0.20))

	score := weightedScore(l.Drivers)
	l.ProbabilityUp = squash(score)
	l.Direction = direction(l.ProbabilityUp)
	l.Confidence = "low"
	if t.ok && f.ok {
		l.Confidence = "medium"
	}
	if !open {
		l.Confidence = "none"
		l.Note = "Market closed — no session left to forecast; treat this as a next-open read."
	}
	return l
}

func nextDayLean(t techView, nw news.Result) Lean {
	l := Lean{Horizon: HorizonNextDay, Label: "Next session / short swing"}
	if !t.ok {
		l.Direction, l.ProbabilityUp, l.Confidence = "flat", 50, "none"
		l.Note = "Needs about three months of daily history; not enough bars available."
		return l
	}
	l.Drivers = []Driver{
		{Name: "Momentum (1 month)", Weight: 0.25, Score: clampScore(t.momentum20 * 8),
			Detail: fmt.Sprintf("%+.1f%% over ~20 sessions. Cross-sectional momentum is the best-evidenced equity signal at this horizon.", t.momentum20*100)},
		{Name: "Momentum (3 months)", Weight: 0.20, Score: clampScore(t.momentum60 * 4),
			Detail: fmt.Sprintf("%+.1f%% over ~60 sessions.", t.momentum60*100)},
		{Name: "Trend persistence", Weight: 0.20, Score: clampScore((t.trendPersistence - 0.5) * 3),
			Detail: fmt.Sprintf("Closed above the 20-day average in %.0f%% of the last 60 sessions — %s.", t.trendPersistence*100, persistenceWord(t.trendPersistence))},
		{Name: "Moving averages", Weight: 0.10, Score: trendScore(t),
			Detail: trendDetail(t)},
		{Name: "RSI(14)", Weight: 0.10, Score: rsiScore(t.rsi14),
			Detail: fmt.Sprintf("RSI %.0f — %s.", t.rsi14, rsiWord(t.rsi14))},
		newsDriver(nw, 0.15),
	}
	score := weightedScore(l.Drivers)
	l.ProbabilityUp = squash(score)
	l.Direction = direction(l.ProbabilityUp)
	l.Confidence = "medium"
	if t.atrPct > 0.035 {
		l.Confidence = "low"
		l.Note = fmt.Sprintf("High volatility regime (ATR %.1f%% of price) — daily range can swamp any directional edge.", t.atrPct*100)
	}
	return l
}

func newsDriver(nw news.Result, weight float64) Driver {
	d := Driver{Name: "News sentiment", Weight: weight}
	if nw.Method == "none" || len(nw.Items) == 0 {
		d.Detail = "No recent headlines available; news contributes nothing to this lean."
		return d
	}
	d.Score = clampScore(nw.Overall * 1.5)
	d.Detail = fmt.Sprintf("%d recent headlines score %+.2f overall (%s).", len(nw.Items), nw.Overall, nw.Method)
	return d
}

func weightedScore(drivers []Driver) float64 {
	var s float64
	for _, d := range drivers {
		s += d.Score * d.Weight
	}
	return s
}

func caveats(t techView, f flowView, open bool) []string {
	out := []string{
		"Directional leans, not price targets. Probabilities are capped near 50% because short-horizon direction is mostly noise.",
		"Buy/sell pressure is inferred from volume on rising vs falling bars — the account has no order-book depth (Level 2) feed.",
		"Nothing here is investment advice; this is a paper-trading analysis tool.",
	}
	if !t.ok {
		out = append(out, "Daily history was unavailable, so trend and momentum drivers are missing.")
	}
	if !f.ok && open {
		out = append(out, "Today's 5-minute bars were unavailable, so intraday flow drivers are missing.")
	}
	return out
}

// ---------------------------------------------------------------------------
// Scoring helpers and plain-language phrasing
// ---------------------------------------------------------------------------

func clampScore(v float64) float64 {
	if v > 1 {
		return 1
	}
	if v < -1 {
		return -1
	}
	return v
}

func trendScore(t techView) float64 {
	s := 0.0
	if t.aboveSMA20 {
		s += 0.5
	} else {
		s -= 0.5
	}
	if t.aboveSMA50 {
		s += 0.5
	} else {
		s -= 0.5
	}
	return s
}

func trendDetail(t techView) string {
	rel := func(above bool) string {
		if above {
			return "above"
		}
		return "below"
	}
	return fmt.Sprintf("Price is %s the 20-day and %s the 50-day moving average.", rel(t.aboveSMA20), rel(t.aboveSMA50))
}

// rsiScore votes mean-reversion at the extremes and mildly with the trend in
// the middle band.
func rsiScore(rsi float64) float64 {
	switch {
	case rsi >= 75:
		return -0.6 // overbought: odds of a pause/pullback rise
	case rsi >= 60:
		return 0.3
	case rsi > 45:
		return 0
	case rsi > 30:
		return -0.3
	default:
		return 0.5 // deeply oversold: bounce odds rise
	}
}

func rsiWord(rsi float64) string {
	switch {
	case rsi >= 75:
		return "overbought; stretched moves tend to pause or pull back"
	case rsi >= 60:
		return "strong but not stretched"
	case rsi > 45:
		return "neutral"
	case rsi > 30:
		return "weak"
	default:
		return "oversold; bounces become more likely"
	}
}

func vwapWord(v float64) string {
	switch {
	case v > 0.002:
		return "Buyers have controlled the session so far."
	case v < -0.002:
		return "Sellers have controlled the session so far."
	default:
		return "The session is balanced around VWAP."
	}
}

func clvWord(clv float64) string {
	switch {
	case clv > 0.15:
		return "near the top of"
	case clv < -0.15:
		return "near the bottom of"
	default:
		return "mid-range within"
	}
}

func clvMeaning(clv float64) string {
	switch {
	case clv > 0.15:
		return "buyers are absorbing supply"
	case clv < -0.15:
		return "sellers are pressing into demand"
	default:
		return "neither side is winning the bar"
	}
}

func persistenceWord(p float64) string {
	switch {
	case p >= 0.75:
		return "a smooth, persistent uptrend"
	case p >= 0.55:
		return "a decent uptrend with some chop"
	case p >= 0.45:
		return "sideways chop"
	default:
		return "a persistent downtrend"
	}
}

// ---------------------------------------------------------------------------
// Indicators (duplicated from package analytics on purpose: analytics keeps
// them unexported, and these four one-liners are not worth an export churn)
// ---------------------------------------------------------------------------

func sma(xs []float64, end, n int) float64 {
	if n <= 0 || end+1 < n {
		return 0
	}
	sum := 0.0
	for i := end - n + 1; i <= end; i++ {
		sum += xs[i]
	}
	return sum / float64(n)
}

func rsi(close []float64, end, period int) float64 {
	if end < period {
		return 50
	}
	var gain, loss float64
	for i := end - period + 1; i <= end; i++ {
		d := close[i] - close[i-1]
		if d > 0 {
			gain += d
		} else {
			loss -= d
		}
	}
	if gain+loss == 0 {
		return 50
	}
	return 100 * gain / (gain + loss)
}

func atr(b []bar, end, period int) float64 {
	if end < period {
		return 0
	}
	sum := 0.0
	for i := end - period + 1; i <= end; i++ {
		tr := b[i].High - b[i].Low
		tr = math.Max(tr, math.Abs(b[i].High-b[i-1].Close))
		tr = math.Max(tr, math.Abs(b[i].Low-b[i-1].Close))
		sum += tr
	}
	return sum / float64(period)
}
