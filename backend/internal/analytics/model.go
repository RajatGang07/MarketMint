package analytics

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

// Weights of the composite score. They sum to 1 and are deliberately boring:
// most of the signal is momentum, the rest rewards smooth, participating,
// not-yet-overheated trends. Documented in the README; change them there too.
const (
	wMomentum60 = 0.30
	wMomentum20 = 0.20
	wTrend      = 0.15
	wProximity  = 0.15
	wVolume     = 0.10
	wRSIBand    = 0.10
)

// Hard screens applied before scoring.
const (
	minPrice    = 50.0 // below this, tick size and impact dominate
	minTurnover = 5e7  // ₹5 crore average daily traded value
	maxRSI      = 80.0 // do not chase a vertical chart
	histRange   = "1y" // bars fetched per symbol
	holdDays    = 21   // backtest horizon: ~one trading month
	lookback    = 63   // feature window: ~three trading months
)

// Candidate is one scored stock, with everything needed to explain the score.
type Candidate struct {
	Symbol   string   `json:"symbol"`
	Name     string   `json:"name"`
	Score    float64  `json:"score"`
	Rank     int      `json:"rank"`
	Features Features `json:"features"`
	// ZContributions break the score into its weighted parts, so the UI can
	// show *why* a stock ranks where it does.
	ZContributions map[string]float64 `json:"z_contributions"`
	Plan           *Plan              `json:"plan,omitempty"`
	PlanNote       string             `json:"plan_note,omitempty"`
}

// Backtest is the honesty box: the same scoring rule replayed on history.
type Backtest struct {
	Folds        int     `json:"folds"`
	MeanIC       float64 `json:"mean_ic"`
	TopDecileHit float64 `json:"top_decile_hit_rate"`
	TopDecileRet float64 `json:"top_decile_mean_return"`
	UniverseRet  float64 `json:"universe_mean_return"`
	HorizonDays  int     `json:"horizon_days"`
	Note         string  `json:"note"`
}

// Result is the full recommendation payload.
type Result struct {
	AsOf        time.Time   `json:"as_of"`
	Universe    int         `json:"universe_size"`
	Scored      int         `json:"scored"`
	Skipped     int         `json:"skipped"`
	Picks       []Candidate `json:"picks"`
	Others      []Candidate `json:"others"`
	Bands       RiskBands   `json:"risk_bands"`
	Backtest    Backtest    `json:"backtest"`
	PriceSource string      `json:"price_source"`
	Caveats     []string    `json:"caveats"`
}

// Engine owns the candle cache and runs scans.
type Engine struct {
	market   marketdata.Provider
	universe *instruments.Store
	log      *slog.Logger

	// Daily bars change once a day; cache them hard.
	barsMu    sync.Mutex
	barsCache map[string]barsEntry

	// A full scan is a couple hundred upstream calls; cache the result.
	scanMu    sync.Mutex
	lastScan  *Result
	scanStamp time.Time
}

type barsEntry struct {
	candles []marketdata.Candle
	fetched time.Time
}

func New(market marketdata.Provider, universe *instruments.Store, log *slog.Logger) *Engine {
	return &Engine{
		market:    market,
		universe:  universe,
		log:       log,
		barsCache: make(map[string]barsEntry),
	}
}

// cachedScan returns the last universe scan, refreshing it when it is older
// than 15 minutes — daily bars do not move faster than that.
func (e *Engine) cachedScan(ctx context.Context) (*Result, error) {
	e.scanMu.Lock()
	cached := e.lastScan
	fresh := time.Since(e.scanStamp) < 15*time.Minute
	e.scanMu.Unlock()

	if cached != nil && fresh {
		return cached, nil
	}
	scanned, err := e.scan(ctx)
	if err != nil {
		return nil, err
	}
	e.scanMu.Lock()
	e.lastScan, e.scanStamp = scanned, time.Now()
	e.scanMu.Unlock()
	return scanned, nil
}

// Scored exposes the full ranked candidate list (no sizing) plus the scan
// metadata — the signals board builds its verdicts from it.
func (e *Engine) Scored(ctx context.Context) ([]Candidate, *Result, error) {
	res, err := e.cachedScan(ctx)
	if err != nil {
		return nil, nil, err
	}
	return res.Others, res, nil
}

// PlanFor sizes one candidate against the bands and available cash — used
// when the board wants a plan for a symbol outside the default top picks.
func PlanFor(c Candidate, bands RiskBands, availableCash float64) (Plan, bool) {
	return buildPlan(c.Features.LastClose, c.Features.ATR14, bands, availableCash)
}

