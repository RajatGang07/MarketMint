package forecast

import (
	"testing"
	"time"
)

func ist(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, istZone)
}

// The default calendar plus one movable holiday for the tests to trip over.
var testCal = newCalendar([]string{"2026-08-04"})

func TestMaturity(t *testing.T) {
	openTime := ist(2026, 7, 29, 11, 0) // Wednesday, mid-session

	t.Run("intraday is +15m", func(t *testing.T) {
		m, ok := maturity(HorizonIntra, openTime, testCal)
		if !ok || !m.Equal(ist(2026, 7, 29, 11, 15)) {
			t.Fatalf("got %v ok=%v", m, ok)
		}
	})
	t.Run("intraday caps at close", func(t *testing.T) {
		m, ok := maturity(HorizonIntra, ist(2026, 7, 29, 15, 25), testCal)
		if !ok || !m.Equal(ist(2026, 7, 29, 15, 30)) {
			t.Fatalf("got %v ok=%v", m, ok)
		}
	})
	t.Run("intraday off-session is not scored", func(t *testing.T) {
		if _, ok := maturity(HorizonIntra, ist(2026, 7, 29, 20, 0), testCal); ok {
			t.Fatal("closed market must not file intraday records")
		}
	})
	t.Run("close matures at 15:30", func(t *testing.T) {
		m, ok := maturity(HorizonClose, openTime, testCal)
		if !ok || !m.Equal(ist(2026, 7, 29, 15, 30)) {
			t.Fatalf("got %v ok=%v", m, ok)
		}
	})
	t.Run("next day skips the weekend", func(t *testing.T) {
		m, ok := maturity(HorizonNextDay, ist(2026, 7, 31, 11, 0), testCal) // Friday
		if !ok || !m.Equal(ist(2026, 8, 3, 15, 30)) {              // Monday close
			t.Fatalf("got %v ok=%v", m, ok)
		}
	})
	t.Run("seconds is never scored", func(t *testing.T) {
		if _, ok := maturity(HorizonSeconds, openTime, testCal); ok {
			t.Fatal("seconds horizon must never be scored")
		}
	})
}

func TestMaturitySkipsHolidays(t *testing.T) {
	// Checked Monday 03 Aug evening (market closed); Tuesday 04 Aug is a
	// configured holiday — the next-session call must mature Wednesday 15:30.
	m, ok := maturity(HorizonNextDay, ist(2026, 8, 3, 18, 0), testCal)
	if !ok || !m.Equal(ist(2026, 8, 5, 15, 30)) {
		t.Fatalf("want Wed 05 Aug close, got %v ok=%v", m, ok)
	}
}

func TestCalendarSessions(t *testing.T) {
	t.Run("upcoming session after close is next day", func(t *testing.T) {
		day, _ := testCal.upcomingSession(ist(2026, 7, 29, 16, 0)) // Wed after close
		if sessionLabel(day) != "Thu, 30 Jul" {
			t.Fatalf("got %s", sessionLabel(day))
		}
	})
	t.Run("upcoming session pre-open is today", func(t *testing.T) {
		day, _ := testCal.upcomingSession(ist(2026, 7, 29, 8, 0))
		if sessionLabel(day) != "Wed, 29 Jul" {
			t.Fatalf("got %s", sessionLabel(day))
		}
	})
	t.Run("saturday points to monday", func(t *testing.T) {
		day, _ := testCal.upcomingSession(ist(2026, 8, 1, 12, 0))
		if sessionLabel(day) != "Mon, 03 Aug" {
			t.Fatalf("got %s", sessionLabel(day))
		}
	})
	t.Run("holiday and independence day both skipped", func(t *testing.T) {
		// Fri 14 Aug evening → Sat 15 (Independence Day) → weekend → Mon 17.
		day, _ := testCal.upcomingSession(ist(2026, 8, 14, 17, 0))
		if sessionLabel(day) != "Mon, 17 Aug" {
			t.Fatalf("got %s", sessionLabel(day))
		}
	})
	t.Run("configured movable holiday closes the session", func(t *testing.T) {
		if testCal.sessionOpen(ist(2026, 8, 4, 11, 0)) {
			t.Fatal("2026-08-04 is configured as a holiday; session must be closed")
		}
		if testCal.closedReason(ist(2026, 8, 4, 11, 0)) != "exchange holiday" {
			t.Fatalf("got %q", testCal.closedReason(ist(2026, 8, 4, 11, 0)))
		}
	})
	t.Run("republic day is always closed", func(t *testing.T) {
		if newCalendar(nil).isTradingDay(ist(2027, 1, 26, 11, 0)) {
			t.Fatal("26 Jan must never be a trading day")
		}
	})
}

func TestStaleTape(t *testing.T) {
	now := ist(2026, 7, 29, 11, 0)
	fresh := []bar{{Time: ist(2026, 7, 29, 10, 55)}}
	old := []bar{{Time: ist(2026, 7, 28, 15, 25)}}
	if staleTape(fresh, now) {
		t.Fatal("today's bars present — tape is fresh")
	}
	if !staleTape(old, now) {
		t.Fatal("no bar today at 11:00 — unlisted holiday, tape is stale")
	}
	if staleTape(old, ist(2026, 7, 29, 9, 20)) {
		t.Fatal("within the morning grace window, stale must not trigger")
	}
}
