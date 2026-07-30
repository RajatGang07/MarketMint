package forecast

import (
	"testing"
	"time"
)

func ist(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, istZone)
}

func TestMaturity(t *testing.T) {
	openTime := ist(2026, 7, 29, 11, 0) // Wednesday, mid-session

	t.Run("intraday is +15m", func(t *testing.T) {
		m, ok := maturity(HorizonIntra, openTime)
		if !ok || !m.Equal(ist(2026, 7, 29, 11, 15)) {
			t.Fatalf("got %v ok=%v", m, ok)
		}
	})
	t.Run("intraday caps at close", func(t *testing.T) {
		m, ok := maturity(HorizonIntra, ist(2026, 7, 29, 15, 25))
		if !ok || !m.Equal(ist(2026, 7, 29, 15, 30)) {
			t.Fatalf("got %v ok=%v", m, ok)
		}
	})
	t.Run("intraday off-session is not scored", func(t *testing.T) {
		if _, ok := maturity(HorizonIntra, ist(2026, 7, 29, 20, 0)); ok {
			t.Fatal("closed market must not file intraday records")
		}
	})
	t.Run("close matures at 15:30", func(t *testing.T) {
		m, ok := maturity(HorizonClose, openTime)
		if !ok || !m.Equal(ist(2026, 7, 29, 15, 30)) {
			t.Fatalf("got %v ok=%v", m, ok)
		}
	})
	t.Run("next day skips the weekend", func(t *testing.T) {
		m, ok := maturity(HorizonNextDay, ist(2026, 7, 31, 11, 0)) // Friday
		if !ok || !m.Equal(ist(2026, 8, 3, 15, 30)) {              // Monday close
			t.Fatalf("got %v ok=%v", m, ok)
		}
	})
	t.Run("seconds is never scored", func(t *testing.T) {
		if _, ok := maturity(HorizonSeconds, openTime); ok {
			t.Fatal("seconds horizon must never be scored")
		}
	})
}