// Recommend scores the universe and sizes the top picks against the risk
// bands and available cash.
func (e *Engine) Recommend(ctx context.Context, bands RiskBands, availableCash float64, topN int) (*Result, error) {
	res, err := e.cachedScan(ctx)
	if err != nil {
		return nil, err
	}

	// Sizing is cheap and depends on the caller's cash, so it is done per
	// request on a copy rather than baked into the cached scan.
	out := *res
	out.Bands = bands
	out.Picks = make([]Candidate, 0, topN)
	out.Others = nil

	for _, c := range res.Others {
		c := c
		if len(out.Picks) < topN {
			if plan, ok := buildPlan(c.Features.LastClose, c.Features.ATR14, bands, availableCash); ok {
				c.Plan = &plan
				if plan.CapitalCapped {
					c.PlanNote = "quantity capped by available cash; loss/profit will run below the requested bands"
				}
				out.Picks = append(out.Picks, c)
				continue
			}
			c.PlanNote = "no whole-share quantity fits the loss band at this volatility"
		}
		out.Others = append(out.Others, c)
		if len(out.Others) >= 20 {
			// The tail is context, not a shopping list.
			if len(out.Picks) >= topN {
				break
			}
		}
	}
	return &out, nil
}

// scan fetches bars for the whole universe, scores today, and backtests the
// rule on the same data.
func (e *Engine) scan(ctx context.Context) (*Result, error) {
	insts := e.universe.Universe()
	res := &Result{
		AsOf:     time.Now(),
		Universe: len(insts),
		Caveats: []string{
			"Momentum ranking, not a forecast: the backtest hit rate below is the honest ceiling.",
			"Signals use daily closes; intraday moves will differ.",
			"No brokerage/STT/slippage in the paper fills.",
		},
	}
	if chain, ok := e.market.(*marketdata.Chain); ok {
		res.PriceSource = chain.Active()
	}

	type fetched struct {
		inst    instruments.Instrument
		candles []marketdata.Candle
	}

	// Fetch with modest concurrency; the upstream is a free public feed and
	// aggressive fan-out earns a rate limit, not a speedup.
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var rows []fetched

	for _, inst := range insts {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(inst instruments.Instrument) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			candles, err := e.dailyBars(ctx, inst.TradingSymbol)
			if err != nil || len(candles) < minBars {
				return
			}
			mu.Lock()
			rows = append(rows, fetched{inst, candles})
			mu.Unlock()
		}(inst)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Deterministic order regardless of fetch completion order.
	sort.Slice(rows, func(i, j int) bool { return rows[i].inst.TradingSymbol < rows[j].inst.TradingSymbol })

	// Score today.
	var usable []scoredRow
	for _, row := range rows {
		b := toBars(row.candles)
		f, ok := computeFeatures(b, b.len()-1)
		if !ok {
			continue
		}
		if f.LastClose < minPrice || f.Turnover20 < minTurnover || f.RSI14 > maxRSI {
			res.Skipped++
			continue
		}
		usable = append(usable, scoredRow{row.inst, f, b})
	}
	res.Scored = len(usable)

	scores, contribs := scoreCrossSection(featuresOf(usable))
	all := make([]Candidate, len(usable))
	for i, row := range usable {
		all[i] = Candidate{
			Symbol:         row.inst.TradingSymbol,
			Name:           row.inst.Name,
			Score:          scores[i],
			Features:       row.f,
			ZContributions: contribs[i],
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Score > all[j].Score })
	for i := range all {
		all[i].Rank = i + 1
	}
	res.Others = all // Recommend() slices picks out of this ranked list

	// Backtest the identical rule on the same bars.
	res.Backtest = e.backtest(usableBars(usable))
	return res, nil
}

// scoredRow pairs an instrument with its features and raw bars.
type scoredRow struct {
	inst instruments.Instrument
	f    Features
	b    bars
}

func featuresOf(rows []scoredRow) []Features {
	out := make([]Features, len(rows))
	for i, r := range rows {
		out[i] = r.f
	}
	return out
}

func usableBars(rows []scoredRow) []bars {
	out := make([]bars, len(rows))
	for i, r := range rows {
		out[i] = r.b
	}
	return out
}

