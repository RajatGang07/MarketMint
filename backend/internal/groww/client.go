// Package groww talks to Groww's Trading API: token minting plus the
// market-data endpoints.
//
// Heads up: /live-data and /historical require a Live Data subscription on the
// Groww account. An order-only API key authenticates fine and then gets 403 on
// every price call, which surfaces here as marketdata.ErrForbidden.
package groww

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/gangrajat/groww-paper-trading/backend/internal/marketdata"
)

// Client is a thin REST client over Groww's market-data endpoints.
type Client struct {
	baseURL string
	tokens  *TokenSource
	http    *http.Client
}

func NewClient(baseURL string, tokens *TokenSource) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		tokens:  tokens,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) Name() string { return "groww" }

type envelope struct {
	Status  string          `json:"status"`
	Payload json.RawMessage `json:"payload"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// get performs an authenticated GET and unwraps Groww's {status, payload}
// envelope. A 401 triggers exactly one token refresh + retry.
func (c *Client) get(ctx context.Context, path string, q url.Values) (json.RawMessage, error) {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.tokens.Token(ctx)
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-API-VERSION", "1.0")

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("groww: %s: %w", path, err)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusUnauthorized && attempt == 0:
			c.tokens.Invalidate()
			continue
		case resp.StatusCode == http.StatusForbidden:
			return nil, marketdata.ErrForbidden
		case resp.StatusCode != http.StatusOK:
			return nil, fmt.Errorf("groww: %s returned %d: %s", path, resp.StatusCode, truncate(raw, 300))
		}

		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("groww: decode %s: %w", path, err)
		}
		if env.Error != nil {
			if env.Error.Code == "403" {
				return nil, marketdata.ErrForbidden
			}
			return nil, fmt.Errorf("groww: %s: %s", path, env.Error.Message)
		}
		return env.Payload, nil
	}
	return nil, fmt.Errorf("groww: %s: authentication kept failing", path)
}

// exchangeSymbol is the composite key Groww's bulk endpoints use.
func exchangeSymbol(exchange, symbol string) string {
	return marketdata.Normalise(exchange) + "_" + marketdata.Normalise(symbol)
}

func (c *Client) LTP(ctx context.Context, exchange, segment, symbol string) (decimal.Decimal, error) {
	payload, err := c.get(ctx, "/live-data/ltp", url.Values{
		"segment":          {marketdata.Normalise(segment)},
		"exchange_symbols": {exchangeSymbol(exchange, symbol)},
	})
	if err != nil {
		return decimal.Zero, err
	}

	// Payload is a map keyed by "NSE_RELIANCE"; we asked for exactly one.
	var prices map[string]decimal.Decimal
	if err := json.Unmarshal(payload, &prices); err != nil {
		return decimal.Zero, fmt.Errorf("groww: decode ltp payload: %w", err)
	}
	if p, ok := prices[exchangeSymbol(exchange, symbol)]; ok {
		return p, nil
	}
	for _, p := range prices {
		return p, nil
	}
	return decimal.Zero, fmt.Errorf("groww: no price returned for %s", symbol)
}

func (c *Client) Quote(ctx context.Context, exchange, segment, symbol string) (marketdata.Quote, error) {
	payload, err := c.get(ctx, "/live-data/quote", url.Values{
		"exchange":       {marketdata.Normalise(exchange)},
		"segment":        {marketdata.Normalise(segment)},
		"trading_symbol": {marketdata.Normalise(symbol)},
	})
	if err != nil {
		return marketdata.Quote{}, err
	}

	var body struct {
		LastPrice decimal.Decimal `json:"last_price"`
		Volume    decimal.Decimal `json:"volume"`
		OHLC      struct {
			Open  decimal.Decimal `json:"open"`
			High  decimal.Decimal `json:"high"`
			Low   decimal.Decimal `json:"low"`
			Close decimal.Decimal `json:"close"`
		} `json:"ohlc"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return marketdata.Quote{}, fmt.Errorf("groww: decode quote payload: %w", err)
	}

	return marketdata.Quote{
		Symbol:    marketdata.Normalise(symbol),
		Exchange:  marketdata.Normalise(exchange),
		LastPrice: body.LastPrice,
		Open:      body.OHLC.Open,
		High:      body.OHLC.High,
		Low:       body.OHLC.Low,
		Close:     body.OHLC.Close,
		Volume:    body.Volume,
	}, nil
}

// Candles maps onto /historical/candle/range. Groww returns bars as positional
// arrays: [epochSeconds, open, high, low, close, volume].
func (c *Client) Candles(ctx context.Context, req marketdata.CandleRequest) ([]marketdata.Candle, error) {
	const layout = "2006-01-02T15:04:05"

	payload, err := c.get(ctx, "/historical/candle/range", url.Values{
		"exchange":            {marketdata.Normalise(req.Exchange)},
		"segment":             {marketdata.Normalise(req.Segment)},
		"trading_symbol":      {marketdata.Normalise(req.Symbol)},
		"start_time":          {req.Start.Format(layout)},
		"end_time":            {req.End.Format(layout)},
		"interval_in_minutes": {strconv.Itoa(req.IntervalMinutes)},
	})
	if err != nil {
		return nil, err
	}

	var body struct {
		Candles [][]json.Number `json:"candles"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, fmt.Errorf("groww: decode candles payload: %w", err)
	}

	out := make([]marketdata.Candle, 0, len(body.Candles))
	for _, row := range body.Candles {
		if len(row) < 5 {
			continue
		}
		epoch, err := row[0].Int64()
		if err != nil {
			continue
		}
		candle := marketdata.Candle{
			Time:  time.Unix(epoch, 0),
			Open:  num(row[1]),
			High:  num(row[2]),
			Low:   num(row[3]),
			Close: num(row[4]),
		}
		if len(row) > 5 {
			candle.Volume = num(row[5])
		}
		out = append(out, candle)
	}
	return out, nil
}

func num(n json.Number) decimal.Decimal {
	d, err := decimal.NewFromString(n.String())
	if err != nil {
		return decimal.Zero
	}
	return d
}
