// Package autopilot trades the paper account automatically, using the same
// signals board a human sees on the dashboard. It is deliberately thin: the
// strategy lives in packages analytics/intraday/signals, the execution and
// exits live in package paper — autopilot only connects verdict to order and
// writes down why.
//
// The rules, in full:
//
//	BUY   a board BUY row (top-decile momentum rank with a risk-sized plan)
//	      becomes a MARKET buy with a server-side bracket: stop-loss at the
//	      plan's stop, target at the plan's target, and — when enabled — a
//	      trailing stop that ratchets up by the initial risk distance.
//	SELL  a board SELL row (rank collapsed, RSI blow-off, or an undefended
//	      loser) becomes a MARKET sell of the whole holding — unless a
//	      protective exit is already resting, in which case the bracket is
//	      left to do its job.
//	GUARDS  max concurrent positions, max capital per trade, available cash,
//	      and a one-trade-per-symbol-per-day cooldown so a flapping signal
//	      cannot churn the account.
//
// Every action AND every deliberate inaction is written to autopilot_log with
// its reasons — the audit trail is the product; a black box would be worthless
// for learning.
package autopilot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/gangrajat/groww-paper-trading/backend/internal/analytics"
	"github.com/gangrajat/groww-paper-trading/backend/internal/paper"
	"github.com/gangrajat/groww-paper-trading/backend/internal/signals"
	"github.com/gangrajat/groww-paper-trading/backend/internal/store"
)

// Runner drives every enabled account on a fixed cadence.
type Runner struct {
	engine  *paper.Engine
	signals *signals.Composer
	store   *store.Store
	log     *slog.Logger
}

func New(engine *paper.Engine, composer *signals.Composer, st *store.Store, log *slog.Logger) *Runner {
	return &Runner{engine: engine, signals: composer, store: st, log: log}
}

// Run loops until ctx ends. The first pass fires one interval in, not at
// boot, so a restart storm cannot double-trade.
func (r *Runner) Run(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	r.log.Info("autopilot running", "interval", every.String())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Pass(ctx)
		}
	}
}

// Pass trades every enabled account once. Exported so a settings change can
// trigger an immediate cycle instead of waiting out the ticker.
func (r *Runner) Pass(ctx context.Context) {
	ids, err := r.store.AutopilotEnabledAccounts(ctx)
	if err != nil {
		if ctx.Err() == nil {
			r.log.Warn("autopilot: listing accounts failed", "err", err)
		}
		return
	}
	for _, id := range ids {
		if err := r.tradeAccount(ctx, id); err != nil && ctx.Err() == nil {
			r.log.Warn("autopilot pass failed", "account", id, "err", err)
			r.audit(ctx, id, "ERROR", "", "pass failed: "+err.Error(), "")
		}
	}
}

