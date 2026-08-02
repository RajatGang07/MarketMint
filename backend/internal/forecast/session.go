package forecast

import (
	"fmt"
	"strings"
	"time"
)

// calendar answers one question honestly: when does the market actually
// trade? Weekends and fixed national holidays are built in; movable festival
// holidays (Holi, Diwali, Eid…) shift every year, so they come from the
// NSE_HOLIDAYS env list rather than a hardcoded guess that would rot.
type calendar struct {
	// extra holds YYYY-MM-DD dates (IST) beyond weekends and fixed holidays.
	extra map[string]bool
}

func newCalendar(extraHolidays []string) calendar {
	c := calendar{extra: make(map[string]bool, len(extraHolidays))}
	for _, d := range extraHolidays {
		if d = strings.TrimSpace(d); d != "" {
			c.extra[d] = true
		}
	}
	return c
}

// fixedHoliday reports the NSE holidays that fall on the same date every
// year. Only the certain ones are listed — a wrong holiday claim on a
// trading day would be worse than a missed one.
func fixedHoliday(t time.Time) bool {
	switch {
	case t.Month() == time.January && t.Day() == 26: // Republic Day
		return true
	case t.Month() == time.April && t.Day() == 14: // Ambedkar Jayanti
		return true
	case t.Month() == time.May && t.Day() == 1: // Maharashtra Day
		return true
	case t.Month() == time.August && t.Day() == 15: // Independence Day
		return true
	case t.Month() == time.October && t.Day() == 2: // Gandhi Jayanti
		return true
	case t.Month() == time.December && t.Day() == 25: // Christmas
		return true
	}
	return false
}

// isTradingDay is true for IST weekdays that are not holidays.
func (c calendar) isTradingDay(t time.Time) bool {
	ist := t.In(istZone)
	if wd := ist.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	if fixedHoliday(ist) {
		return false
	}
	return !c.extra[ist.Format("2006-01-02")]
}

// sessionOpen reports whether the NSE cash session is trading right now.
func (c calendar) sessionOpen(now time.Time) bool {
	if !c.isTradingDay(now) {
		return false
	}
	t := now.In(istZone)
	mins := t.Hour()*60 + t.Minute()
	return mins >= 9*60+15 && mins < 15*60+30
}

// upcomingSession returns the next session's date: today when the session is
// still ahead (pre-open or in progress on a trading day), otherwise the next
// trading day. The second value is that session's 15:30 IST close.
func (c calendar) upcomingSession(now time.Time) (time.Time, time.Time) {
	ist := now.In(istZone)
	day := time.Date(ist.Year(), ist.Month(), ist.Day(), 0, 0, 0, 0, istZone)

	beforeClose := ist.Hour()*60+ist.Minute() < 15*60+30
	if !(c.isTradingDay(day) && beforeClose) {
		day = c.nextTradingDay(day)
	}
	return day, day.Add(15*time.Hour + 30*time.Minute)
}

// nextTradingDay is the first trading day strictly after the given date.
func (c calendar) nextTradingDay(after time.Time) time.Time {
	d := after.In(istZone)
	d = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, istZone)
	for i := 0; i < 30; i++ { // an exchange never closes a month straight
		d = d.AddDate(0, 0, 1)
		if c.isTradingDay(d) {
			return d
		}
	}
	return d
}

// sessionLabel names a session date the way a person would: "Mon, 03 Aug".
func sessionLabel(d time.Time) string {
	return d.In(istZone).Format("Mon, 02 Jan")
}

// closedReason says WHY the market is closed right now, for honest UI copy.
func (c calendar) closedReason(now time.Time) string {
	ist := now.In(istZone)
	switch {
	case ist.Weekday() == time.Saturday || ist.Weekday() == time.Sunday:
		return "weekend"
	case fixedHoliday(ist) || c.extra[ist.Format("2006-01-02")]:
		return "exchange holiday"
	case ist.Hour()*60+ist.Minute() >= 15*60+30:
		return "after market close (15:30 IST)"
	default:
		return "before market open (09:15 IST)"
	}
}

// describeNextOpen is the sentence the closed-market UI leads with.
func (c calendar) describeNextOpen(now time.Time) string {
	day, _ := c.upcomingSession(now)
	return fmt.Sprintf("Market closed — %s. Next session: %s, 09:15–15:30 IST.",
		c.closedReason(now), sessionLabel(day))
}
