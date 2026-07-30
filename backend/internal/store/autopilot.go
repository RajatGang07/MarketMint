package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// AutopilotSettings is one account's automation policy. Defaults are
// conservative: disabled, five concurrent positions, ₹2L per trade.
type AutopilotSettings struct {
	AccountID          int64           `json:"-"`
	Enabled            bool            `json:"enabled"`
	MaxPositions       int             `json:"max_positions"`
	MaxCapitalPerTrade decimal.Decimal `json:"max_capital_per_trade"`
	TrailStops         bool            `json:"trail_stops"`
	// ExitStyle is "trail" (ride the trend on a trailing stop alone) or
	// "bracket" (also cap the win at the plan's fixed target).
	ExitStyle string    `json:"exit_style"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DefaultAutopilotSettings is what an account gets before it ever saves.
func DefaultAutopilotSettings(accountID int64) AutopilotSettings {
	return AutopilotSettings{
		AccountID:          accountID,
		MaxPositions:       5,
		MaxCapitalPerTrade: decimal.NewFromInt(200_000),
		TrailStops:         true,
		ExitStyle:          "trail",
	}
}

// AutopilotSettingsFor returns the saved policy, or defaults when none exists.
func (s *Store) AutopilotSettingsFor(ctx context.Context, accountID int64) (AutopilotSettings, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT account_id, enabled, max_positions, max_capital_per_trade, trail_stops, exit_style, updated_at
		FROM autopilot_settings WHERE account_id = $1`, accountID)
	var a AutopilotSettings
	err := row.Scan(&a.AccountID, &a.Enabled, &a.MaxPositions, &a.MaxCapitalPerTrade, &a.TrailStops, &a.ExitStyle, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultAutopilotSettings(accountID), nil
	}
	return a, err
}

// SaveAutopilotSettings upserts the policy.
func (s *Store) SaveAutopilotSettings(ctx context.Context, a AutopilotSettings) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO autopilot_settings (account_id, enabled, max_positions, max_capital_per_trade, trail_stops, exit_style, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (account_id) DO UPDATE SET
			enabled = EXCLUDED.enabled,
			max_positions = EXCLUDED.max_positions,
			max_capital_per_trade = EXCLUDED.max_capital_per_trade,
			trail_stops = EXCLUDED.trail_stops,
			exit_style = EXCLUDED.exit_style,
			updated_at = now()`,
		a.AccountID, a.Enabled, a.MaxPositions, a.MaxCapitalPerTrade, a.TrailStops, a.ExitStyle)
	return err
}

// AutopilotEnabledAccounts lists the accounts the runner must serve.
func (s *Store) AutopilotEnabledAccounts(ctx context.Context) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `SELECT account_id FROM autopilot_settings WHERE enabled`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// AutopilotLogEntry is one audited decision.
type AutopilotLogEntry struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	Action   string    `json:"action"`
	Symbol   string    `json:"symbol,omitempty"`
	Detail   string    `json:"detail"`
	OrderRef string    `json:"order_ref,omitempty"`
}

// AppendAutopilotLog records one decision. Logging must never break a trading
// pass, so callers treat errors as warnings.
func (s *Store) AppendAutopilotLog(ctx context.Context, accountID int64, action, symbol, detail, orderRef string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO autopilot_log (account_id, action, symbol, detail, order_ref)
		VALUES ($1, $2, $3, $4, $5)`, accountID, action, symbol, detail, orderRef)
	return err
}

// ListAutopilotLog returns the newest entries first.
func (s *Store) ListAutopilotLog(ctx context.Context, accountID int64, limit int) ([]AutopilotLogEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, at, action, symbol, detail, order_ref
		FROM autopilot_log WHERE account_id = $1
		ORDER BY at DESC, id DESC LIMIT $2`, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutopilotLogEntry
	for rows.Next() {
		var e AutopilotLogEntry
		if err := rows.Scan(&e.ID, &e.At, &e.Action, &e.Symbol, &e.Detail, &e.OrderRef); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
