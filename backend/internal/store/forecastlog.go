package store

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// ForecastRecord is one directional call awaiting (or holding) its verdict.
type ForecastRecord struct {
	ID            int64
	Symbol        string
	Horizon       string
	Direction     string
	ProbabilityUp float64
	PriceAt       decimal.Decimal
	CreatedAt     time.Time
	MaturesAt     time.Time
}

// RecordForecast files a directional call for later scoring. To keep the
// sample honest it refuses duplicates: one live record per symbol+horizon at
// a time, so refreshing the tab cannot stuff the ledger with copies.
func (s *Store) RecordForecast(ctx context.Context, r ForecastRecord) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO forecast_log (symbol, horizon, direction, probability_up, price_at, matures_at)
		SELECT $1, $2, $3, $4, $5, $6
		WHERE NOT EXISTS (
			SELECT 1 FROM forecast_log
			WHERE symbol = $1 AND horizon = $2 AND resolved_at IS NULL
		)`,
		r.Symbol, r.Horizon, r.Direction, r.ProbabilityUp, r.PriceAt, r.MaturesAt)
	return err
}

// DueForecasts lists unresolved records whose horizon has matured.
func (s *Store) DueForecasts(ctx context.Context, limit int) ([]ForecastRecord, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, symbol, horizon, direction, probability_up, price_at, created_at, matures_at
		FROM forecast_log
		WHERE resolved_at IS NULL AND matures_at <= now()
		ORDER BY matures_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ForecastRecord
	for rows.Next() {
		var r ForecastRecord
		if err := rows.Scan(&r.ID, &r.Symbol, &r.Horizon, &r.Direction, &r.ProbabilityUp,
			&r.PriceAt, &r.CreatedAt, &r.MaturesAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResolveForecast scores one record against the price at maturity.
func (s *Store) ResolveForecast(ctx context.Context, id int64, priceAfter decimal.Decimal, correct bool) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE forecast_log
		SET resolved_at = now(), price_after = $2, correct = $3
		WHERE id = $1 AND resolved_at IS NULL`, id, priceAfter, correct)
	return err
}

// HorizonAccuracy is the measured track record for one horizon.
type HorizonAccuracy struct {
	Horizon string  `json:"horizon"`
	N       int     `json:"n"`
	Hits    int     `json:"hits"`
	HitRate float64 `json:"hit_rate"`
	// Brier is the mean squared error of the stated probability, 0 (perfect)
	// to 1; an uninformative 50% forecaster scores 0.25.
	Brier float64 `json:"brier"`
}

// ForecastAccuracy aggregates the resolved ledger per horizon.
func (s *Store) ForecastAccuracy(ctx context.Context) ([]HorizonAccuracy, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT horizon,
		       COUNT(*),
		       COUNT(*) FILTER (WHERE correct),
		       AVG(POWER(probability_up / 100.0 -
		           (CASE WHEN (direction = 'up') = correct THEN 1.0 ELSE 0.0 END), 2))
		FROM forecast_log
		WHERE resolved_at IS NOT NULL
		GROUP BY horizon`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HorizonAccuracy
	for rows.Next() {
		var h HorizonAccuracy
		if err := rows.Scan(&h.Horizon, &h.N, &h.Hits, &h.Brier); err != nil {
			return nil, err
		}
		if h.N > 0 {
			h.HitRate = float64(h.Hits) / float64(h.N)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
