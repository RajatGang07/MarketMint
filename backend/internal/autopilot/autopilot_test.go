package autopilot

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/gangrajat/groww-paper-trading/backend/internal/analytics"
	"github.com/gangrajat/groww-paper-trading/backend/internal/signals"
	"github.com/gangrajat/groww-paper-trading/backend/internal/store"
)

func plan(entry, stop, target float64, qty int64, capital float64) *analytics.Plan {
	return &analytics.Plan{
		Entry:    decimal.NewFromFloat(entry),
		StopLoss: decimal.NewFromFloat(stop),
		Target:   decimal.NewFromFloat(target),
		Quantity: qty,
		Capital:  decimal.NewFromFloat(capital),
	}
}

func settings() store.AutopilotSettings {
	s := store.DefaultAutopilotSettings(1)
	s.Enabled = true
	return s
}

func snap(cash float64) Snapshot {
	return Snapshot{
		Cash:        decimal.NewFromFloat(cash),
		PendingBuys: map[string]bool{},
		TradedToday: map[string]bool{},
	}
}

func find(t *testing.T, ds []Decision, symbol string) Decision {
	t.Helper()
	for _, d := range ds {
		if d.Symbol == symbol {
			return d
		}
	}
	t.Fatalf("no decision for %s in %+v", symbol, ds)
	return Decision{}
}

func TestTrailStyleRidesTheTrend(t *testing.T) {
	// Default style is "trail": stop + trailing, deliberately no target.
	rows := []signals.Row{{
		Action: "BUY", Symbol: "TCS", Plan: plan(100, 95, 110, 10, 1000),
		Reasons: []string{"momentum rank #3"},
	}}
	d := find(t, Decide(rows, settings(), snap(10_000)), "TCS")
	if d.Action != "BUY" || d.Order == nil {
		t.Fatalf("want BUY with order, got %+v", d)
	}
	if d.Order.StopLoss == nil || d.Order.TrailBy == nil {
		t.Fatal("trail style must carry stop and trail")
	}
	if d.Order.Target != nil {
		t.Error("trail style must not cap the win with a target")
	}
	if !d.Order.TrailBy.Equal(decimal.NewFromInt(5)) {
		t.Errorf("trail should equal entry-stop distance, got %s", d.Order.TrailBy)
	}
}

func TestBracketStyleCapsAtTarget(t *testing.T) {
	rows := []signals.Row{{Action: "BUY", Symbol: "TCS", Plan: plan(100, 95, 110, 10, 1000)}}
	s := settings()
	s.ExitStyle = "bracket"
	d := find(t, Decide(rows, s, snap(10_000)), "TCS")
	if d.Order.StopLoss == nil || d.Order.Target == nil || d.Order.TrailBy == nil {
		t.Fatal("bracket style with trail_stops must carry stop, target and trail")
	}

	s.TrailStops = false
	d = find(t, Decide(rows, s, snap(10_000)), "TCS")
	if d.Order.TrailBy != nil {
		t.Error("bracket style with trail_stops off must not trail")
	}
	if d.Order.Target == nil {
		t.Error("bracket style must keep the target")
	}
}

