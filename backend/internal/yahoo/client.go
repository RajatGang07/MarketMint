// Package yahoo reads live NSE/BSE prices from Yahoo Finance's public chart
// endpoint. No API key, no subscription — which makes it the practical live
// feed while the Groww account lacks a Live Data entitlement.
//
// One endpoint serves both needs: /v8/finance/chart returns the intraday bars
// *and* a meta block with the current price and previous close, so a quote and
// a chart cost a single request.
package yahoo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/gangrajat/groww-paper-trading/backend/internal/marketdata"
)

// userAgent matters: Yahoo rejects requests that look automated.
const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// Client fetches from Yahoo, with a short TTL cache so a dashboard polling a
// ten-symbol watchlist every few seconds doesn't become ten requests a tick.
type Client struct {
	baseURL string
	http    *http.Client
	ttl     time.Duration

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	result  chartResult
	fetched time.Time
}

func NewClient() *Client {
	return &Client{
		baseURL: "https://query1.finance.yahoo.com/v8/finance/chart",
		http:    &http.Client{Timeout: 10 * time.Second},
		ttl:     3 * time.Second,
		cache:   make(map[string]cacheEntry),
	}
}

func (c *Client) Name() string { return "yahoo" }

// yahooSymbol maps an Indian exchange + trading symbol onto Yahoo's ticker.
func yahooSymbol(exchange, symbol string) string {
	suffix := ".NS"
	if marketdata.Normalise(exchange) == "BSE" {
		suffix = ".BO"
	}
	return marketdata.Normalise(symbol) + suffix
}

// ---------------------------------------------------------------------------
// Wire types
// ---------------------------------------------------------------------------

