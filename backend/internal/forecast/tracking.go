package forecast

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/gangrajat/groww-paper-trading/backend/internal/store"
)

// Forecast accuracy is measured, not asserted: every directional lean the tab
// hands out is filed with the price at issue time, and a background resolver
// scores it against the price once the horizon matures. The Forecast tab then
// shows the real hit rate per horizon — including when it embarrasses us.

// maturity computes when a horizon's clock runs out, on the IST session
// calendar (weekends AND holidays skipped). ok=false means the horizon has no
// meaningful expiry right now (e.g. intraday leans while the market is closed).
func maturity(h Horizon, now time.Time, cal calendar) (time.Time, bool) {
	ist := now.In(istZone)
	closeToday := time.Date(ist.Year(), ist.Month(), ist.Day(), 15, 30, 0, 0, istZone)

	switch h {
	case HorizonIntra:
		if !cal.sessionOpen(now) {
			return time.Time{}, false
		}
		m := ist.Add(15 * time.Minute)
		if m.After(closeToday) {
			m = closeToday
		}
		return m, true
	case HorizonClose:
		if !cal.sessionOpen(now) {
			return time.Time{}, false
		}
		return closeToday, true
	case HorizonNextDay:
		// The lean targets the upcoming session's close: today's close while
		// the session is still trading would be the "close" horizon, so an
		// open market points at the NEXT trading day; a closed one points at
		// whichever session comes next (Monday after a Friday-evening check,
		// the day after a holiday, and so on).
		if cal.sessionOpen(now) {
			d := cal.nextTradingDay(now)
			return d.Add(15*time.Hour + 30*time.Minute), true
		}
		_, close := cal.upcomingSession(now)
		return close, true
	default:
		return time.Time{}, false // seconds: labelled unpredictable, never scored
	}
}

// record files every directional lean for later scoring. Best-effort: a full
// ledger is worthless if it can break the forecast that feeds it.
func (e *Engine) record(ctx context.Context, res Result) {
	if e.store == nil {
		return
	}
	for _, lean := range res.Leans {
		if lean.Direction == "flat" || lean.Horizon == HorizonSeconds {
			continue // no directional call to hold accountable
		}
		matures, ok := maturity(lean.Horizon, res.AsOf, e.cal)
		if !ok {
			continue
		}
		err := e.store.RecordForecast(ctx, store.ForecastRecord{
			Symbol:        res.Symbol,
			Horizon:       string(lean.Horizon),
			Direction:     lean.Direction,
			ProbabilityUp: lean.ProbabilityUp,
			PriceAt:       decimal.NewFromFloat(res.LastPrice),
			MaturesAt:     matures,
		})
		if err != nil && ctx.Err() == nil {
			e.log.Warn("forecast: recording lean failed", "symbol", res.Symbol, "err", err)
		}
	}
}

// RunResolver scores matured forecasts until ctx ends. Records that mature
// while the server is down are scored on the next tick — a slightly later
// price, at worst the same session's close.
func (e *Engine) RunResolver(ctx context.Context, every time.Duration) {
	if e.store == nil {
		return
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.resolveDue(ctx)
		}
	}
}

func (e *Engine) resolveDue(ctx context.Context) {
	due, err := e.store.DueForecasts(ctx, 100)
	if err != nil {
		if ctx.Err() == nil {
			e.log.Warn("forecast resolver: listing due records failed", "err", err)
		}
		return
	}
	for _, rec := range due {
		ltp, err := e.market.LTP(ctx, "NSE", "CASH", rec.Symbol)
		if err != nil || !ltp.IsPositive() {
			continue // provider hiccup: leave unresolved for the next tick
		}
		wentUp := ltp.GreaterThan(rec.PriceAt)
		correct := (rec.Direction == "up") == wentUp
		if err := e.store.ResolveForecast(ctx, rec.ID, ltp, correct); err != nil && ctx.Err() == nil {
			e.log.Warn("forecast resolver: resolve failed", "id", rec.ID, "err", err)
		}
	}
	if len(due) > 0 {
		e.log.Info("forecast resolver scored records", "count", len(due))
	}
}
