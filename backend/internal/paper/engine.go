// Package paper is the simulated exchange: it prices orders off the live (or
// simulated) market, moves cash, maintains positions and books P&L.
//
// Scope (v1): long-only cash equities. MARKET orders fill immediately at the
// last traded price. LIMIT orders fill when marketable and otherwise rest as
// OPEN until the background matcher picks them up. Shorting, F&O margining and
// brokerage/STT/GST charges are deliberately out of scope and flagged where
// they would slot in.
package paper

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/gangrajat/groww-paper-trading/backend/internal/marketdata"
	"github.com/gangrajat/groww-paper-trading/backend/internal/store"
)

// RejectError is a business-rule rejection (bad input, no funds, no shares).
// The API turns it into a 400 and the engine records it on the order.
type RejectError struct{ Reason string }

func (e RejectError) Error() string { return e.Reason }

func reject(format string, args ...any) RejectError {
	return RejectError{Reason: fmt.Sprintf(format, args...)}
}

// Engine coordinates the store and the market data source.
type Engine struct {
	store  *store.Store
	market marketdata.Provider
	log    *slog.Logger
	// now is injectable so the intraday square-off is testable.
	now func() time.Time
}

func New(s *store.Store, m marketdata.Provider, log *slog.Logger) *Engine {
	return &Engine{store: s, market: m, log: log, now: time.Now}
}

// istZone is the exchange clock; the square-off rule lives on it.
var istZone = time.FixedZone("IST", 5*3600+1800)

// inSquareOffWindow reports whether intraday (MIS) positions must be closed:
// NSE brokers square off between 15:15 and the 15:30 close.
func (e *Engine) inSquareOffWindow() bool {
	now := e.now().In(istZone)
	mins := now.Hour()*60 + now.Minute()
	return mins >= 15*60+15 && mins <= 15*60+40
}

// OrderRequest is the inbound order, already JSON-decoded.
type OrderRequest struct {
	TradingSymbol   string           `json:"trading_symbol"`
	Exchange        string           `json:"exchange"`
	Segment         string           `json:"segment"`
	Product         string           `json:"product"`
	TransactionType string           `json:"transaction_type"`
	OrderType       string           `json:"order_type"`
	Quantity        int64            `json:"quantity"`
	LimitPrice      *decimal.Decimal `json:"limit_price"`
	// TriggerPrice arms an SL (stop-market) order directly.
	TriggerPrice *decimal.Decimal `json:"trigger_price"`
	// StopLoss and Target on a BUY create a bracket: once the buy fills, a
	// stop-market SELL at StopLoss and a LIMIT SELL at Target are placed as an
	// OCO pair — whichever fills first cancels the other.
	StopLoss *decimal.Decimal `json:"stop_loss"`
	Target   *decimal.Decimal `json:"target"`
	// TrailBy makes the bracket's stop a trailing stop: as the price makes new
	// highs, the stop trigger ratchets up to (high - TrailBy), locking in
	// profit while never moving down.
	TrailBy *decimal.Decimal `json:"trail_by"`
}

// normalise upper-cases the enum-ish fields, applies defaults and validates.
func (r *OrderRequest) normalise() error {
	r.TradingSymbol = strings.ToUpper(strings.TrimSpace(r.TradingSymbol))
	r.Exchange = strings.ToUpper(strings.TrimSpace(defaultTo(r.Exchange, "NSE")))
	r.Segment = strings.ToUpper(strings.TrimSpace(defaultTo(r.Segment, "CASH")))
	r.Product = strings.ToUpper(strings.TrimSpace(defaultTo(r.Product, "CNC")))
	r.TransactionType = strings.ToUpper(strings.TrimSpace(r.TransactionType))
	r.OrderType = strings.ToUpper(strings.TrimSpace(defaultTo(r.OrderType, "MARKET")))

	switch {
	case r.TradingSymbol == "":
		return reject("trading_symbol is required")
	case r.TransactionType != "BUY" && r.TransactionType != "SELL":
		return reject("transaction_type must be BUY or SELL")
	case r.OrderType != "MARKET" && r.OrderType != "LIMIT" && r.OrderType != "SL":
		return reject("order_type must be MARKET, LIMIT or SL")
	case r.Quantity <= 0:
		return reject("quantity must be greater than 0")
	case r.OrderType == "LIMIT" && (r.LimitPrice == nil || !r.LimitPrice.IsPositive()):
		return reject("limit_price is required and must be positive for LIMIT orders")
	case r.OrderType == "SL" && r.TransactionType != "SELL":
		return reject("SL (stop-loss) orders are SELL-only in v1 — long positions only")
	case r.OrderType == "SL" && (r.TriggerPrice == nil || !r.TriggerPrice.IsPositive()):
		return reject("trigger_price is required and must be positive for SL orders")
	}
	if r.OrderType == "MARKET" {
		r.LimitPrice = nil // a stray limit price on a market order is noise
	}
	if r.OrderType != "SL" {
		r.TriggerPrice = nil
	}

	// Bracket parameters only make sense on a BUY.
	if r.StopLoss != nil || r.Target != nil {
		if r.TransactionType != "BUY" {
			return reject("stop_loss/target brackets are only supported on BUY orders")
		}
		if r.StopLoss == nil || r.Target == nil {
			return reject("a bracket needs both stop_loss and target")
		}
		if !r.StopLoss.IsPositive() || !r.Target.IsPositive() {
			return reject("stop_loss and target must be positive")
		}
		if r.StopLoss.GreaterThanOrEqual(*r.Target) {
			return reject("stop_loss must be below target")
		}
	}
	if r.TrailBy != nil {
		if r.StopLoss == nil {
			return reject("trail_by requires a stop_loss bracket")
		}
		if !r.TrailBy.IsPositive() {
			return reject("trail_by must be positive")
		}
	}
	return nil
}