// scoreCrossSection z-scores each feature across the universe and combines
// them with the documented weights. Returns the composite plus the per-stock
// weighted contributions for the explain panel.
func scoreCrossSection(feats []Features) ([]float64, []map[string]float64) {
	n := len(feats)
	pull := func(get func(Features) float64) []float64 {
		xs := make([]float64, n)
		for i, f := range feats {
			xs[i] = get(f)
		}
		return zScores(xs)
	}

	zMom60 := pull(func(f Features) float64 { return f.Momentum60 })
	zMom20 := pull(func(f Features) float64 { return f.Momentum20 })
	zTrend := pull(func(f Features) float64 { return f.TrendPersistence })
	zProx := pull(func(f Features) float64 { return f.ProximityToHigh })
	zVol := pull(func(f Features) float64 { return f.VolumeRatio })
	// RSI is scored as distance from 60: a healthy uptrend hums there, while
	// both exhaustion (85) and weakness (35) drift away from it.
	zRSI := pull(func(f Features) float64 { return -math.Abs(f.RSI14 - 60) })

	scores := make([]float64, n)
	contribs := make([]map[string]float64, n)
	for i := 0; i < n; i++ {
		c := map[string]float64{
			"momentum_60d":      wMomentum60 * zMom60[i],
			"momentum_20d":      wMomentum20 * zMom20[i],
			"trend_persistence": wTrend * zTrend[i],
			"proximity_to_high": wProximity * zProx[i],
			"volume_ratio":      wVolume * zVol[i],
			"rsi_band":          wRSIBand * zRSI[i],
		}
		s := 0.0
		for _, v := range c {
			s += v
		}
		scores[i] = s
		contribs[i] = c
	}
	return scores, contribs
}

// backtest replays the scoring rule at ~monthly steps across the fetched
// history: features on the trailing 63 bars, forward return over the next 21.
// It reports rank-IC and how the top decile actually did.
func (e *Engine) backtest(universe []bars) Backtest {
	bt := Backtest{HorizonDays: holdDays}

	// Fold evaluation points, stepping back from the latest usable day.
	minLen := math.MaxInt
	for _, b := range universe {
		if b.len() < minLen {
			minLen = b.len()
		}
	}
	if minLen == math.MaxInt || minLen < minBars+holdDays {
		bt.Note = "not enough shared history to backtest"
		return bt
	}

	var ics []float64
	var topHits, topCount int
	var topRetSum, uniRetSum float64
	var uniCount int

	for end := minLen - 1 - holdDays; end >= minBars-1; end -= holdDays {
		var scoresIn []Features
		var fwd []float64
		for _, b := range universe {
			// Align folds on the shared tail so every stock is evaluated on
			// the same calendar-ish day.
			offset := b.len() - minLen
			f, ok := computeFeatures(b, offset+end)
			if !ok {
				continue
			}
			entry := b.close[offset+end]
			exit := b.close[offset+end+holdDays]
			if entry <= 0 {
				continue
			}
			scoresIn = append(scoresIn, f)
			fwd = append(fwd, exit/entry-1)
		}
		if len(scoresIn) < 20 {
			continue
		}

		scores, _ := scoreCrossSection(scoresIn)
		ics = append(ics, spearmanIC(scores, fwd))

		// Top decile performance.
		idx := make([]int, len(scores))
		for i := range idx {
			idx[i] = i
		}
		sort.Slice(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
		decile := len(idx) / 10
		if decile < 5 {
			decile = 5
		}
		for _, i := range idx[:decile] {
			if fwd[i] > 0 {
				topHits++
			}
			topRetSum += fwd[i]
			topCount++
		}
		for _, r := range fwd {
			uniRetSum += r
			uniCount++
		}
	}

	bt.Folds = len(ics)
	if len(ics) > 0 {
		sum := 0.0
		for _, ic := range ics {
			sum += ic
		}
		bt.MeanIC = sum / float64(len(ics))
	}
	if topCount > 0 {
		bt.TopDecileHit = float64(topHits) / float64(topCount)
		bt.TopDecileRet = topRetSum / float64(topCount)
	}
	if uniCount > 0 {
		bt.UniverseRet = uniRetSum / float64(uniCount)
	}
	bt.Note = "same rule, walked forward monthly over the fetched year; IC is Spearman rank correlation with the next month's return"
	return bt
}

// dailyBars fetches (and caches for an hour) one symbol's daily candles.
func (e *Engine) dailyBars(ctx context.Context, symbol string) ([]marketdata.Candle, error) {
	e.barsMu.Lock()
	if hit, ok := e.barsCache[symbol]; ok && time.Since(hit.fetched) < time.Hour {
		e.barsMu.Unlock()
		return hit.candles, nil
	}
	e.barsMu.Unlock()

	end := time.Now()
	candles, err := e.market.Candles(ctx, marketdata.CandleRequest{
		Exchange:        "NSE",
		Segment:         "CASH",
		Symbol:          symbol,
		IntervalMinutes: 1440,
		Start:           end.AddDate(-1, 0, 0),
		End:             end,
	})
	if err != nil {
		return nil, err
	}

	e.barsMu.Lock()
	e.barsCache[symbol] = barsEntry{candles: candles, fetched: time.Now()}
	e.barsMu.Unlock()
	return candles, nil
}
