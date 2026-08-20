package marketdata

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// stub is a provider whose behaviour the test controls.
type stub struct {
	name  string
	price string
	err   error
	calls int
}

func (s *stub) Name() string { return s.name }

func (s *stub) LTP(context.Context, string, string, string) (decimal.Decimal, error) {
	s.calls++
	if s.err != nil {
		return decimal.Zero, s.err
	}
	return decimal.RequireFromString(s.price), nil
}

func (s *stub) Quote(context.Context, string, string, string) (Quote, error) {
	s.calls++
	if s.err != nil {
		return Quote{}, s.err
	}
	return Quote{Symbol: "X", LastPrice: decimal.RequireFromString(s.price)}, nil
}

func (s *stub) Candles(context.Context, CandleRequest) ([]Candle, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return []Candle{{Close: decimal.RequireFromString(s.price)}}, nil
}

func TestChainFallsThroughToTheNextProvider(t *testing.T) {
	primary := &stub{name: "primary", err: ErrForbidden}
	backup := &stub{name: "backup", price: "101.50"}

	chain := NewChain(quietLogger(), primary, backup)

	got, err := chain.LTP(context.Background(), "NSE", "CASH", "RELIANCE")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Equal(decimal.RequireFromString("101.50")) {
		t.Fatalf("price = %s, want 101.50", got)
	}
	if chain.Active() != "backup" {
		t.Fatalf("active = %q, want backup", chain.Active())
	}
}