func TestGuards(t *testing.T) {
	row := signals.Row{Action: "BUY", Symbol: "TCS", Plan: plan(100, 95, 110, 10, 1000)}

	t.Run("position cap", func(t *testing.T) {
		sn := snap(10_000)
		sn.OpenPositions = 5
		d := find(t, Decide([]signals.Row{row}, settings(), sn), "TCS")
		if d.Action != "SKIP" || d.Order != nil {
			t.Fatalf("full book must SKIP, got %+v", d)
		}
	})
	t.Run("traded today", func(t *testing.T) {
		sn := snap(10_000)
		sn.TradedToday["TCS"] = true
		if d := find(t, Decide([]signals.Row{row}, settings(), sn), "TCS"); d.Action != "SKIP" {
			t.Fatalf("cooldown must SKIP, got %+v", d)
		}
	})
	t.Run("pending buy", func(t *testing.T) {
		sn := snap(10_000)
		sn.PendingBuys["TCS"] = true
		if d := find(t, Decide([]signals.Row{row}, settings(), sn), "TCS"); d.Action != "SKIP" {
			t.Fatalf("resting entry must SKIP, got %+v", d)
		}
	})
	t.Run("cash short sizes down", func(t *testing.T) {
		// Plan wants 10 shares (1000); 500 cash fits 5 at the same stop/target.
		d := find(t, Decide([]signals.Row{row}, settings(), snap(500)), "TCS")
		if d.Action != "BUY" || d.Order.Quantity != 5 {
			t.Fatalf("want sized-down BUY of 5, got %+v", d)
		}
	})
	t.Run("per-trade cap sizes down", func(t *testing.T) {
		s := settings()
		s.MaxCapitalPerTrade = decimal.NewFromInt(500)
		d := find(t, Decide([]signals.Row{row}, s, snap(10_000)), "TCS")
		if d.Action != "BUY" || d.Order.Quantity != 5 {
			t.Fatalf("want sized-down BUY of 5, got %+v", d)
		}
	})
	t.Run("below one share skips", func(t *testing.T) {
		if d := find(t, Decide([]signals.Row{row}, settings(), snap(50)), "TCS"); d.Action != "SKIP" {
			t.Fatalf("sub-one-share budget must SKIP, got %+v", d)
		}
	})
}

func TestCashBudgetSpansPass(t *testing.T) {
	// Two BUYs, cash for only one: the second must be skipped.
	rows := []signals.Row{
		{Action: "BUY", Symbol: "AAA", Plan: plan(100, 95, 110, 10, 900)},
		{Action: "BUY", Symbol: "BBB", Plan: plan(100, 95, 110, 10, 900)},
	}
	ds := Decide(rows, settings(), snap(1_000))
	if find(t, ds, "AAA").Action != "BUY" {
		t.Error("first should buy")
	}
	// 100 cash remains after AAA: BBB is sized down to what fits, not skipped.
	if d := find(t, ds, "BBB"); d.Action != "BUY" || d.Order.Quantity != 1 {
		t.Errorf("second should size down to 1 share on remaining cash, got %+v", d)
	}
}

func TestSellHolding(t *testing.T) {
	rows := []signals.Row{{
		Action: "SELL", Symbol: "INFY", HeldQuantity: 7,
		Reasons: []string{"momentum rank collapsed"},
	}}
	d := find(t, Decide(rows, settings(), snap(0)), "INFY")
	if d.Action != "SELL" || d.Order == nil || d.Order.Quantity != 7 {
		t.Fatalf("want SELL of 7, got %+v", d)
	}
	if d.Order.TransactionType != "SELL" || d.Order.OrderType != "MARKET" {
		t.Fatalf("want market sell, got %+v", d.Order)
	}
}

func TestSellSkippedWhenExitsArmed(t *testing.T) {
	rows := []signals.Row{{Action: "SELL", Symbol: "INFY", HeldQuantity: 7, ExitsArmed: true}}
	d := find(t, Decide(rows, settings(), snap(0)), "INFY")
	if d.Action != "SKIP" || d.Order != nil {
		t.Fatalf("armed exits must not double-sell, got %+v", d)
	}
}

func TestSellFreesSlotForBuy(t *testing.T) {
	rows := []signals.Row{
		{Action: "SELL", Symbol: "INFY", HeldQuantity: 7, Reasons: []string{"rank collapsed"}},
		{Action: "BUY", Symbol: "TCS", Plan: plan(100, 95, 110, 10, 1000)},
	}
	sn := snap(10_000)
	sn.OpenPositions = 5 // book full until the sell frees a slot
	ds := Decide(rows, settings(), sn)
	if find(t, ds, "INFY").Action != "SELL" {
		t.Error("sell should fire")
	}
	if find(t, ds, "TCS").Action != "BUY" {
		t.Error("freed slot should allow the buy")
	}
}

func TestWatchAndHoldDoNothing(t *testing.T) {
	rows := []signals.Row{
		{Action: "WATCH", Symbol: "AAA"},
		{Action: "HOLD", Symbol: "BBB", HeldQuantity: 3},
	}
	if ds := Decide(rows, settings(), snap(10_000)); len(ds) != 0 {
		t.Fatalf("WATCH/HOLD must produce no decisions, got %+v", ds)
	}
}
