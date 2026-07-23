package instruments

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

const sampleCSV = `exchange,exchange_token,trading_symbol,groww_symbol,name,instrument_type,segment,series,isin,underlying_symbol,underlying_exchange_token,expiry_date,strike_price,lot_size,tick_size,freeze_quantity,is_reserved,buy_allowed,sell_allowed,internal_trading_symbol,is_intraday
NSE,2885,RELIANCE,NSE-RELIANCE,Reliance Industries,EQ,CASH,EQ,INE002A01018,,,,,1,0.1,,,1,1,RELIANCE-EQ,1
NSE,1234,RELAXO,NSE-RELAXO,Relaxo Footwears,EQ,CASH,EQ,INE131B01039,,,,,1,0.05,,,1,1,RELAXO-EQ,1
NSE,9999,RELCHEMQ,NSE-RELCHEMQ,Reliance Chemo,EQ,CASH,EQ,INE750D01016,,,,,1,0.01,,,1,1,RELCHEMQ-EQ,1
BSE,500325,RELIANCE,BSE-RELIANCE,Reliance Industries,EQ,CASH,A,INE002A01018,,,,,1,0.05,,,1,1,RELIANCE,1
NSE,66825,RELIANCE26AUG1080CE,NSE-RELIANCE-Aug26,,CE,FNO,,,RELIANCE,2885,2026-08-25,1080,500,0.05,20001,0,1,1,RELIANCE26AUG1080CE,0
NSE,4321,7RELBOND30,NSE-7RELBOND30,Reliance Bond 2030,EQ,CASH,N1,INE002A07AB1,,,,,1,0.01,,,1,1,7RELBOND30,0
`

func loadTestStore(t *testing.T) *Store {
	t.Helper()
	s := New(Options{Segments: []string{"CASH"}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := s.parse(strings.NewReader(sampleCSV)); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return s
}

func TestParseKeepsOnlyConfiguredSegments(t *testing.T) {
	s := loadTestStore(t)

	// 4 NSE cash rows + 1 BSE cash row; the F&O contract is excluded.
	if got := s.Count(); got != 5 {
		t.Fatalf("count = %d, want 5 (F&O rows must be filtered out)", got)
	}
	for _, inst := range s.all {
		if inst.Segment != "CASH" {
			t.Fatalf("non-cash instrument survived the filter: %+v", inst)
		}
	}
}

func TestLookupIsExchangeScoped(t *testing.T) {
	s := loadTestStore(t)

	nse, ok := s.Lookup("NSE", "RELIANCE")
	if !ok || nse.TickSize != "0.1" {
		t.Fatalf("NSE lookup = %+v, ok=%v", nse, ok)
	}
	bse, ok := s.Lookup("BSE", "RELIANCE")
	if !ok || bse.Series != "A" {
		t.Fatalf("BSE lookup = %+v, ok=%v", bse, ok)
	}
	if _, ok := s.Lookup("NSE", "NOSUCHTICKER"); ok {
		t.Fatal("expected miss for an unknown symbol")
	}
}

// A prefix query matches several tickers; the one with listed derivatives is
// the household name and should lead.
func TestSearchRanksTheLiquidNameFirst(t *testing.T) {
	s := loadTestStore(t)

	got := s.Search("REL", "NSE", 10)
	if len(got) == 0 {
		t.Fatal("expected matches for REL")
	}
	if got[0].TradingSymbol != "RELIANCE" {
		t.Fatalf("first hit = %s, want RELIANCE", got[0].TradingSymbol)
	}
}

func TestSearchMatchesCompanyName(t *testing.T) {
	s := loadTestStore(t)

	got := s.Search("relaxo foot", "NSE", 5)
	if len(got) == 0 || got[0].TradingSymbol != "RELAXO" {
		t.Fatalf("name search returned %+v", got)
	}
}

func TestSearchFiltersByExchange(t *testing.T) {
	s := loadTestStore(t)

	for _, inst := range s.Search("RELIANCE", "NSE", 10) {
		if inst.Exchange != "NSE" {
			t.Fatalf("BSE row leaked into an NSE-scoped search: %+v", inst)
		}
	}
	if got := s.Search("RELIANCE", "", 10); len(got) < 2 {
		t.Fatalf("unscoped search should return both listings, got %d", len(got))
	}
}

func TestSearchIgnoresBlankQueries(t *testing.T) {
	s := loadTestStore(t)
	if got := s.Search("   ", "NSE", 10); got != nil {
		t.Fatalf("blank query returned %d results, want none", len(got))
	}
}

func TestSearchRespectsTheLimit(t *testing.T) {
	s := loadTestStore(t)
	if got := s.Search("REL", "NSE", 2); len(got) != 2 {
		t.Fatalf("limit not applied: got %d results", len(got))
	}
}
