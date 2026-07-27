package forecast

import (
	"math"
	"testing"
	"time"
)

// syntheticDaily builds n daily bars trending by drift per bar.
func syntheticDaily(n int, start, drift float64) []bar {
	bars := make([]bar, n)
	price := start
	day := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range bars {
		open := price
		price += drift
		bars[i] = bar{
			Time: day.AddDate(0, 0, i), Open: open,
			High: math.Max(open, price) + 1, Low: math.Min(open, price) - 1,
			Close: price, Volume: 1000,
		}
	}
	return bars
}

func TestAnalyzeDailyUptrend(t *testing.T) {
	bars := syntheticDaily(120, 100, 0.5)
	last := bars[len(bars)-1].Close
	v := analyzeDaily(bars, last)

	if !v.ok {
		t.Fatal("expected ok with 120 bars")
	}
	if v.momentum20 <= 0 || v.momentum60 <= 0 {
		t.Errorf("uptrend should have positive momentum, got %v / %v", v.momentum20, v.momentum60)
	}
	if !v.aboveSMA20 || !v.aboveSMA50 {
		t.Error("uptrend close should sit above both MAs")
	}
	if v.trendPersistence < 0.9 {
		t.Errorf("steady uptrend persistence = %v, want >= 0.9", v.trendPersistence)
	}
}

func TestAnalyzeDailyTooShort(t *testing.T) {
	if v := analyzeDaily(syntheticDaily(30, 100, 0.5), 115); v.ok {
		t.Error("30 bars must not be enough")
	}
}

func TestSquashStaysHumble(t *testing.T) {
	for _, s := range []float64{-10, -1, 0, 1, 10} {
		p := squash(s)
		if p < 38 || p > 62 {
			t.Errorf("squash(%v) = %v, want within [38, 62]", s, p)
		}
	}
	if squash(0) != 50 {
		t.Errorf("squash(0) = %v, want 50", squash(0))
	}
}

func TestSecondsLeanIsHonest(t *testing.T) {
	l := secondsLean()
	if l.ProbabilityUp != 50 || l.Confidence != "none" {
		t.Errorf("seconds lean must be 50%% / none, got %v / %v", l.ProbabilityUp, l.Confidence)
	}
	if l.Note == "" {
		t.Error("seconds lean must explain why it cannot predict")
	}
}

func TestAnalyzeIntradayFlow(t *testing.T) {
	// Build a rising session today: closes near highs, volume on up bars.
	now := time.Now()
	ist := now.In(istZone)
	open := time.Date(ist.Year(), ist.Month(), ist.Day(), 9, 15, 0, 0, istZone)
	var bars []bar
	price := 100.0
	for i := 0; i < 12; i++ {
		o := price
		price += 0.5
		bars = append(bars, bar{
			Time: open.Add(time.Duration(i) * 5 * time.Minute),
			Open: o, High: price + 0.1, Low: o - 0.1, Close: price, Volume: 1000,
		})
	}
	v := analyzeIntraday(bars, open.Add(70*time.Minute))
	if !v.ok {
		t.Fatal("expected ok")
	}
	if v.vsVWAP <= 0 {
		t.Errorf("rising tape should trade above VWAP, got %v", v.vsVWAP)
	}
	if v.upVolShare != 1 {
		t.Errorf("all volume on up bars, got share %v", v.upVolShare)
	}
	if v.mom15 <= 0 {
		t.Errorf("15m momentum should be positive, got %v", v.mom15)
	}
}
