package intraday

import (
	"testing"
	"time"
)

// mkDay builds a synthetic session. Each spec is {open, high, low, close,
// volume}; bars start at 09:15 IST on five-minute steps.
func mkDay(specs ...[5]float64) Day {
	base := time.Date(2026, 7, 23, 9, 15, 0, 0, istZone)
	bars := make([]Bar, len(specs))
	for i, s := range specs {
		bars[i] = Bar{
			Time: base.Add(time.Duration(i) * 5 * time.Minute),
			Open: s[0], High: s[1], Low: s[2], Close: s[3], Volume: s[4],
		}
	}
	return Day{Date: "2026-07-23", Bars: bars}
}

// flat produces n identical quiet bars — used to pad sessions.
func flat(n int, px, vol float64) [][5]float64 {
	out := make([][5]float64, n)
	for i := range out {
		out[i] = [5]float64{px, px + 0.5, px - 0.5, px, vol}
	}
	return out
}

func TestDetectFindsAVolumeBackedBreakout(t *testing.T) {
	specs := [][5]float64{
		{100, 101, 99, 100.5, 1000}, // opening range: high 101.2
		{100.5, 101.2, 100, 101, 900},
		{101, 101.1, 100.4, 100.8, 800},
		{100.8, 101.0, 100.5, 100.9, 850}, // inside OR, no signal
		{101, 102.5, 100.9, 102.2, 2500},  // closes above 101.2 on 2.8x volume
	}
	sig, ok := Detect(mkDay(specs...))
	if !ok || !sig.Triggered {
		t.Fatal("expected a breakout signal")
	}
	if sig.BarIndex != 4 {
		t.Fatalf("breakout bar = %d, want 4", sig.BarIndex)
	}
	if sig.Entry != 102.2 {
		t.Fatalf("entry = %v, want 102.2 (breakout close)", sig.Entry)
	}
	if sig.Stop >= sig.Entry {
		t.Fatalf("stop %v must sit below entry %v", sig.Stop, sig.Entry)
	}
	if sig.Target <= sig.Entry {
		t.Fatalf("target %v must sit above entry %v", sig.Target, sig.Entry)
	}
	// 2R geometry: target distance = 2 × risk (tick rounding tolerance).
	risk := sig.Entry - sig.Stop
	if got := sig.Target - sig.Entry; got < 1.9*risk || got > 2.1*risk {
		t.Fatalf("target distance %v is not ~2R (risk %v)", got, risk)
	}
}

func TestDetectRejectsLowVolumeAndBelowVWAPBreaks(t *testing.T) {
	// Breakout close but volume equal to average — no signal.
	weak := [][5]float64{
		{100, 101, 99, 100.5, 1000},
		{100.5, 101.2, 100, 101, 1000},
		{101, 101.1, 100.4, 100.8, 1000},
		{101, 102.0, 100.9, 101.9, 1000}, // above OR high, but RVOL 1.0
	}
	if _, ok := Detect(mkDay(weak...)); ok {
		t.Fatal("a breakout without volume must not signal")
	}
}

func TestDetectHonoursTheEntryCutoff(t *testing.T) {
	// A perfect breakout, but on the first bar after 14:30 — too late to
	// build a fresh position before square-off.
	specs := flat(entryCutoff, 100, 1000)
	specs = append(specs, [5]float64{100, 104, 100, 103.5, 9000})
	if _, ok := Detect(mkDay(specs...)); ok {
		t.Fatal("entries after the 14:30 cutoff must be ignored")
	}
}

func TestWalkForwardHitsTargetCleanly(t *testing.T) {
	specs := [][5]float64{
		{100, 101, 99, 100.5, 1000},
		{100.5, 101.2, 100, 101, 900},
		{101, 101.1, 100.4, 100.8, 800},
		{101, 102.5, 100.9, 102.2, 2500}, // entry 102.2
		{102.2, 103, 102, 102.8, 1200},
		{102.8, 110, 102.5, 109, 3000}, // blows through any 2R target
	}
	sig, ok := Detect(mkDay(specs...))
	if !ok || !sig.Exited {
		t.Fatalf("expected a completed trade, got %+v", sig)
	}
	if sig.ExitKind != "TARGET" {
		t.Fatalf("exit = %s, want TARGET", sig.ExitKind)
	}
	if sig.Exit != sig.Target {
		t.Fatalf("target exit fills at the limit: got %v want %v", sig.Exit, sig.Target)
	}
	if sig.ResultR < 1.9 || sig.ResultR > 2.1 {
		t.Fatalf("target exit should be ~+2R, got %vR", sig.ResultR)
	}
}

