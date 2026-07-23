// Package intraday scans the F&O universe for opening-range-breakout (ORB)
// long setups on 5-minute bars, and backtests the identical rule over the
// last ~20 sessions so the user can see the strategy's real hit rate before
// risking anything.
//
// The rule, in full (long-only, one trade per symbol per day):
//
//	Opening range = the first 15 minutes (three 5m bars, 09:15–09:30).
//	ENTRY   when a 5m bar CLOSES above the OR high, before the entry cutoff
//	        (14:30), with volume ≥ 1.5× the session's average so far, and the
//	        close above VWAP.
//	STOP    the higher of (OR low, entry − 1.5×ATR) — structural if it is
//	        tight enough, volatility-based otherwise. Risk is floored at 0.35%
//	        of entry so tick noise cannot produce silly position sizes.
//	TARGET  entry + 2R (R = entry − stop).
//	TRAIL   the stop ratchets up to (session high − 1R): after price runs 1R
//	        the position cannot lose, and a 2R move locks at least +1R.
//	EXIT    stop / target / trailing stop — or the 15:15 square-off,
//	        whichever comes first. Nothing is held overnight.
package intraday

import (
	"math"
	"sort"
	"time"

	"github.com/gangrajat/groww-paper-trading/backend/internal/marketdata"
)

const (
	orBars       = 3   // 09:15–09:30 on 5m bars
	entryCutoff  = 63  // bar index of ~14:30 (63 bars past 09:15)
	squareOffBar = 72  // ~15:15
	volMultiple  = 1.5 // breakout volume vs session average
	atrMultiple  = 1.5 // volatility stop distance
	targetR      = 2.0 // reward multiple
	minRiskPct   = 0.0035
	sessionOpen  = 9*60 + 15
	sessionClose = 15*60 + 30
)

