package store

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// ClosedTrade is one realised exit: a SELL with its booked P&L.
type ClosedTrade struct {
	Symbol      string
	RealizedPnL float64
	At          time.Time
}

// ClosedTrades lists every realising SELL, oldest first, for performance math.
func (s *Store) ClosedTrades(ctx context.Context, accountID int64) ([]ClosedTrade, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT trading_symbol, realized_pnl, created_at
		FROM trades
		WHERE account_id = $1 AND transaction_type = 'SELL'
		ORDER BY created_at, id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClosedTrade
	for rows.Next() {
		var t ClosedTrade
		var pnl decimal.Decimal
		if err := rows.Scan(&t.Symbol, &pnl, &t.At); err != nil {
			return nil, err
		}
		t.RealizedPnL, _ = pnl.Float64()
		out = append(out, t)
	}
	return out, rows.Err()
}

// Performance is the account's closed-trade report card.
type Performance struct {
	ClosedTrades int     `json:"closed_trades"`
	Wins         int     `json:"wins"`
	Losses       int     `json:"losses"`
	WinRate      float64 `json:"win_rate"`
	AvgWin       float64 `json:"avg_win"`
	AvgLoss      float64 `json:"avg_loss"` // reported as a positive number
	// Expectancy is the mean realised P&L per closed trade — the single
	// number that says whether the process makes money.
	Expectancy float64 `json:"expectancy"`
	// ProfitFactor is gross wins / gross losses; > 1 means profitable.
	ProfitFactor float64 `json:"profit_factor"`
	TotalPnL     float64 `json:"total_pnl"`
	// MaxDrawdown is the deepest peak-to-trough fall of the realised P&L
	// curve (open positions are not marked here).
	MaxDrawdown float64 `json:"max_drawdown"`
}

// ComputePerformance folds closed trades into the report card. Pure.
func ComputePerformance(trades []ClosedTrade) Performance {
	var p Performance
	var grossWin, grossLoss, equity, peak float64
	for _, t := range trades {
		p.ClosedTrades++
		p.TotalPnL += t.RealizedPnL
		if t.RealizedPnL >= 0 {
			p.Wins++
			grossWin += t.RealizedPnL
		} else {
			p.Losses++
			grossLoss += -t.RealizedPnL
		}
		equity += t.RealizedPnL
		if equity > peak {
			peak = equity
		}
		if dd := peak - equity; dd > p.MaxDrawdown {
			p.MaxDrawdown = dd
		}
	}
	if p.ClosedTrades > 0 {
		p.WinRate = float64(p.Wins) / float64(p.ClosedTrades)
		p.Expectancy = p.TotalPnL / float64(p.ClosedTrades)
	}
	if p.Wins > 0 {
		p.AvgWin = grossWin / float64(p.Wins)
	}
	if p.Losses > 0 {
		p.AvgLoss = grossLoss / float64(p.Losses)
	}
	if grossLoss > 0 {
		p.ProfitFactor = grossWin / grossLoss
	} else if grossWin > 0 {
		p.ProfitFactor = -1 // sentinel: no losses yet, ratio undefined
	}
	return p
}