func TestChainSkipsAFailedProviderUntilTheCoolOffExpires(t *testing.T) {
	primary := &stub{name: "primary", err: errors.New("boom")}
	backup := &stub{name: "backup", price: "10"}

	chain := NewChain(quietLogger(), primary, backup)
	ctx := context.Background()

	if _, err := chain.LTP(ctx, "NSE", "CASH", "X"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if primary.calls != 1 {
		t.Fatalf("primary calls = %d, want 1", primary.calls)
	}

	// Second call should not retry the failed provider.
	if _, err := chain.LTP(ctx, "NSE", "CASH", "X"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if primary.calls != 1 {
		t.Fatalf("primary was retried during cool-off: calls = %d", primary.calls)
	}

	// Once the window passes it is probed again, and a recovered provider
	// takes back the lead. Wind the probe deadline back rather than sleeping
	// out the real cool-off.
	primary.err = nil
	primary.price = "20"
	chain.health["primary"].nextProbe = time.Now().Add(-time.Second)

	got, err := chain.LTP(ctx, "NSE", "CASH", "X")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Equal(decimal.RequireFromString("20")) {
		t.Fatalf("price = %s, want 20 from the recovered provider", got)
	}
	if chain.Active() != "primary" {
		t.Fatalf("active = %q, want primary after recovery", chain.Active())
	}
}

func TestChainReportsStatusPerProvider(t *testing.T) {
	primary := &stub{name: "primary", err: ErrForbidden}
	backup := &stub{name: "backup", price: "1"}
	chain := NewChain(quietLogger(), primary, backup)

	if _, err := chain.LTP(context.Background(), "NSE", "CASH", "X"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	status := chain.Status()
	if len(status) != 2 {
		t.Fatalf("status entries = %d, want 2", len(status))
	}
	if status[0].Healthy || status[0].Reason == "" {
		t.Fatalf("primary should be unhealthy with a reason, got %+v", status[0])
	}
	if !status[1].Healthy || !status[1].Active {
		t.Fatalf("backup should be healthy and active, got %+v", status[1])
	}
}

// ErrUnsupported means "this source can't answer that", which must not mark the
// provider unhealthy — it should still serve the calls it does support.
func TestChainDoesNotPenaliseUnsupportedCalls(t *testing.T) {
	first := &stub{name: "first", err: ErrUnsupported}
	second := &stub{name: "second", err: ErrUnsupported}
	chain := NewChain(quietLogger(), first, second)

	if _, err := chain.Candles(context.Background(), CandleRequest{Symbol: "X"}); err == nil {
		t.Fatal("expected an error when no provider can serve candles")
	}
	for _, status := range chain.Status() {
		if !status.Healthy {
			t.Fatalf("ErrUnsupported must not mark %s unhealthy", status.Name)
		}
	}

	// A provider that only declines history still serves the calls it supports.
	first.err = nil
	first.price = "7"
	got, err := chain.LTP(context.Background(), "NSE", "CASH", "X")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Equal(decimal.RequireFromString("7")) {
		t.Fatalf("price = %s, want 7", got)
	}
}

func TestQuoteChangeIsMeasuredAgainstPreviousClose(t *testing.T) {
	q := Quote{
		LastPrice: decimal.RequireFromString("110"),
		Close:     decimal.RequireFromString("100"),
	}
	if got := q.Change(); !got.Equal(decimal.RequireFromString("10")) {
		t.Fatalf("change = %s, want 10", got)
	}
	if got := q.ChangePct(); !got.Equal(decimal.RequireFromString("10")) {
		t.Fatalf("change pct = %s, want 10", got)
	}

	// A missing previous close must not produce a divide-by-zero or a bogus
	// infinite percentage.
	empty := Quote{LastPrice: decimal.RequireFromString("110")}
	if !empty.Change().IsZero() || !empty.ChangePct().IsZero() {
		t.Fatal("change against a zero previous close should be zero, not nonsense")
	}
}

func TestMockCandlesAreDeterministic(t *testing.T) {
	m := NewMock()
	end := time.Now()
	req := CandleRequest{Symbol: "RELIANCE", IntervalMinutes: 5, Start: end.Add(-time.Hour), End: end}

	first, err := m.Candles(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := m.Candles(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(first) == 0 {
		t.Fatal("expected candles for a one-hour window")
	}
	for i := range first {
		if !first[i].Close.Equal(second[i].Close) {
			t.Fatalf("bar %d differs between calls: %s vs %s", i, first[i].Close, second[i].Close)
		}
	}
}

// While a real provider is configured, NOTHING prices off the simulator —
// not fills, not quotes, not charts, not history. A feed outage must read
// as an error, never as plausible fake numbers (APARINDS once showed the
// simulator's hash-derived ~1,920 instead of its real ~16,700).
func TestChainNeverPricesOffTheSimulator(t *testing.T) {
	dead := &stub{name: "yahoo", err: errors.New("rate limited")}
	sim := &stub{name: SimulatorName, price: "3428.75"} // the fantasy price
	chain := NewChain(quietLogger(), dead, sim)
	chain.coolOff = 0 // re-probe immediately so recovery is visible in-test
	ctx := context.Background()

	if _, err := chain.LTP(ctx, "NSE", "CASH", "NAUKRI"); err == nil {
		t.Fatal("LTP must not fall through to simulated prices")
	}
	if _, err := chain.LTPLive(ctx, "NSE", "CASH", "NAUKRI"); err == nil {
		t.Fatal("LTPLive must not fall through to simulated prices")
	}
	if _, err := chain.Quote(ctx, "NSE", "CASH", "NAUKRI"); err == nil {
		t.Fatal("Quote must not fall through to simulated prices")
	}
	if _, err := chain.Candles(ctx, CandleRequest{Symbol: "NAUKRI"}); err == nil {
		t.Fatal("Candles must not fall through to simulated prices")
	}
	if _, err := chain.CandlesLive(ctx, CandleRequest{Symbol: "NAUKRI"}); err == nil {
		t.Fatal("CandlesLive must not fall through to simulated prices")
	}

	dead.err = nil
	dead.price = "1247.85"
	got, err := chain.LTPLive(ctx, "NSE", "CASH", "NAUKRI")
	if err != nil {
		t.Fatalf("live provider recovered, want price: %v", err)
	}
	if got.String() != "1247.85" {
		t.Fatalf("want the live price, got %s", got)
	}
}

// CandlesLive re-probes a provider that is still inside its failure
// cool-down: history requests are user-triggered, so Retry must actually
// retry instead of failing for the rest of the window.
func TestCandlesLiveReprobesACoolingProvider(t *testing.T) {
	flaky := &stub{name: "yahoo", err: errors.New("rate limited")}
	chain := NewChain(quietLogger(), flaky)
	ctx := context.Background()

	if _, err := chain.Candles(ctx, CandleRequest{Symbol: "X"}); err == nil {
		t.Fatal("expected the first call to fail")
	}
	flaky.err = nil
	flaky.price = "42"

	// The polled path waits out the cool-down…
	if _, err := chain.Candles(ctx, CandleRequest{Symbol: "X"}); err == nil {
		t.Fatal("Candles should still be cooling off")
	}
	// …the user-triggered path does not.
	got, err := chain.CandlesLive(ctx, CandleRequest{Symbol: "X"})
	if err != nil {
		t.Fatalf("CandlesLive must bypass the cool-down: %v", err)
	}
	if len(got) != 1 || got[0].Close.String() != "42" {
		t.Fatalf("want the recovered provider's bar, got %+v", got)
	}
}

func TestSimulatorOnlySetupStillWorks(t *testing.T) {
	sim := &stub{name: SimulatorName, price: "100"}
	chain := NewChain(quietLogger(), sim)
	ctx := context.Background()

	if got, err := chain.LTPLive(ctx, "NSE", "CASH", "X"); err != nil || got.String() != "100" {
		t.Fatalf("simulator-only chain is the price authority, got %s err=%v", got, err)
	}
	if q, err := chain.Quote(ctx, "NSE", "CASH", "X"); err != nil || q.LastPrice.String() != "100" {
		t.Fatalf("simulator-only Quote should serve, got %+v err=%v", q, err)
	}
	if bars, err := chain.Candles(ctx, CandleRequest{Symbol: "X"}); err != nil || len(bars) != 1 {
		t.Fatalf("simulator-only Candles should serve, got %d bars err=%v", len(bars), err)
	}
}