// Bar is one 5-minute candle in float form.
type Bar struct {
	Time   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

// Day is one session's bars, oldest first.
type Day struct {
	Date string
	Bars []Bar
}

// istZone matches the exchange clock.
var istZone = time.FixedZone("IST", 5*3600+1800)

// SplitDays groups candles into NSE sessions, dropping anything outside
// 09:15–15:30 IST.
func SplitDays(candles []marketdata.Candle) []Day {
	byDate := map[string][]Bar{}
	var order []string

	for _, c := range candles {
		t := c.Time.In(istZone)
		mins := t.Hour()*60 + t.Minute()
		if mins < sessionOpen || mins >= sessionClose {
			continue
		}
		close, _ := c.Close.Float64()
		if close <= 0 {
			continue
		}
		open, _ := c.Open.Float64()
		high, _ := c.High.Float64()
		low, _ := c.Low.Float64()
		vol, _ := c.Volume.Float64()

		key := t.Format("2006-01-02")
		if _, seen := byDate[key]; !seen {
			order = append(order, key)
		}
		byDate[key] = append(byDate[key], Bar{Time: t, Open: open, High: high, Low: low, Close: close, Volume: vol})
	}

	out := make([]Day, 0, len(order))
	for _, key := range order {
		bars := byDate[key]
		sort.Slice(bars, func(i, j int) bool { return bars[i].Time.Before(bars[j].Time) })
		out = append(out, Day{Date: key, Bars: bars})
	}
	return out
}

// Signal is a triggered (or completed) ORB trade on one session.
type Signal struct {
	Triggered bool
	BarIndex  int
	EntryTime time.Time
	Entry     float64
	Stop      float64
	Target    float64
	TrailBy   float64 // = initial risk (1R)
	Risk      float64 // per share
	ORHigh    float64
	ORLow     float64
	VWAP      float64 // at entry
	RVOL      float64 // breakout bar volume vs session average before it
	// Outcome of walking the rest of the session (also filled live, so a
	// signal that already stopped out today reports it).
	Exited    bool
	ExitTime  time.Time
	Exit      float64
	ExitKind  string  // TARGET / TRAIL / STOP / SQUARE_OFF
	ResultR   float64 // (exit-entry)/risk
	HighSince float64 // best price since entry (drives the live trail level)
}

// Detect runs the ORB rule over one session's bars.
func Detect(day Day) (Signal, bool) {
	bars := day.Bars
	if len(bars) <= orBars {
		return Signal{}, false
	}

	orHigh, orLow := bars[0].High, bars[0].Low
	for _, b := range bars[1:orBars] {
		orHigh = math.Max(orHigh, b.High)
		orLow = math.Min(orLow, b.Low)
	}

	// Cumulative VWAP and volume as the session unfolds.
	var cumPV, cumV float64
	vwapAt := make([]float64, len(bars))
	for i, b := range bars {
		typical := (b.High + b.Low + b.Close) / 3
		cumPV += typical * b.Volume
		cumV += b.Volume
		if cumV > 0 {
			vwapAt[i] = cumPV / cumV
		} else {
			vwapAt[i] = b.Close
		}
	}

	limit := min(len(bars), entryCutoff)
	for i := orBars; i < limit; i++ {
		b := bars[i]
		if b.Close <= orHigh || b.Close < vwapAt[i] {
			continue
		}
		avgVol := meanVolume(bars[:i])
		if avgVol <= 0 || b.Volume < volMultiple*avgVol {
			continue
		}

		entry := b.Close
		stop := math.Max(orLow, entry-atrMultiple*atr5m(bars[:i+1]))
		risk := entry - stop
		if floor := entry * minRiskPct; risk < floor {
			risk = floor
			stop = entry - risk
		}
		if risk <= 0 {
			continue
		}

		sig := Signal{
			Triggered: true,
			BarIndex:  i,
			EntryTime: b.Time,
			Entry:     round2(entry),
			Stop:      roundTick(stop),
			Target:    roundTick(entry + targetR*risk),
			TrailBy:   roundTick(risk),
			Risk:      round2(risk),
			ORHigh:    round2(orHigh),
			ORLow:     round2(orLow),
			VWAP:      round2(vwapAt[i]),
			RVOL:      round2(b.Volume / avgVol),
		}
		walkForward(&sig, bars)
		return sig, true
	}
	return Signal{}, false
}

// walkForward replays the exits over the remainder of the session: trailing
// stop first within a bar (conservative), then target, then square-off.
func walkForward(sig *Signal, bars []Bar) {
	stop := sig.Stop
	high := sig.Entry
	sig.HighSince = sig.Entry

	for j := sig.BarIndex + 1; j < len(bars); j++ {
		b := bars[j]

		// Square-off bar: out at its close, no questions asked.
		if j >= squareOffBar {
			sig.exit(b.Time, b.Close, "SQUARE_OFF")
			return
		}

		// Conservative intrabar ordering: if the low breaches the stop, the
		// stop is assumed to fill even if the high would also have hit the
		// target. A gap through the stop fills at the open, like a real
		// stop-market.
		if b.Low <= stop {
			px := math.Min(stop, b.Open)
			kind := "STOP"
			if stop > sig.Stop {
				kind = "TRAIL"
			}
			sig.exit(b.Time, px, kind)
			return
		}
		if b.High >= sig.Target {
			sig.exit(b.Time, sig.Target, "TARGET")
			return
		}

		if b.High > high {
			high = b.High
			sig.HighSince = round2(high)
			stop = math.Max(stop, high-sig.TrailBy)
		}
	}

	// Session data ran out with the trade still open (only possible on the
	// live, still-running day). Report the current trail level via Stop.
	sig.Stop = roundTick(stop)
}

func (s *Signal) exit(t time.Time, px float64, kind string) {
	s.Exited = true
	s.ExitTime = t
	s.Exit = round2(px)
	s.ExitKind = kind
	if s.Risk > 0 {
		s.ResultR = round2((s.Exit - s.Entry) / s.Risk)
	}
}

// ---------------------------------------------------------------------------
// Backtest
// ---------------------------------------------------------------------------

// Stats aggregates simulated trades.
type Stats struct {
	Trades       int     `json:"trades"`
	Wins         int     `json:"wins"`
	WinRate      float64 `json:"win_rate"`
	AvgR         float64 `json:"avg_r"`
	AvgWinR      float64 `json:"avg_win_r"`
	AvgLossR     float64 `json:"avg_loss_r"`
	ProfitFactor float64 `json:"profit_factor"`
	BestR        float64 `json:"best_r"`
	WorstR       float64 `json:"worst_r"`
}

// Backtest replays Detect over completed sessions (excluding excludeDate,
// normally the live day).
func Backtest(days []Day, excludeDate string) (Stats, []float64) {
	var rs []float64
	for _, day := range days {
		if day.Date == excludeDate {
			continue
		}
		// Only complete-ish sessions: a holiday half-day would distort exits.
		if len(day.Bars) < squareOffBar/2 {
			continue
		}
		if sig, ok := Detect(day); ok && sig.Exited {
			rs = append(rs, sig.ResultR)
		}
	}
	return statsOf(rs), rs
}

func statsOf(rs []float64) Stats {
	st := Stats{Trades: len(rs)}
	if len(rs) == 0 {
		return st
	}
	var sum, winSum, lossSum float64
	st.BestR, st.WorstR = rs[0], rs[0]
	for _, r := range rs {
		sum += r
		if r > 0 {
			st.Wins++
			winSum += r
		} else {
			lossSum += r
		}
		st.BestR = math.Max(st.BestR, r)
		st.WorstR = math.Min(st.WorstR, r)
	}
	st.WinRate = round2(float64(st.Wins) / float64(len(rs)))
	st.AvgR = round2(sum / float64(len(rs)))
	if st.Wins > 0 {
		st.AvgWinR = round2(winSum / float64(st.Wins))
	}
	if losses := len(rs) - st.Wins; losses > 0 {
		st.AvgLossR = round2(lossSum / float64(losses))
	}
	if lossSum != 0 {
		st.ProfitFactor = round2(winSum / -lossSum)
	}
	st.BestR = round2(st.BestR)
	st.WorstR = round2(st.WorstR)
	return st
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func meanVolume(bars []Bar) float64 {
	if len(bars) == 0 {
		return 0
	}
	var sum float64
	for _, b := range bars {
		sum += b.Volume
	}
	return sum / float64(len(bars))
}

// atr5m is a simple true-range average over up to the last 14 bars.
func atr5m(bars []Bar) float64 {
	n := len(bars)
	if n < 2 {
		return 0
	}
	start := n - 14
	if start < 1 {
		start = 1
	}
	var sum float64
	var count int
	for i := start; i < n; i++ {
		tr := bars[i].High - bars[i].Low
		tr = math.Max(tr, math.Abs(bars[i].High-bars[i-1].Close))
		tr = math.Max(tr, math.Abs(bars[i].Low-bars[i-1].Close))
		sum += tr
		count++
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

func round2(v float64) float64    { return math.Round(v*100) / 100 }
func roundTick(v float64) float64 { return math.Round(v*20) / 20 }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
