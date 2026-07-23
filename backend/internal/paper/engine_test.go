package paper

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/gangrajat/groww-paper-trading/backend/internal/store"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func ptr(d decimal.Decimal) *decimal.Decimal { return &d }

func TestFillPrice(t *testing.T) {
	tests := []struct {
		name       string
		order      store.Order
		ltp        string
		want       string
		marketable bool
	}{
		{
			name:  "market buy fills at ltp",
			order: store.Order{OrderType: "MARKET", TransactionType: "BUY"},
			ltp:   "100", want: "100", marketable: true,
		},
		{
			name:  "market sell fills at ltp",
			order: store.Order{OrderType: "MARKET", TransactionType: "SELL"},
			ltp:   "100", want: "100", marketable: true,
		},
		{
			name:  "limit buy above market fills at the market",
			order: store.Order{OrderType: "LIMIT", TransactionType: "BUY", LimitPrice: ptr(dec("110"))},
			ltp:   "100", want: "100", marketable: true,
		},
		{
			name:  "limit buy below market rests",
			order: store.Order{OrderType: "LIMIT", TransactionType: "BUY", LimitPrice: ptr(dec("90"))},
			ltp:   "100", marketable: false,
		},
		{
			name:  "limit buy exactly at market fills",
			order: store.Order{OrderType: "LIMIT", TransactionType: "BUY", LimitPrice: ptr(dec("100"))},
			ltp:   "100", want: "100", marketable: true,
		},
		{
			name:  "limit sell below market fills at the market",
			order: store.Order{OrderType: "LIMIT", TransactionType: "SELL", LimitPrice: ptr(dec("90"))},
			ltp:   "100", want: "100", marketable: true,
		},
		{
			name:  "limit sell above market rests",
			order: store.Order{OrderType: "LIMIT", TransactionType: "SELL", LimitPrice: ptr(dec("110"))},
			ltp:   "100", marketable: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, marketable := fillPrice(tc.order, dec(tc.ltp))
			if marketable != tc.marketable {
				t.Fatalf("marketable = %v, want %v", marketable, tc.marketable)
			}
			if marketable && !got.Equal(dec(tc.want)) {
				t.Fatalf("price = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestOrderRequestNormalise(t *testing.T) {
	t.Run("applies defaults and upper-cases", func(t *testing.T) {
		req := OrderRequest{TradingSymbol: " reliance ", TransactionType: "buy", Quantity: 5}
		if err := req.normalise(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.TradingSymbol != "RELIANCE" || req.Exchange != "NSE" ||
			req.Segment != "CASH" || req.Product != "CNC" || req.OrderType != "MARKET" {
			t.Fatalf("defaults not applied: %+v", req)
		}
	})

	t.Run("drops a stray limit price on a market order", func(t *testing.T) {
		req := OrderRequest{TradingSymbol: "TCS", TransactionType: "BUY", Quantity: 1, LimitPrice: ptr(dec("10"))}
		if err := req.normalise(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.LimitPrice != nil {
			t.Fatalf("limit price survived on a MARKET order: %v", req.LimitPrice)
		}
	})

	for _, tc := range []struct {
		name string
		req  OrderRequest
	}{
		{"missing symbol", OrderRequest{TransactionType: "BUY", Quantity: 1}},
		{"bad side", OrderRequest{TradingSymbol: "TCS", TransactionType: "HOLD", Quantity: 1}},
		{"bad order type", OrderRequest{TradingSymbol: "TCS", TransactionType: "BUY", OrderType: "ICEBERG", Quantity: 1}},
		{"zero quantity", OrderRequest{TradingSymbol: "TCS", TransactionType: "BUY", Quantity: 0}},
		{"negative quantity", OrderRequest{TradingSymbol: "TCS", TransactionType: "BUY", Quantity: -3}},
		{"limit without price", OrderRequest{TradingSymbol: "TCS", TransactionType: "BUY", OrderType: "LIMIT", Quantity: 1}},
		{"limit with zero price", OrderRequest{TradingSymbol: "TCS", TransactionType: "BUY", OrderType: "LIMIT", Quantity: 1, LimitPrice: ptr(decimal.Zero)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			var rej RejectError
			if err := req.normalise(); err == nil {
				t.Fatal("expected a rejection, got nil")
			} else if !asReject(err, &rej) {
				t.Fatalf("expected RejectError, got %T", err)
			}
		})
	}
}

func asReject(err error, target *RejectError) bool {
	r, ok := err.(RejectError)
	if ok {
		*target = r
	}
	return ok
}