func TestWalkForwardStopBeatsTargetInsideOneBar(t *testing.T) {
	// The bar after entry spans both the stop and the target; the simulation
	// must take the pessimistic exit.
	specs := [][5]float64{
		{100, 101, 99, 100.5, 1000},
		{100.5, 101.2, 100, 101, 900},
		{101, 101.1, 100.4, 100.8, 800},
		{101, 102.5, 100.9, 102.2, 2500}, // entry
		{102.2, 115, 90, 100, 5000},      // everything hit at once
	}
	sig, _ := Detect(mkDay(specs...))
	if sig.ExitKind != "STOP" {
		t.Fatalf("ambiguous bar must resolve to STOP, got %s", sig.ExitKind)
	}
	if sig.ResultR >= 0 {
		t.Fatalf("stop exit should book a loss, got %vR", sig.ResultR)
	}
}

func TestWalkForwardTrailLocksProfit(t *testing.T) {
	specs := [][5]float64{
		{100, 101, 99, 100.5, 1000},
		{100.5, 101.2, 100, 101, 900},
		{101, 101.1, 100.4, 100.8, 800},
		{101, 102.5, 100.9, 102.2, 2500}, // entry ~102.2, risk ~1.3-1.5
	}
	// Rally ~1.7R high, then a controlled fade that walks down through the
	// trailed stop without ever reaching the original stop or the target.
	specs = append(specs,
		[5]float64{102.2, 104.6, 102.1, 104.4, 1500},
		[5]float64{104.4, 104.7, 103.9, 104.0, 1000},
		[5]float64{104.0, 104.2, 103.4, 103.6, 1000},
		[5]float64{103.6, 103.7, 102.9, 103.1, 1000},
		[5]float64{103.1, 103.2, 102.4, 102.6, 1000},
		[5]float64{102.6, 102.7, 101.9, 102.1, 1000},
	)
	sig, _ := Detect(mkDay(specs...))
	if !sig.Exited {
		t.Fatalf("fade through the trail should have exited: %+v", sig)
	}
	if sig.ExitKind != "TRAIL" {
		t.Fatalf("exit = %s, want TRAIL", sig.ExitKind)
	}
	if sig.ResultR <= 0 {
		t.Fatalf("a 1R-trail after a ~1.7R run must exit in profit, got %vR", sig.ResultR)
	}
}

func TestWalkForwardSquaresOffAtSessionEnd(t *testing.T) {
	// Entry, then a drift that never touches stop or target until 15:15.
	specs := [][5]float64{
		{100, 101, 99, 100.5, 1000},
		{100.5, 101.2, 100, 101, 900},
		{101, 101.1, 100.4, 100.8, 800},
		{101, 102.5, 100.9, 102.2, 2500}, // entry
	}
	specs = append(specs, flat(squareOffBar+2-len(specs), 102.4, 1000)...)
	sig, _ := Detect(mkDay(specs...))
	if sig.ExitKind != "SQUARE_OFF" {
		t.Fatalf("exit = %s, want SQUARE_OFF", sig.ExitKind)
	}
	if !sig.Exited {
		t.Fatal("square-off must close the trade")
	}
}

func TestBacktestSkipsTheLiveDayAndAggregates(t *testing.T) {
	// A full-length session (the backtest skips half days) whose trade hits
	// the target on bar 4; the padding after that changes nothing.
	winnerSpecs := [][5]float64{
		{100, 101, 99, 100.5, 1000},
		{100.5, 101.2, 100, 101, 900},
		{101, 101.1, 100.4, 100.8, 800},
		{101, 102.5, 100.9, 102.2, 2500},
		{102.2, 110, 102, 109, 3000},
	}
	winnerSpecs = append(winnerSpecs, flat(squareOffBar-len(winnerSpecs), 109, 1000)...)
	winner := mkDay(winnerSpecs...)
	winner.Date = "2026-07-21"

	live := mkDay(flat(10, 100, 1000)...)
	live.Date = "2026-07-23"

	stats, rs := Backtest([]Day{winner, live}, "2026-07-23")
	if stats.Trades != 1 || len(rs) != 1 {
		t.Fatalf("expected exactly the completed historical trade, got %+v", stats)
	}
	if stats.WinRate != 1 || stats.AvgR < 1.9 {
		t.Fatalf("winner day should aggregate as a ~2R win: %+v", stats)
	}
}