type chartResponse struct {
	Chart struct {
		Result []chartResult `json:"result"`
		Error  *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

type chartResult struct {
	Meta struct {
		Symbol               string  `json:"symbol"`
		Currency             string  `json:"currency"`
		RegularMarketPrice   float64 `json:"regularMarketPrice"`
		PreviousClose        float64 `json:"previousClose"`
		ChartPreviousClose   float64 `json:"chartPreviousClose"`
		RegularMarketDayHigh float64 `json:"regularMarketDayHigh"`
		RegularMarketDayLow  float64 `json:"regularMarketDayLow"`
		RegularMarketVolume  float64 `json:"regularMarketVolume"`
		RegularMarketDayOpen float64 `json:"regularMarketOpen"`
	} `json:"meta"`
	Timestamp  []int64 `json:"timestamp"`
	Indicators struct {
		Quote []struct {
			Open   []*float64 `json:"open"`
			High   []*float64 `json:"high"`
			Low    []*float64 `json:"low"`
			Close  []*float64 `json:"close"`
			Volume []*float64 `json:"volume"`
		} `json:"quote"`
	} `json:"indicators"`
}

// ---------------------------------------------------------------------------
// Fetch
// ---------------------------------------------------------------------------

func (c *Client) fetch(ctx context.Context, symbol, rng, interval string) (chartResult, error) {
	key := symbol + "|" + rng + "|" + interval

	c.mu.Lock()
	if hit, ok := c.cache[key]; ok && time.Since(hit.fetched) < c.ttl {
		c.mu.Unlock()
		return hit.result, nil
	}
	c.mu.Unlock()

	endpoint := fmt.Sprintf("%s/%s?%s", c.baseURL, url.PathEscape(symbol), url.Values{
		"range":    {rng},
		"interval": {interval},
	}.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return chartResult{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return chartResult{}, fmt.Errorf("yahoo: %s: %w", symbol, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return chartResult{}, fmt.Errorf("yahoo: %s returned %d: %s", symbol, resp.StatusCode, truncate(raw, 200))
	}

	var body chartResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		return chartResult{}, fmt.Errorf("yahoo: decode %s: %w", symbol, err)
	}
	if body.Chart.Error != nil {
		return chartResult{}, fmt.Errorf("yahoo: %s: %s", symbol, body.Chart.Error.Description)
	}
	if len(body.Chart.Result) == 0 {
		return chartResult{}, fmt.Errorf("yahoo: no data for %s", symbol)
	}

	result := body.Chart.Result[0]
	c.mu.Lock()
	// Evict stale entries opportunistically: chart payloads are large and a
	// small always-on instance must not accumulate every symbol it ever saw.
	if len(c.cache) > 64 {
		for k, hit := range c.cache {
			if time.Since(hit.fetched) > 10*time.Minute {
				delete(c.cache, k)
			}
		}
	}
	c.cache[key] = cacheEntry{result: result, fetched: time.Now()}
	c.mu.Unlock()
	return result, nil
}

// ---------------------------------------------------------------------------
// Provider
// ---------------------------------------------------------------------------

func (c *Client) LTP(ctx context.Context, exchange, segment, symbol string) (decimal.Decimal, error) {
	q, err := c.Quote(ctx, exchange, segment, symbol)
	if err != nil {
		return decimal.Zero, err
	}
	return q.LastPrice, nil
}

func (c *Client) Quote(ctx context.Context, exchange, _, symbol string) (marketdata.Quote, error) {
	// A one-day, one-minute chart is the cheapest call that carries a full
	// meta block.
	res, err := c.fetch(ctx, yahooSymbol(exchange, symbol), "1d", "1m")
	if err != nil {
		return marketdata.Quote{}, err
	}
	if res.Meta.RegularMarketPrice == 0 {
		return marketdata.Quote{}, fmt.Errorf("yahoo: no price for %s", symbol)
	}

	prevClose := res.Meta.PreviousClose
	if prevClose == 0 {
		prevClose = res.Meta.ChartPreviousClose
	}

	// Yahoo's meta block doesn't always carry the day's open; the first bar of
	// the intraday series in the same response does.
	dayOpen := res.Meta.RegularMarketDayOpen
	if dayOpen == 0 {
		dayOpen = firstBarOpen(res)
	}

	return marketdata.Quote{
		Symbol:    marketdata.Normalise(symbol),
		Exchange:  marketdata.Normalise(exchange),
		LastPrice: f(res.Meta.RegularMarketPrice),
		Open:      f(dayOpen),
		High:      f(res.Meta.RegularMarketDayHigh),
		Low:       f(res.Meta.RegularMarketDayLow),
		Close:     f(prevClose),
		Volume:    f(res.Meta.RegularMarketVolume),
	}, nil
}

func (c *Client) Candles(ctx context.Context, req marketdata.CandleRequest) ([]marketdata.Candle, error) {
	rng, interval := yahooRange(req)

	res, err := c.fetch(ctx, yahooSymbol(req.Exchange, req.Symbol), rng, interval)
	if err != nil {
		return nil, err
	}
	if len(res.Indicators.Quote) == 0 {
		return nil, fmt.Errorf("yahoo: no candles for %s", req.Symbol)
	}

	q := res.Indicators.Quote[0]
	out := make([]marketdata.Candle, 0, len(res.Timestamp))
	for i, ts := range res.Timestamp {
		// Yahoo pads series with nulls for gaps (halts, holidays); skip them.
		if at(q.Close, i) == nil || at(q.Open, i) == nil {
			continue
		}
		out = append(out, marketdata.Candle{
			Time:   time.Unix(ts, 0),
			Open:   f(*at(q.Open, i)),
			High:   fOr(at(q.High, i), *at(q.Open, i)),
			Low:    fOr(at(q.Low, i), *at(q.Open, i)),
			Close:  f(*at(q.Close, i)),
			Volume: fOr(at(q.Volume, i), 0),
		})
	}
	return out, nil
}

// yahooRange translates our (start, end, intervalMinutes) request into the
// range/interval pair Yahoo accepts, snapping to the nearest supported values.
func yahooRange(req marketdata.CandleRequest) (rng, interval string) {
	span := req.End.Sub(req.Start)

	switch {
	case span <= 26*time.Hour:
		rng = "1d"
	case span <= 6*24*time.Hour:
		rng = "5d"
	case span <= 32*24*time.Hour:
		rng = "1mo"
	case span <= 95*24*time.Hour:
		rng = "3mo"
	case span <= 380*24*time.Hour:
		rng = "1y"
	default:
		rng = "5y"
	}

	switch mins := req.IntervalMinutes; {
	case mins <= 1:
		interval = "1m"
	case mins <= 5:
		interval = "5m"
	case mins <= 15:
		interval = "15m"
	case mins <= 30:
		interval = "30m"
	case mins <= 60:
		interval = "60m"
	case mins <= 1440:
		interval = "1d"
	default:
		interval = "1wk"
	}

	// Yahoo caps intraday history: minute bars only go back a week or so.
	if interval == "1m" && rng != "1d" {
		interval = "5m"
	}
	return rng, interval
}

// firstBarOpen digs the session's opening print out of the intraday series.
func firstBarOpen(res chartResult) float64 {
	if len(res.Indicators.Quote) == 0 {
		return 0
	}
	for _, v := range res.Indicators.Quote[0].Open {
		if v != nil {
			return *v
		}
	}
	return 0
}

func at(xs []*float64, i int) *float64 {
	if i >= len(xs) {
		return nil
	}
	return xs[i]
}

func f(v float64) decimal.Decimal {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return decimal.Zero
	}
	return decimal.NewFromFloat(v).Round(2)
}

func fOr(v *float64, fallback float64) decimal.Decimal {
	if v == nil {
		return f(fallback)
	}
	return f(*v)
}

func truncate(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