func (r *Runner) tradeAccount(ctx context.Context, accountID int64) error {
	acct, err := r.store.GetAccount(ctx, accountID)
	if err != nil {
		return fmt.Errorf("account: %w", err)
	}
	settings, err := r.store.AutopilotSettingsFor(ctx, accountID)
	if err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	cash, _ := acct.Cash.Float64()

	board, err := r.signals.Compose(ctx, accountID, analytics.DefaultRiskBands, cash)
	if err != nil {
		return fmt.Errorf("signals: %w", err)
	}
	orders, err := r.store.ListOrders(ctx, accountID, 200)
	if err != nil {
		return fmt.Errorf("orders: %w", err)
	}
	positions, err := r.store.ListPositions(ctx, accountID)
	if err != nil {
		return fmt.Errorf("positions: %w", err)
	}

	decisions := Decide(board.Rows, settings, snapshot(acct, positions, orders))
	for _, d := range decisions {
		traded := r.execute(ctx, acct, d)
		// A fill changes cash; refresh so the next order in this pass is
		// validated against reality (a SELL's proceeds can fund a BUY).
		if traded {
			if fresh, err := r.store.GetAccount(ctx, accountID); err == nil {
				acct = fresh
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Decision logic — pure, so the rules are testable without a database.
// ---------------------------------------------------------------------------

// Snapshot is what Decide needs to know about the account right now.
type Snapshot struct {
	Cash          decimal.Decimal
	OpenPositions int
	// PendingBuys are symbols with an OPEN BUY resting (counts toward the
	// position cap; also blocks a duplicate entry).
	PendingBuys map[string]bool
	// TradedToday blocks re-entry: symbols with any order placed today.
	TradedToday map[string]bool
}

func snapshot(acct store.Account, positions []store.Position, orders []store.Order) Snapshot {
	s := Snapshot{
		Cash:        acct.Cash,
		PendingBuys: map[string]bool{},
		TradedToday: map[string]bool{},
	}
	for _, p := range positions {
		if p.Quantity > 0 {
			s.OpenPositions++
		}
	}
	today := time.Now().In(istZone).Format("2006-01-02")
	for _, o := range orders {
		if o.TransactionType == "BUY" && o.Status == "OPEN" {
			s.PendingBuys[o.TradingSymbol] = true
		}
		if o.CreatedAt.In(istZone).Format("2006-01-02") == today {
			s.TradedToday[o.TradingSymbol] = true
		}
	}
	return s
}

var istZone = time.FixedZone("IST", 5*3600+1800)

// Decision is one intended action, with the audit text already composed.
type Decision struct {
	Action string // BUY / SELL / SKIP
	Symbol string
	Detail string
	Order  *paper.OrderRequest // nil for SKIP
}

// Decide turns board rows into orders under the account's policy. SELLs come
// first: freeing capital before spending it lets an exit fund an entry in the
// same pass.
func Decide(rows []signals.Row, settings store.AutopilotSettings, snap Snapshot) []Decision {
	var out []Decision

	slots := settings.MaxPositions - snap.OpenPositions - len(snap.PendingBuys)
	cash := snap.Cash

	// --- Exits ----------------------------------------------------------
	for _, row := range rows {
		if row.Action != "SELL" || row.HeldQuantity <= 0 {
			continue
		}
		if row.ExitsArmed {
			out = append(out, Decision{Action: "SKIP", Symbol: row.Symbol,
				Detail: "SELL signal, but a protective exit is already resting — leaving the bracket to work. " + reasons(row)})
			continue
		}
		out = append(out, Decision{
			Action: "SELL", Symbol: row.Symbol,
			Detail: fmt.Sprintf("selling %d shares. %s", row.HeldQuantity, reasons(row)),
			Order: &paper.OrderRequest{
				TradingSymbol: row.Symbol, Exchange: "NSE", Segment: "CASH", Product: "CNC",
				TransactionType: "SELL", OrderType: "MARKET", Quantity: row.HeldQuantity,
			},
		})
		slots++ // the freed slot may be reused below
	}

	// --- Entries --------------------------------------------------------
	for _, row := range rows {
		if row.Action != "BUY" || row.Plan == nil || row.Plan.Quantity <= 0 {
			continue
		}
		plan := row.Plan
		switch {
		case snap.PendingBuys[row.Symbol]:
			out = append(out, Decision{Action: "SKIP", Symbol: row.Symbol,
				Detail: "BUY signal, but an entry order is already resting."})
		case snap.TradedToday[row.Symbol]:
			out = append(out, Decision{Action: "SKIP", Symbol: row.Symbol,
				Detail: "BUY signal, but this symbol was already traded today — one trade per symbol per day."})
		case slots <= 0:
			out = append(out, Decision{Action: "SKIP", Symbol: row.Symbol,
				Detail: fmt.Sprintf("BUY signal, but the position cap (%d) is full.", settings.MaxPositions)})
		default:
			// The plan is sized to the risk bands; the account's own budget —
			// per-trade cap and free cash — may be tighter. Buy fewer shares
			// at the same stop/target rather than skipping the idea entirely.
			budget := decimal.Min(settings.MaxCapitalPerTrade, cash)
			qty, capital, sizedDown := fitBudget(plan, budget)
			if qty <= 0 {
				out = append(out, Decision{Action: "SKIP", Symbol: row.Symbol,
					Detail: fmt.Sprintf("BUY signal, but not even one share at %s fits the budget (%s free under the caps).",
						plan.Entry.StringFixed(2), budget.StringFixed(0))})
				continue
			}
			req := &paper.OrderRequest{
				TradingSymbol: row.Symbol, Exchange: "NSE", Segment: "CASH", Product: "CNC",
				TransactionType: "BUY", OrderType: "MARKET", Quantity: qty,
				StopLoss: dec(plan.StopLoss), Target: dec(plan.Target),
			}
			detail := fmt.Sprintf("buying %d shares ≈%s with stop %s / target %s. %s",
				qty, capital.StringFixed(0), plan.StopLoss.StringFixed(2), plan.Target.StringFixed(2), reasons(row))
			if sizedDown {
				detail += fmt.Sprintf(" Sized down from the plan's %d shares to fit the capital caps.", plan.Quantity)
			}
			if settings.TrailStops {
				trail := plan.Entry.Sub(plan.StopLoss)
				if trail.IsPositive() {
					req.TrailBy = dec(trail)
					detail += fmt.Sprintf(" Stop trails by %s once price runs.", trail.StringFixed(2))
				}
			}
			out = append(out, Decision{Action: "BUY", Symbol: row.Symbol, Detail: detail, Order: req})
			slots--
			cash = cash.Sub(capital)
		}
	}
	return out
}

// fitBudget shrinks a plan's quantity until its capital fits the budget,
// keeping entry/stop/target untouched. Returns the final quantity, its cost,
// and whether shrinking happened.
func fitBudget(plan *analytics.Plan, budget decimal.Decimal) (int64, decimal.Decimal, bool) {
	if plan.Capital.LessThanOrEqual(budget) {
		return plan.Quantity, plan.Capital, false
	}
	if !plan.Entry.IsPositive() {
		return 0, decimal.Zero, true
	}
	qty := budget.Div(plan.Entry).IntPart()
	if qty > plan.Quantity {
		qty = plan.Quantity
	}
	if qty <= 0 {
		return 0, decimal.Zero, true
	}
	return qty, plan.Entry.Mul(decimal.NewFromInt(qty)), true
}

func reasons(row signals.Row) string {
	if len(row.Reasons) == 0 {
		return ""
	}
	return "Why: " + strings.Join(row.Reasons, "; ") + "."
}

func dec(d decimal.Decimal) *decimal.Decimal { return &d }

// ---------------------------------------------------------------------------
// Execution
// ---------------------------------------------------------------------------

// execute performs one decision and reports whether an order was placed.
func (r *Runner) execute(ctx context.Context, acct store.Account, d Decision) bool {
	if d.Order == nil {
		r.audit(ctx, acct.ID, d.Action, d.Symbol, d.Detail, "")
		return false
	}
	order, err := r.engine.PlaceOrder(ctx, acct, *d.Order)
	if err != nil {
		r.audit(ctx, acct.ID, "ERROR", d.Symbol, d.Action+" failed: "+err.Error(), "")
		return false
	}
	r.log.Info("autopilot traded", "account", acct.ID, "action", d.Action, "symbol", d.Symbol, "ref", order.OrderRef)
	r.audit(ctx, acct.ID, d.Action, d.Symbol, d.Detail, order.OrderRef)
	return true
}

func (r *Runner) audit(ctx context.Context, accountID int64, action, symbol, detail, ref string) {
	if err := r.store.AppendAutopilotLog(ctx, accountID, action, symbol, detail, ref); err != nil && ctx.Err() == nil {
		r.log.Warn("autopilot: audit write failed", "err", err)
	}
}
