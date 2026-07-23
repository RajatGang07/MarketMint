package paper

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/gangrajat/groww-paper-trading/backend/internal/store"
)

// PositionView is a holding marked to market.
type PositionView struct {
	TradingSymbol string          `json:"trading_symbol"`
	Exchange      string          `json:"exchange"`
	Segment       string          `json:"segment"`
	Quantity      int64           `json:"quantity"`
	AvgPrice      decimal.Decimal `json:"avg_price"`
	RealizedPnL   decimal.Decimal `json:"realized_pnl"`
	LTP           decimal.Decimal `json:"ltp"`
	MarketValue   decimal.Decimal `json:"market_value"`
	UnrealizedPnL decimal.Decimal `json:"unrealized_pnl"`
}

// PortfolioView is the account summary the dashboard renders.
type PortfolioView struct {
	AccountName   string          `json:"account_name"`
	StartingCash  decimal.Decimal `json:"starting_cash"`
	Cash          decimal.Decimal `json:"cash"`
	Invested      decimal.Decimal `json:"invested"`
	MarketValue   decimal.Decimal `json:"market_value"`
	Equity        decimal.Decimal `json:"equity"`
	RealizedPnL   decimal.Decimal `json:"realized_pnl"`
	UnrealizedPnL decimal.Decimal `json:"unrealized_pnl"`
	TotalPnL      decimal.Decimal `json:"total_pnl"`
	TotalPnLPct   decimal.Decimal `json:"total_pnl_pct"`
	Positions     []PositionView  `json:"positions"`
}

// Portfolio marks every open holding to market and rolls up account equity.
//
// Realised P&L is summed across all rows, including fully-closed holdings
// (quantity 0), which are kept precisely so booked profits survive the exit.
func (e *Engine) Portfolio(ctx context.Context, account store.Account) (PortfolioView, error) {
	rows, err := e.store.ListPositions(ctx, account.ID)
	if err != nil {
		return PortfolioView{}, err
	}

	view := PortfolioView{
		AccountName:  account.Name,
		StartingCash: account.StartingCash.Round(2),
		Cash:         account.Cash.Round(2),
		Positions:    []PositionView{},
	}

	invested, marketValue, unrealized, realized := decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero

	for _, p := range rows {
		realized = realized.Add(p.RealizedPnL)
		if p.Quantity == 0 {
			continue
		}

		ltp, err := e.market.LTP(ctx, p.Exchange, p.Segment, p.TradingSymbol)
		if err != nil {
			// A single unpriceable symbol shouldn't blank the whole dashboard;
			// fall back to cost basis so the row still shows something sane.
			e.log.Warn("portfolio: price lookup failed", "symbol", p.TradingSymbol, "err", err)
			ltp = p.AvgPrice
		}

		qty := decimal.NewFromInt(p.Quantity)
		mv := ltp.Mul(qty)
		cost := p.AvgPrice.Mul(qty)
		upnl := mv.Sub(cost)

		invested = invested.Add(cost)
		marketValue = marketValue.Add(mv)
		unrealized = unrealized.Add(upnl)

		view.Positions = append(view.Positions, PositionView{
			TradingSymbol: p.TradingSymbol,
			Exchange:      p.Exchange,
			Segment:       p.Segment,
			Quantity:      p.Quantity,
			AvgPrice:      p.AvgPrice.Round(2),
			RealizedPnL:   p.RealizedPnL.Round(2),
			LTP:           ltp.Round(2),
			MarketValue:   mv.Round(2),
			UnrealizedPnL: upnl.Round(2),
		})
	}

	equity := account.Cash.Add(marketValue)
	totalPnL := equity.Sub(account.StartingCash)

	pct := decimal.Zero
	if account.StartingCash.IsPositive() {
		pct = totalPnL.Div(account.StartingCash).Mul(decimal.NewFromInt(100))
	}

	view.Invested = invested.Round(2)
	view.MarketValue = marketValue.Round(2)
	view.Equity = equity.Round(2)
	view.RealizedPnL = realized.Round(2)
	view.UnrealizedPnL = unrealized.Round(2)
	view.TotalPnL = totalPnL.Round(2)
	view.TotalPnLPct = pct.Round(2)
	return view, nil
}
