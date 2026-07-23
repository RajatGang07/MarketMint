// Package marketdata defines the price feed the platform trades against, and
// the chain that picks whichever source is actually working.
//
// Three sources exist today:
//
//   - groww  — the real thing, but /live-data and /historical need a Live Data
//     subscription on the account; without it they answer 403.
//   - yahoo  — public NSE/BSE prices, no key required. The pragmatic live feed
//     when the Groww subscription is not in place.
//   - mock   — a deterministic simulator so the platform is always usable,
//     including outside market hours.
package marketdata

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

// Quote is a normalised snapshot of one instrument.
type Quote struct {
	Symbol    string          `json:"symbol"`
	Exchange  string          `json:"exchange"`
	LastPrice decimal.Decimal `json:"last_price"`
	Open      decimal.Decimal `json:"open"`
	High      decimal.Decimal `json:"high"`
	Low       decimal.Decimal `json:"low"`
	// Close is the *previous* session's close, which is what day-change is
	// measured against.
	Close  decimal.Decimal `json:"close"`
	Volume decimal.Decimal `json:"volume"`
}

// Change is the absolute move since the previous close.
func (q Quote) Change() decimal.Decimal {
	if q.Close.IsZero() {
		return decimal.Zero
	}
	return q.LastPrice.Sub(q.Close)
}

// ChangePct is the day move in percent.
func (q Quote) ChangePct() decimal.Decimal {
	if q.Close.IsZero() {
		return decimal.Zero
	}
	return q.Change().Div(q.Close).Mul(decimal.NewFromInt(100))
}

// Candle is one OHLCV bar.
type Candle struct {
	Time   time.Time       `json:"time"`
	Open   decimal.Decimal `json:"open"`
	High   decimal.Decimal `json:"high"`
	Low    decimal.Decimal `json:"low"`
	Close  decimal.Decimal `json:"close"`
	Volume decimal.Decimal `json:"volume"`
}

// CandleRequest asks for history. Interval is in minutes; 1440 means daily.
type CandleRequest struct {
	Exchange        string
	Segment         string
	Symbol          string
	IntervalMinutes int
	Start           time.Time
	End             time.Time
}

// Provider is one price source.
type Provider interface {
	// Name identifies the source in /health and in logs.
	Name() string
	LTP(ctx context.Context, exchange, segment, symbol string) (decimal.Decimal, error)
	Quote(ctx context.Context, exchange, segment, symbol string) (Quote, error)
	Candles(ctx context.Context, req CandleRequest) ([]Candle, error)
}

// ErrForbidden means the credentials are valid but the account is not entitled
// to this data. Groww sells Live Data separately, so an order-only API key hits
// this on every price call.
var ErrForbidden = errors.New("market data not permitted for these credentials")

// ErrUnsupported is returned by a provider that cannot serve a particular call
// (e.g. history from a quote-only source). It does not mark the provider
// unhealthy — the chain simply moves on.
var ErrUnsupported = errors.New("market data operation not supported by this provider")

// ---------------------------------------------------------------------------
// Chain
// ---------------------------------------------------------------------------

// Chain tries providers in order and remembers which ones are failing, so a
// dead source is skipped for a cool-off window instead of being retried on
// every tick. The last provider is expected to be the simulator, which never
// fails — that is what keeps the platform usable.
type Chain struct {
	providers []Provider
	log       *slog.Logger
	coolOff   time.Duration

	mu     sync.RWMutex
	health map[string]*providerHealth
	active string
}

type providerHealth struct {
	failing   bool
	reason    string
	nextProbe time.Time
}

func NewChain(log *slog.Logger, providers ...Provider) *Chain {
	c := &Chain{
		providers: providers,
		log:       log,
		coolOff:   2 * time.Minute,
		health:    make(map[string]*providerHealth, len(providers)),
	}
	for _, p := range providers {
		c.health[p.Name()] = &providerHealth{}
	}
	if len(providers) > 0 {
		c.active = providers[0].Name()
	}
	return c
}

// Active names the provider that most recently served a request.
func (c *Chain) Active() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.active
}

// Status reports, per provider, whether it is currently usable and why not.
// Ordered as the chain tries them.
func (c *Chain) Status() []ProviderStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]ProviderStatus, 0, len(c.providers))
	for _, p := range c.providers {
		h := c.health[p.Name()]
		out = append(out, ProviderStatus{
			Name:    p.Name(),
			Healthy: !h.failing,
			Reason:  h.reason,
			Active:  p.Name() == c.active,
		})
	}
	return out
}

// ProviderStatus is the wire shape of one source's health.
type ProviderStatus struct {
	Name    string `json:"name"`
	Healthy bool   `json:"healthy"`
	Reason  string `json:"reason,omitempty"`
	Active  bool   `json:"active"`
}

func (c *Chain) shouldTry(name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	h := c.health[name]
	return !h.failing || time.Now().After(h.nextProbe)
}

func (c *Chain) markFailed(name string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := c.health[name]
	if !h.failing {
		c.log.Warn("market data provider unavailable", "provider", name, "err", err)
	}
	h.failing = true
	h.reason = err.Error()
	h.nextProbe = time.Now().Add(c.coolOff)
}

func (c *Chain) markOK(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := c.health[name]
	if h.failing {
		c.log.Info("market data provider recovered", "provider", name)
	}
	h.failing = false
	h.reason = ""
	c.active = name
}

// run walks the chain until one provider answers.
func run[T any](c *Chain, call func(Provider) (T, error)) (T, error) {
	var zero T
	var lastErr error

	for _, p := range c.providers {
		if !c.shouldTry(p.Name()) {
			continue
		}
		out, err := call(p)
		switch {
		case err == nil:
			c.markOK(p.Name())
			return out, nil
		case errors.Is(err, ErrUnsupported):
			// Not a failure of the provider, just a gap in what it offers.
			lastErr = err
		default:
			c.markFailed(p.Name(), err)
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no market data provider available")
	}
	return zero, lastErr
}

func (c *Chain) Name() string { return "chain" }

func (c *Chain) LTP(ctx context.Context, exchange, segment, symbol string) (decimal.Decimal, error) {
	return run(c, func(p Provider) (decimal.Decimal, error) {
		return p.LTP(ctx, exchange, segment, symbol)
	})
}

func (c *Chain) Quote(ctx context.Context, exchange, segment, symbol string) (Quote, error) {
	return run(c, func(p Provider) (Quote, error) {
		return p.Quote(ctx, exchange, segment, symbol)
	})
}

func (c *Chain) Candles(ctx context.Context, req CandleRequest) ([]Candle, error) {
	return run(c, func(p Provider) ([]Candle, error) {
		return p.Candles(ctx, req)
	})
}

// Normalise upper-cases and trims an instrument key. Every provider funnels
// through it so cache keys and API calls agree.
func Normalise(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