func defaultTo(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// Account returns (creating on first use) the paper account being traded.
func (e *Engine) Account(ctx context.Context, name string, startingCash decimal.Decimal) (store.Account, error) {
	return e.store.EnsureAccount(ctx, name, startingCash)
}

// ---------------------------------------------------------------------------
// Order placement
// ---------------------------------------------------------------------------

// PlaceOrder prices, records and (if marketable) fills an order.
//
// A rejection is not an error to the caller: the order is persisted with status
// REJECTED and the reason in message, matching how a real broker reports it.
// Only infrastructure failures come back as errors.
func (e *Engine) PlaceOrder(ctx context.Context, account store.Account, req OrderRequest) (store.Order, error) {
	if err := req.normalise(); err != nil {
		return store.Order{}, err
	}

	ltp, err := e.market.LTP(ctx, req.Exchange, req.Segment, req.TradingSymbol)
	if err != nil {
		return store.Order{}, fmt.Errorf("price %s: %w", req.TradingSymbol, err)
	}
	if !ltp.IsPositive() {
		return store.Order{}, reject("no price available for %s", req.TradingSymbol)
	}

	// A bracket that is already breached at entry would fill and instantly
	// exit; reject it as user error instead.
	if req.StopLoss != nil && req.Target != nil {
		if req.StopLoss.GreaterThanOrEqual(ltp) {
			return store.Order{}, reject("stop_loss %s is at or above the market (%s)",
				req.StopLoss.StringFixed(2), ltp.StringFixed(2))
		}
		if req.Target.LessThanOrEqual(ltp) {
			return store.Order{}, reject("target %s is at or below the market (%s)",
				req.Target.StringFixed(2), ltp.StringFixed(2))
		}
	}

	ref, err := newOrderRef()
	if err != nil {
		return store.Order{}, err
	}

	var out store.Order
	err = e.store.InTx(ctx, func(tx pgx.Tx) error {
		acct, err := store.LockAccount(ctx, tx, account.ID)
		if err != nil {
			return err
		}

		order, err := store.InsertOrder(ctx, tx, store.Order{
			AccountID:       acct.ID,
			OrderRef:        ref,
			TradingSymbol:   req.TradingSymbol,
			Exchange:        req.Exchange,
			Segment:         req.Segment,
			Product:         req.Product,
			TransactionType: req.TransactionType,
			OrderType:       req.OrderType,
			Quantity:        req.Quantity,
			LimitPrice:      req.LimitPrice,
			TriggerPrice:    req.TriggerPrice,
			StopLoss:        req.StopLoss,
			Target:          req.Target,
			TrailBy:         req.TrailBy,
			Status:          "OPEN",
		})
		if err != nil {
			return err
		}

		out, err = e.settle(ctx, tx, acct, order, ltp)
		return err
	})
	return out, err
}

// settle applies the fill decision for one order inside an open transaction.
// The account row must already be locked by the caller.
func (e *Engine) settle(ctx context.Context, tx pgx.Tx, acct store.Account, order store.Order, ltp decimal.Decimal) (store.Order, error) {
	fill, marketable := fillPrice(order, ltp)
	if !marketable {
		var waitingFor string
		if order.OrderType == "SL" && order.TriggerPrice != nil {
			waitingFor = "trigger " + order.TriggerPrice.StringFixed(2)
		} else if order.LimitPrice != nil {
			waitingFor = order.LimitPrice.StringFixed(2)
		}
		msg := fmt.Sprintf("Resting — waiting for %s to reach %s (LTP %s).",
			order.TradingSymbol, waitingFor, ltp.StringFixed(2))
		order.Message = &msg
		return store.UpdateOrderOutcome(ctx, tx, order)
	}

	if err := e.applyFill(ctx, tx, acct, &order, fill); err != nil {
		var rej RejectError
		if errors.As(err, &rej) {
			msg := rej.Reason
			order.Status = "REJECTED"
			order.Message = &msg
			return store.UpdateOrderOutcome(ctx, tx, order)
		}
		return store.Order{}, err
	}

	msg := "Filled (paper)."
	// A square-off fill keeps its explanation instead of the generic receipt.
	if order.Message != nil && strings.Contains(*order.Message, "square-off") {
		msg = *order.Message
	}
	order.Status = "FILLED"
	order.FillPrice = &fill
	order.FilledQuantity = order.Quantity
	order.Message = &msg

	updated, err := store.UpdateOrderOutcome(ctx, tx, order)
	if err != nil {
		return store.Order{}, err
	}

	// A filled exit cancels its OCO sibling; a filled bracket BUY spawns the
	// exit pair.
	if updated.OCOGroup != nil {
		if _, err := store.CancelOCOSiblings(ctx, tx, acct.ID, *updated.OCOGroup, updated.OrderRef); err != nil {
			return store.Order{}, err
		}
	}
	if updated.TransactionType == "BUY" && updated.StopLoss != nil && updated.Target != nil {
		if err := e.spawnBracket(ctx, tx, acct, updated); err != nil {
			return store.Order{}, err
		}
	}
	return updated, nil
}

// spawnBracket places the stop-loss and target exits for a filled bracket buy.
// They rest as an OCO pair keyed by the parent's order ref.
func (e *Engine) spawnBracket(ctx context.Context, tx pgx.Tx, acct store.Account, parent store.Order) error {
	group := parent.OrderRef

	slRef, err := newOrderRef()
	if err != nil {
		return err
	}
	slMsg := fmt.Sprintf("Stop-loss for %s (bracket).", parent.OrderRef)
	if parent.TrailBy != nil {
		slMsg = fmt.Sprintf("Trailing stop for %s (bracket, trails by %s).",
			parent.OrderRef, parent.TrailBy.StringFixed(2))
	}
	slSeed := parent.FillPrice // trail ratchets from the entry price
	if _, err := store.InsertOrder(ctx, tx, store.Order{
		AccountID:       acct.ID,
		OrderRef:        slRef,
		TradingSymbol:   parent.TradingSymbol,
		Exchange:        parent.Exchange,
		Segment:         parent.Segment,
		Product:         parent.Product,
		TransactionType: "SELL",
		OrderType:       "SL",
		Quantity:        parent.Quantity,
		TriggerPrice:    parent.StopLoss,
		TrailBy:         parent.TrailBy,
		HighWater:       slSeed,
		OCOGroup:        &group,
		Status:          "OPEN",
		Message:         &slMsg,
	}); err != nil {
		return err
	}

	tgtRef, err := newOrderRef()
	if err != nil {
		return err
	}
	tgtMsg := fmt.Sprintf("Target for %s (bracket).", parent.OrderRef)
	if _, err := store.InsertOrder(ctx, tx, store.Order{
		AccountID:       acct.ID,
		OrderRef:        tgtRef,
		TradingSymbol:   parent.TradingSymbol,
		Exchange:        parent.Exchange,
		Segment:         parent.Segment,
		Product:         parent.Product,
		TransactionType: "SELL",
		OrderType:       "LIMIT",
		Quantity:        parent.Quantity,
		LimitPrice:      parent.Target,
		OCOGroup:        &group,
		Status:          "OPEN",
		Message:         &tgtMsg,
	}); err != nil {
		return err
	}
	return nil
}

// fillPrice decides what an order fills at, if at all.
//
// A marketable LIMIT fills at the better of the limit and the market, which is
// how a real exchange would treat a crossing order. An SL sell is a
// stop-market: it arms when the price trades at or below the trigger and then
// fills at the market, which models the gap risk a real stop carries.
func fillPrice(order store.Order, ltp decimal.Decimal) (price decimal.Decimal, marketable bool) {
	if order.OrderType == "MARKET" {
		return ltp, true
	}
	if order.OrderType == "SL" {
		if order.TriggerPrice != nil && ltp.LessThanOrEqual(*order.TriggerPrice) {
			return ltp, true
		}
		return decimal.Zero, false
	}
	if order.LimitPrice == nil {
		return decimal.Zero, false
	}
	limit := *order.LimitPrice

	if order.TransactionType == "BUY" {
		if ltp.LessThanOrEqual(limit) {
			return decimal.Min(ltp, limit), true
		}
		return decimal.Zero, false
	}
	if ltp.GreaterThanOrEqual(limit) {
		return decimal.Max(ltp, limit), true
	}
	return decimal.Zero, false
}

// applyFill moves cash, updates the holding and books the trade. It returns a
// RejectError when the account can't support the fill.
//
// Brokerage, STT and GST would be deducted here; v1 trades at a clean price.
func (e *Engine) applyFill(ctx context.Context, tx pgx.Tx, acct store.Account, order *store.Order, fill decimal.Decimal) error {
	qty := decimal.NewFromInt(order.Quantity)
	value := fill.Mul(qty)

	pos, err := store.GetPositionForUpdate(ctx, tx, acct.ID, order.TradingSymbol, order.Segment)
	switch {
	case errors.Is(err, store.ErrNotFound):
		pos = store.Position{
			AccountID:     acct.ID,
			TradingSymbol: order.TradingSymbol,
			Exchange:      order.Exchange,
			Segment:       order.Segment,
		}
	case err != nil:
		return err
	}

	realized := decimal.Zero

	if order.TransactionType == "BUY" {
		if acct.Cash.LessThan(value) {
			return reject("insufficient funds: need %s, have %s",
				value.StringFixed(2), acct.Cash.StringFixed(2))
		}
		acct.Cash = acct.Cash.Sub(value)

		// Weighted-average cost basis.
		newQty := pos.Quantity + order.Quantity
		cost := pos.AvgPrice.Mul(decimal.NewFromInt(pos.Quantity)).Add(value)
		pos.AvgPrice = cost.Div(decimal.NewFromInt(newQty)).Round(4)
		pos.Quantity = newQty
	} else {
		// Long-only: you can only sell what you hold.
		if pos.Quantity < order.Quantity {
			return reject("cannot sell %d %s: only %d held (shorting is not supported)",
				order.Quantity, order.TradingSymbol, pos.Quantity)
		}
		realized = fill.Sub(pos.AvgPrice).Mul(qty).Round(4)
		pos.RealizedPnL = pos.RealizedPnL.Add(realized)
		pos.Quantity -= order.Quantity
		acct.Cash = acct.Cash.Add(value)
		if pos.Quantity == 0 {
			pos.AvgPrice = decimal.Zero
		}
	}

	if err := store.UpsertPosition(ctx, tx, pos); err != nil {
		return err
	}
	if err := store.UpdateAccountCash(ctx, tx, acct.ID, acct.Cash); err != nil {
		return err
	}
	return store.InsertTrade(ctx, tx, store.Trade{
		AccountID:       acct.ID,
		OrderRef:        order.OrderRef,
		TradingSymbol:   order.TradingSymbol,
		TransactionType: order.TransactionType,
		Quantity:        order.Quantity,
		Price:           fill,
		RealizedPnL:     realized,
	})
}

// ---------------------------------------------------------------------------
// Resting orders
// ---------------------------------------------------------------------------

// MatchOpenOrders tries to fill every resting LIMIT order at the current price
// and returns how many filled. Safe to call concurrently: the account row is
// locked for the duration.
func (e *Engine) MatchOpenOrders(ctx context.Context, accountID int64) (int, error) {
	// Price lookups hit the network, so gather the orders first, then price
	// them outside the transaction, then re-open a transaction to settle.
	var open []store.Order
	err := e.store.InTx(ctx, func(tx pgx.Tx) error {
		var err error
		open, err = store.ListOpenOrdersForUpdate(ctx, tx, accountID)
		return err
	})
	if err != nil || len(open) == 0 {
		return 0, err
	}

	prices := make(map[string]decimal.Decimal, len(open))
	for _, o := range open {
		key := o.Exchange + "|" + o.Segment + "|" + o.TradingSymbol
		if _, ok := prices[key]; ok {
			continue
		}
		p, err := e.market.LTP(ctx, o.Exchange, o.Segment, o.TradingSymbol)
		if err != nil {
			e.log.Warn("matcher: price lookup failed", "symbol", o.TradingSymbol, "err", err)
			continue
		}
		prices[key] = p
	}

	filled := 0
	err = e.store.InTx(ctx, func(tx pgx.Tx) error {
		acct, err := store.LockAccount(ctx, tx, accountID)
		if err != nil {
			return err
		}
		orders, err := store.ListOpenOrdersForUpdate(ctx, tx, accountID)
		if err != nil {
			return err
		}
		squareOff := e.inSquareOffWindow()
		for _, o := range orders {
			// An earlier fill in this pass may have cancelled this order as
			// its OCO sibling; the snapshot is stale, so re-read before acting.
			current, err := store.GetOrderForUpdate(ctx, tx, accountID, o.OrderRef)
			if err != nil {
				return err
			}
			if current.Status != "OPEN" {
				continue
			}
			ltp, ok := prices[current.Exchange+"|"+current.Segment+"|"+current.TradingSymbol]
			if !ok {
				continue
			}

			// Trailing stop: ratchet the trigger up before testing the fill.
			if current.OrderType == "SL" && current.TrailBy != nil {
				wm := ltp
				if current.HighWater != nil && current.HighWater.GreaterThan(wm) {
					wm = *current.HighWater
				}
				newTrigger := wm.Sub(*current.TrailBy)
				if current.TriggerPrice == nil || newTrigger.GreaterThan(*current.TriggerPrice) {
					if err := store.UpdateOrderTrail(ctx, tx, current.ID, newTrigger, wm); err != nil {
						return err
					}
					current.TriggerPrice = &newTrigger
					current.HighWater = &wm
				} else if current.HighWater == nil || wm.GreaterThan(*current.HighWater) {
					if err := store.UpdateOrderTrail(ctx, tx, current.ID, *current.TriggerPrice, wm); err != nil {
						return err
					}
					current.HighWater = &wm
				}
			}

			// 15:15 IST: intraday (MIS) exits stop waiting for their level and
			// go out at the market, closing the position for the day.
			if squareOff && current.Product == "MIS" && current.TransactionType == "SELL" {
				current.OrderType = "MARKET"
				msg := "Auto square-off 15:15 IST (intraday)."
				current.Message = &msg
			}

			settled, err := e.settle(ctx, tx, acct, current, ltp)
			if err != nil {
				return err
			}
			if settled.Status == "FILLED" {
				filled++
				// Cash moved; re-read so the next order in this batch sees it.
				if acct, err = store.LockAccount(ctx, tx, accountID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return filled, err
}

// CancelOrder cancels a resting order.
func (e *Engine) CancelOrder(ctx context.Context, accountID int64, orderRef string) (store.Order, error) {
	var out store.Order
	err := e.store.InTx(ctx, func(tx pgx.Tx) error {
		order, err := store.GetOrderForUpdate(ctx, tx, accountID, orderRef)
		if err != nil {
			return err
		}
		if order.Status != "OPEN" {
			return reject("order %s is %s and cannot be cancelled", orderRef, order.Status)
		}
		msg := "Cancelled by user."
		order.Status = "CANCELLED"
		order.Message = &msg
		out, err = store.UpdateOrderOutcome(ctx, tx, order)
		return err
	})
	return out, err
}

// RunMatcher fills resting orders for every account on a ticker until ctx is
// cancelled. Accounts are independent: one user's failure doesn't stall the
// rest.
func (e *Engine) RunMatcher(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ids, err := e.store.AccountIDsWithOpenOrders(ctx)
			if err != nil {
				if ctx.Err() == nil {
					e.log.Warn("matcher: listing accounts failed", "err", err)
				}
				continue
			}
			for _, id := range ids {
				n, err := e.MatchOpenOrders(ctx, id)
				if err != nil && ctx.Err() == nil {
					e.log.Warn("matcher pass failed", "account", id, "err", err)
				}
				if n > 0 {
					e.log.Info("matcher filled resting orders", "account", id, "count", n)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Reset
// ---------------------------------------------------------------------------

// Reset wipes history and restores the starting balance.
func (e *Engine) Reset(ctx context.Context, accountID int64) error {
	return e.store.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := store.LockAccount(ctx, tx, accountID); err != nil {
			return err
		}
		return store.ResetAccount(ctx, tx, accountID)
	})
}

// newOrderRef mints the client-visible order id.
func newOrderRef() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "PT" + hex.EncodeToString(b), nil
}
