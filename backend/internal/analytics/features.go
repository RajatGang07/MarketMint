// Package analytics scores the tradable universe on ~3 months of daily bars
// and turns the result into risk-sized trade ideas.
//
// The model is a transparent momentum/trend composite, not a forecaster: it
// ranks stocks by how persistently they have been going up without being
// overextended, because over 1–2 month horizons cross-sectional momentum is
// the one equity anomaly with enough evidence to lean on. Every idea ships
// with the feature breakdown and the rule's own backtest, so the user sees
// exactly how weak or strong the edge is.
package analytics

import (
	"math"

	"github.com/gangrajat/groww-paper-trading/backend/internal/marketdata"
)

// bars is a convenience view over candles as float64 closes/highs/lows.
type bars struct {
	close  []float64
	high   []float64
	low    []float64
	volume []float64
}

func toBars(candles []marketdata.Candle) bars {
	b := bars{
		close:  make([]float64, len(candles)),
		high:   make([]float64, len(candles)),
		low:    make([]float64, len(candles)),
		volume: make([]float64, len(candles)),
	}
	for i, c := range candles {
		b.close[i], _ = c.Close.Float64()
		b.high[i], _ = c.High.Float64()
		b.low[i], _ = c.Low.Float64()
		b.volume[i], _ = c.Volume.Float64()
	}
	return b
}

func (b bars) len() int { return len(b.close) }

// Features are the raw per-stock measurements the score is built from.
// All are computed on the trailing window ending at the evaluation day.
type Features struct {
	// Momentum60 and Momentum20 are simple returns over ~3 months and ~1
	// month of trading days.
	Momentum60 float64 `json:"momentum_60d"`
	Momentum20 float64 `json:"momentum_20d"`
	// TrendPersistence is the fraction of the last 60 sessions that closed
	// above the 20-day moving average — smooth uptrends score high, chop low.
	TrendPersistence float64 `json:"trend_persistence"`
	// ProximityToHigh is close / max(high, 60d): 1.0 means at the high.
	ProximityToHigh float64 `json:"proximity_to_high"`
	// VolumeRatio compares 20d average volume to 60d average volume; > 1
	// means participation is expanding.
	VolumeRatio float64 `json:"volume_ratio"`
	// RSI14 is the classic Wilder RSI on the last close.
	RSI14 float64 `json:"rsi_14"`
	// ATR14 is the average true range in price units; ATRPct is it as a
	// fraction of the last close. This drives the stop distance.
	ATR14  float64 `json:"atr_14"`
	ATRPct float64 `json:"atr_pct"`
	// Turnover20 is the 20d average of close×volume, a liquidity floor.
	Turnover20 float64 `json:"turnover_20d"`
	LastClose  float64 `json:"last_close"`
}

// minBars is the fewest daily bars a symbol needs before it is scoreable:
// 60 sessions of history plus a seed for the 14-period indicators.
const minBars = 65

// computeFeatures measures one symbol on the window ending at index end
// (inclusive). Returns ok=false when there is not enough history.
func computeFeatures(b bars, end int) (Features, bool) {
	if end+1 < minBars || end >= b.len() {
		return Features{}, false
	}

	last := b.close[end]
	if last <= 0 {
		return Features{}, false
	}

	f := Features{LastClose: last}

	// Momentum over 60 and 20 trading days.
	if p := b.close[end-60]; p > 0 {
		f.Momentum60 = last/p - 1
	}
	if p := b.close[end-20]; p > 0 {
		f.Momentum20 = last/p - 1
	}

	// Trend persistence: share of the last 60 closes above their SMA20.
	above := 0
	for i := end - 59; i <= end; i++ {
		if b.close[i] > sma(b.close, i, 20) {
			above++
		}
	}
	f.TrendPersistence = float64(above) / 60

	// Proximity to the 60d high.
	hi := 0.0
	for i := end - 59; i <= end; i++ {
		hi = math.Max(hi, b.high[i])
	}
	if hi > 0 {
		f.ProximityToHigh = last / hi
	}

	// Volume expansion and liquidity.
	vol20 := mean(b.volume, end, 20)
	vol60 := mean(b.volume, end, 60)
	if vol60 > 0 {
		f.VolumeRatio = vol20 / vol60
	}
	turnover := 0.0
	for i := end - 19; i <= end; i++ {
		turnover += b.close[i] * b.volume[i]
	}
	f.Turnover20 = turnover / 20

	f.RSI14 = rsi(b.close, end, 14)
	f.ATR14 = atr(b, end, 14)
	f.ATRPct = f.ATR14 / last

	return f, true
}

// ---------------------------------------------------------------------------
// Indicators
// ---------------------------------------------------------------------------

func sma(xs []float64, end, n int) float64 {
	return mean(xs, end, n)
}

func mean(xs []float64, end, n int) float64 {
	if n <= 0 || end+1 < n {
		return 0
	}
	sum := 0.0
	for i := end - n + 1; i <= end; i++ {
		sum += xs[i]
	}
	return sum / float64(n)
}

// rsi is Wilder's RSI: smoothed average gain vs loss.
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

// atr is the average true range over the trailing period.
func atr(b bars, end, period int) float64 {
	if end < period {
		return 0
	}
	sum := 0.0
	for i := end - period + 1; i <= end; i++ {
		tr := b.high[i] - b.low[i]
		tr = math.Max(tr, math.Abs(b.high[i]-b.close[i-1]))
		tr = math.Max(tr, math.Abs(b.low[i]-b.close[i-1]))
		sum += tr
	}
	return sum / float64(period)
}

// ---------------------------------------------------------------------------
// Cross-sectional statistics
// ---------------------------------------------------------------------------

// zScores standardises one feature across the universe. A tiny stddev (all
// stocks identical) collapses to zeros rather than exploding.
func zScores(values []float64) []float64 {
	n := len(values)
	if n == 0 {
		return nil
	}
	m := 0.0
	for _, v := range values {
		m += v
	}
	m /= float64(n)

	varSum := 0.0
	for _, v := range values {
		varSum += (v - m) * (v - m)
	}
	sd := math.Sqrt(varSum / float64(n))

	out := make([]float64, n)
	if sd < 1e-12 {
		return out
	}
	for i, v := range values {
		z := (v - m) / sd
		// Winsorise: a single 300% mover shouldn't own the whole scale.
		out[i] = math.Max(-3, math.Min(3, z))
	}
	return out
}

// spearmanIC is the rank correlation between scores and forward returns —
// the standard "did the ranking mean anything" statistic.
func spearmanIC(scores, fwd []float64) float64 {
	if len(scores) != len(fwd) || len(scores) < 3 {
		return 0
	}
	rs := ranks(scores)
	rf := ranks(fwd)

	n := float64(len(scores))
	var sum float64
	for i := range rs {
		d := rs[i] - rf[i]
		sum += d * d
	}
	return 1 - 6*sum/(n*(n*n-1))
}

func ranks(xs []float64) []float64 {
	type iv struct {
		i int
		v float64
	}
	sorted := make([]iv, len(xs))
	for i, v := range xs {
		sorted[i] = iv{i, v}
	}
	// Insertion sort keeps this dependency-free; universes are a few hundred.
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].v < sorted[j-1].v; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	out := make([]float64, len(xs))
	for rank, e := range sorted {
		out[e.i] = float64(rank)
	}
	return out
}
