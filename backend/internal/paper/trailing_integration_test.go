package paper

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/gangrajat/groww-paper-trading/backend/internal/store"
)

func TestTrailingStopRatchetsAndLocksProfit(t *testing.T) {
	market := &scriptedMarket{}
	market.set("1000")
	engine, st, account := newTestEngine(t, market)
	ctx := context.Background()

	stop := decimal.RequireFromString("980")
	target := decimal.RequireFromString("1100")
	trail := decimal.RequireFromString("20") // 1R

	buy, err := engine.PlaceOrder(ctx, account, OrderRequest{
		TradingSymbol:   "TESTSTOCK",
		TransactionType: "BUY",
		OrderType:       "MARKET",
		Quantity:        100,
		StopLoss:        &stop,
		Target:          &target,
		TrailBy:         &trail,
	})
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	// Rally to 1050: the trigger must ratchet from 980 to 1050-20=1030.
	market.set("1050")
	if _, err := engine.MatchOpenOrders(ctx, account.ID); err != nil {
		t.Fatalf("match: %v", err)
	}

	slOrder := findExit(t, st, account.ID, buy.OrderRef, "SL")
	if slOrder.Status != "OPEN" {
		t.Fatalf("trailing stop should still rest, got %s", slOrder.Status)
	}
	if !slOrder.TriggerPrice.Equal(decimal.RequireFromString("1030")) {
		t.Fatalf("trigger = %s, want 1030 after the ratchet", slOrder.TriggerPrice)
	}

	// Fade to 1025: below the trailed trigger → fills at market, in profit,
	// and the target is cancelled as its OCO sibling.
	market.set("1025")
	if _, err := engine.MatchOpenOrders(ctx, account.ID); err != nil {
		t.Fatalf("match: %v", err)
	}

	slOrder = findExit(t, st, account.ID, buy.OrderRef, "SL")
	if slOrder.Status != "FILLED" || !slOrder.FillPrice.Equal(decimal.RequireFromString("1025")) {
		t.Fatalf("trail exit = %s @ %v, want FILLED @ 1025", slOrder.Status, slOrder.FillPrice)
	}
	tgt := findExit(t, st, account.ID, buy.OrderRef, "LIMIT")
	if tgt.Status != "CANCELLED" {
		t.Fatalf("target should be cancelled, got %s", tgt.Status)
	}

	// Profit is locked: bought 100 @ 1000, trailed out @ 1025 → +2500.
	positions, err := st.ListPositions(ctx, account.ID)
	if err != nil {
		t.Fatalf("positions: %v", err)
	}
	for _, p := range positions {
		if p.TradingSymbol == "TESTSTOCK" {
			if p.Quantity != 0 || !p.RealizedPnL.Equal(decimal.RequireFromString("2500")) {
				t.Fatalf("want flat with +2500 realised, got qty=%d pnl=%s", p.Quantity, p.RealizedPnL)
			}
		}
	}

	// The trigger must never ratchet down: high-water stays at 1050.
	if slOrder.HighWater == nil || !slOrder.HighWater.Equal(decimal.RequireFromString("1050")) {
		t.Fatalf("high water = %v, want 1050", slOrder.HighWater)
	}
}

func TestMISExitsAreSquaredOffAfter1515(t *testing.T) {
	market := &scriptedMarket{}
	market.set("1000")
	engine, st, account := newTestEngine(t, market)
	ctx := context.Background()

	stop := decimal.RequireFromString("980")
	target := decimal.RequireFromString("1100")
	buy, err := engine.PlaceOrder(ctx, account, OrderRequest{
		TradingSymbol:   "TESTSTOCK",
		Product:         "MIS",
		TransactionType: "BUY",
		OrderType:       "MARKET",
		Quantity:        50,
		StopLoss:        &stop,
		Target:          &target,
	})
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	// 15:20 IST: price is between stop and target — neither exit level is
	// touched, but the square-off must flatten the position anyway.
	engine.now = func() time.Time {
		return time.Date(2026, 7, 23, 15, 20, 0, 0, istZone)
	}
	market.set("1010")
	if _, err := engine.MatchOpenOrders(ctx, account.ID); err != nil {
		t.Fatalf("match: %v", err)
	}

	var filled, cancelled int
	orders, err := st.ListOrders(ctx, account.ID, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, o := range orders {
		if o.OCOGroup == nil || *o.OCOGroup != buy.OrderRef {
			continue
		}
		switch o.Status {
		case "FILLED":
			filled++
			if !o.FillPrice.Equal(decimal.RequireFromString("1010")) {
				t.Fatalf("square-off should fill at market 1010, got %v", o.FillPrice)
			}
		case "CANCELLED":
			cancelled++
		}
	}
	if filled != 1 || cancelled != 1 {
		t.Fatalf("want one square-off fill + one OCO cancel, got filled=%d cancelled=%d", filled, cancelled)
	}

	positions, _ := st.ListPositions(ctx, account.ID)
	for _, p := range positions {
		if p.TradingSymbol == "TESTSTOCK" && p.Quantity != 0 {
			t.Fatalf("MIS position must be flat after square-off, holds %d", p.Quantity)
		}
	}
}

// CNC (delivery) exits must NOT be squared off — only intraday product is.
func TestCNCPositionsSurviveTheSquareOffWindow(t *testing.T) {
	market := &scriptedMarket{}
	market.set("1000")
	engine, st, account := newTestEngine(t, market)
	ctx := context.Background()

	stop := decimal.RequireFromString("980")
	target := decimal.RequireFromString("1100")
	buy, err := engine.PlaceOrder(ctx, account, OrderRequest{
		TradingSymbol:   "TESTSTOCK",
		Product:         "CNC",
		TransactionType: "BUY",
		OrderType:       "MARKET",
		Quantity:        50,
		StopLoss:        &stop,
		Target:          &target,
	})
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	engine.now = func() time.Time {
		return time.Date(2026, 7, 23, 15, 20, 0, 0, istZone)
	}
	market.set("1010")
	if _, err := engine.MatchOpenOrders(ctx, account.ID); err != nil {
		t.Fatalf("match: %v", err)
	}

	for _, kind := range []string{"SL", "LIMIT"} {
		if o := findExit(t, st, account.ID, buy.OrderRef, kind); o.Status != "OPEN" {
			t.Fatalf("CNC %s exit should keep resting through 15:15, got %s", kind, o.Status)
		}
	}
}

func findExit(t *testing.T, st *store.Store, accountID int64, group, orderType string) store.Order {
	t.Helper()
	orders, err := st.ListOrders(context.Background(), accountID, 20)
	if err != nil {
		t.Fatalf("list orders: %v", err)
	}
	for _, o := range orders {
		if o.OCOGroup != nil && *o.OCOGroup == group && o.OrderType == orderType {
			return o
		}
	}
	t.Fatalf("no %s exit found for group %s", orderType, group)
	return store.Order{}
}
