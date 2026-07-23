package paper

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/gangrajat/groww-paper-trading/backend/internal/marketdata"
	"github.com/gangrajat/groww-paper-trading/backend/internal/store"
)

// scriptedMarket serves whatever price the test sets, so fills are
// deterministic.
type scriptedMarket struct {
	mu    sync.Mutex
	price decimal.Decimal
}

func (m *scriptedMarket) set(p string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.price = decimal.RequireFromString(p)
}

func (m *scriptedMarket) Name() string { return "scripted" }

func (m *scriptedMarket) LTP(context.Context, string, string, string) (decimal.Decimal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.price, nil
}

func (m *scriptedMarket) Quote(ctx context.Context, ex, seg, sym string) (marketdata.Quote, error) {
	p, _ := m.LTP(ctx, ex, seg, sym)
	return marketdata.Quote{Symbol: sym, LastPrice: p}, nil
}

func (m *scriptedMarket) Candles(context.Context, marketdata.CandleRequest) ([]marketdata.Candle, error) {
	return nil, marketdata.ErrUnsupported
}

// newTestEngine spins up an engine against a throwaway schema in the local
// Postgres. Skips when no database is reachable so `go test` still works on a
// bare machine.
func newTestEngine(t *testing.T, market *scriptedMarket) (*Engine, *store.Store, store.Account) {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://paper:paper@localhost:5432/paper_trading?sslmode=disable"
	}

	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v); skipping integration test", err)
	}
	t.Cleanup(st.Close)

	// A per-run account name keeps parallel/old runs from colliding.
	name := fmt.Sprintf("it-%d", time.Now().UnixNano())
	account, err := st.EnsureAccount(ctx, name, decimal.RequireFromString("1000000"))
	if err != nil {
		t.Fatalf("ensure account: %v", err)
	}
	// Leave no residue: the FK cascade removes the account's orders,
	// positions and trades with it.
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM accounts WHERE id = $1`, account.ID)
	})

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(st, market, log), st, account
}

func TestBracketBuySpawnsOCOExits(t *testing.T) {
	market := &scriptedMarket{}
	market.set("1000")
	engine, st, account := newTestEngine(t, market)
	ctx := context.Background()

	stop := decimal.RequireFromString("950")
	target := decimal.RequireFromString("1080")

	buy, err := engine.PlaceOrder(ctx, account, OrderRequest{
		TradingSymbol:   "TESTSTOCK",
		TransactionType: "BUY",
		OrderType:       "MARKET",
		Quantity:        100,
		StopLoss:        &stop,
		Target:          &target,
	})
	if err != nil {
		t.Fatalf("place bracket buy: %v", err)
	}
	if buy.Status != "FILLED" {
		t.Fatalf("buy status = %s, want FILLED", buy.Status)
	}

	orders, err := st.ListOrders(ctx, account.ID, 10)
	if err != nil {
		t.Fatalf("list orders: %v", err)
	}

	var slOrder, tgtOrder *store.Order
	for i := range orders {
		o := orders[i]
		if o.OCOGroup != nil && *o.OCOGroup == buy.OrderRef {
			switch o.OrderType {
			case "SL":
				slOrder = &orders[i]
			case "LIMIT":
				tgtOrder = &orders[i]
			}
		}
	}
	if slOrder == nil || tgtOrder == nil {
		t.Fatalf("bracket exits missing: sl=%v tgt=%v", slOrder != nil, tgtOrder != nil)
	}
	if slOrder.Status != "OPEN" || tgtOrder.Status != "OPEN" {
		t.Fatalf("exits should rest OPEN, got sl=%s tgt=%s", slOrder.Status, tgtOrder.Status)
	}
	if !slOrder.TriggerPrice.Equal(stop) || !tgtOrder.LimitPrice.Equal(target) {
		t.Fatalf("exit prices wrong: sl trigger=%v tgt limit=%v", slOrder.TriggerPrice, tgtOrder.LimitPrice)
	}
}

func TestStopLossFillCancelsTargetAndBooksLoss(t *testing.T) {
	market := &scriptedMarket{}
	market.set("1000")
	engine, st, account := newTestEngine(t, market)
	ctx := context.Background()

	stop := decimal.RequireFromString("950")
	target := decimal.RequireFromString("1080")
	buy, err := engine.PlaceOrder(ctx, account, OrderRequest{
		TradingSymbol:   "TESTSTOCK",
		TransactionType: "BUY",
		OrderType:       "MARKET",
		Quantity:        100,
		StopLoss:        &stop,
		Target:          &target,
	})
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	// Gap through the stop: 940 < 950 trigger. The SL is a stop-market, so it
	// fills at the (worse) market price — that models real gap risk.
	market.set("940")
	if _, err := engine.MatchOpenOrders(ctx, account.ID); err != nil {
		t.Fatalf("match: %v", err)
	}

	orders, err := st.ListOrders(ctx, account.ID, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	var filledSL, cancelledTgt bool
	for _, o := range orders {
		if o.OCOGroup == nil || *o.OCOGroup != buy.OrderRef {
			continue
		}
		switch o.OrderType {
		case "SL":
			if o.Status == "FILLED" && o.FillPrice.Equal(decimal.RequireFromString("940")) {
				filledSL = true
			}
		case "LIMIT":
			if o.Status == "CANCELLED" {
				cancelledTgt = true
			}
		}
	}
	if !filledSL {
		t.Fatal("stop-loss should have filled at the gapped market price 940")
	}
	if !cancelledTgt {
		t.Fatal("target should have been cancelled as the OCO sibling")
	}

	// Position is flat and the loss is booked: 100 × (940 − 1000) = −6000.
	acct, err := st.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	wantCash := decimal.RequireFromString("994000") // 1,000,000 − 100,000 + 94,000
	if !acct.Cash.Equal(wantCash) {
		t.Fatalf("cash = %s, want %s", acct.Cash, wantCash)
	}

	positions, err := st.ListPositions(ctx, account.ID)
	if err != nil {
		t.Fatalf("positions: %v", err)
	}
	for _, p := range positions {
		if p.TradingSymbol == "TESTSTOCK" {
			if p.Quantity != 0 {
				t.Fatalf("position should be flat, holds %d", p.Quantity)
			}
			if !p.RealizedPnL.Equal(decimal.RequireFromString("-6000")) {
				t.Fatalf("realised = %s, want -6000", p.RealizedPnL)
			}
		}
	}
}

func TestTargetFillCancelsStopAndBooksProfit(t *testing.T) {
	market := &scriptedMarket{}
	market.set("1000")
	engine, st, account := newTestEngine(t, market)
	ctx := context.Background()

	stop := decimal.RequireFromString("950")
	target := decimal.RequireFromString("1080")
	buy, err := engine.PlaceOrder(ctx, account, OrderRequest{
		TradingSymbol:   "TESTSTOCK",
		TransactionType: "BUY",
		OrderType:       "MARKET",
		Quantity:        100,
		StopLoss:        &stop,
		Target:          &target,
	})
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	// Rally through the target. The LIMIT sell fills at the better of limit
	// and market: max(1090, 1080) = 1090.
	market.set("1090")
	if _, err := engine.MatchOpenOrders(ctx, account.ID); err != nil {
		t.Fatalf("match: %v", err)
	}

	orders, err := st.ListOrders(ctx, account.ID, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var profitBooked, slCancelled bool
	for _, o := range orders {
		if o.OCOGroup == nil || *o.OCOGroup != buy.OrderRef {
			continue
		}
		switch o.OrderType {
		case "LIMIT":
			if o.Status == "FILLED" && o.FillPrice.Equal(decimal.RequireFromString("1090")) {
				profitBooked = true
			}
		case "SL":
			if o.Status == "CANCELLED" {
				slCancelled = true
			}
		}
	}
	if !profitBooked {
		t.Fatal("target should have filled at 1090")
	}
	if !slCancelled {
		t.Fatal("stop should have been cancelled as the OCO sibling")
	}
}

func TestRestingBracketBuySpawnsExitsOnLaterFill(t *testing.T) {
	market := &scriptedMarket{}
	market.set("1000")
	engine, st, account := newTestEngine(t, market)
	ctx := context.Background()

	stop := decimal.RequireFromString("900")
	target := decimal.RequireFromString("1100")
	limit := decimal.RequireFromString("980")

	buy, err := engine.PlaceOrder(ctx, account, OrderRequest{
		TradingSymbol:   "TESTSTOCK",
		TransactionType: "BUY",
		OrderType:       "LIMIT",
		Quantity:        50,
		LimitPrice:      &limit,
		StopLoss:        &stop,
		Target:          &target,
	})
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	if buy.Status != "OPEN" {
		t.Fatalf("limit below market should rest, got %s", buy.Status)
	}

	// Price dips to the limit; the buy fills and the bracket must spawn then.
	market.set("975")
	if _, err := engine.MatchOpenOrders(ctx, account.ID); err != nil {
		t.Fatalf("match: %v", err)
	}

	orders, err := st.ListOrders(ctx, account.ID, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	exits := 0
	for _, o := range orders {
		if o.OCOGroup != nil && *o.OCOGroup == buy.OrderRef && o.Status == "OPEN" {
			exits++
		}
	}
	if exits != 2 {
		t.Fatalf("expected 2 resting exits after the deferred fill, got %d", exits)
	}
}
