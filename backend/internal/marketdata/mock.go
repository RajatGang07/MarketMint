package marketdata

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"math/rand"
	"time"

	"github.com/shopspring/decimal"
)

// Mock is a deterministic price simulator and the last link in the chain: it
// never fails, so the platform stays usable outside market hours and without a
// market-data subscription.
//
// Each symbol gets a stable base price (well-known NSE tickers use realistic
// values, everything else is derived from a hash of the name) and moves on a
// slow sine of about ±1% with a little tick noise on top — enough that resting
// limit orders actually get hit and charts have shape.
type Mock struct{}

func NewMock() *Mock { return &Mock{} }

func (*Mock) Name() string { return "simulator" }

var mockBase = map[string]float64{
	"RELIANCE": 2900, "TCS": 3850, "INFY": 1650, "HDFCBANK": 1700,
	"SBIN": 820, "ICICIBANK": 1250, "WIPRO": 480, "ITC": 460,
	"HINDUNILVR": 2450, "BHARTIARTL": 1600, "KALYANKJIL": 590,
	"AXISBANK": 1150, "MARUTI": 12800, "TATAMOTORS": 980, "LT": 3600,
	"NIFTY": 24500, "BANKNIFTY": 52000,
}

func basePrice(symbol string) float64 {
	symbol = Normalise(symbol)
	if p, ok := mockBase[symbol]; ok {
		return p
	}
	sum := sha256.Sum256([]byte(symbol))
	return 100 + float64(binary.BigEndian.Uint64(sum[:8])%4000)
}

// symbolPhase spreads symbols across the drift cycle so they don't all move in
// lockstep.
func symbolPhase(symbol string) float64 {
	sum := sha256.Sum256([]byte(Normalise(symbol)))
	return float64(binary.BigEndian.Uint32(sum[8:12])%1000) / 1000 * 2 * math.Pi
}

// driftAt is the deterministic component of the price at a point in time. Both
// live prices and historical candles are built from it, so a chart and the
// current LTP always tell the same story.
func driftAt(symbol string, t time.Time) float64 {
	base := basePrice(symbol)
	seconds := float64(t.UnixNano()) / 1e9
	return base * (1 + math.Sin(seconds/30+symbolPhase(symbol))*0.01)
}

func (m *Mock) price(symbol string) decimal.Decimal {
	noise := (rand.Float64() - 0.5) * 0.002
	return decimal.NewFromFloat(driftAt(symbol, time.Now()) * (1 + noise)).Round(2)
}

func (m *Mock) LTP(_ context.Context, _, _, symbol string) (decimal.Decimal, error) {
	return m.price(symbol), nil
}

func (m *Mock) Quote(_ context.Context, exchange, _, symbol string) (Quote, error) {
	last := m.price(symbol)
	// Treat "yesterday's close" as the plain base price, so day-change swings
	// through zero across the drift cycle the way a real ticker would.
	prevClose := decimal.NewFromFloat(basePrice(symbol)).Round(2)

	dayOpen := decimal.NewFromFloat(driftAt(symbol, startOfDay(time.Now()))).Round(2)
	high, low := decimal.Max(dayOpen, last), decimal.Min(dayOpen, last)

	return Quote{
		Symbol:    Normalise(symbol),
		Exchange:  Normalise(exchange),
		LastPrice: last,
		Open:      dayOpen,
		High:      high.Mul(decimal.NewFromFloat(1.004)).Round(2),
		Low:       low.Mul(decimal.NewFromFloat(0.996)).Round(2),
		Close:     prevClose,
		Volume:    decimal.Zero,
	}, nil
}

// Candles replays driftAt over the requested window, so simulated history is
// stable across reloads rather than re-randomised each time.
func (m *Mock) Candles(_ context.Context, req CandleRequest) ([]Candle, error) {
	step := time.Duration(req.IntervalMinutes) * time.Minute
	if step <= 0 {
		step = 5 * time.Minute
	}
	if !req.End.After(req.Start) {
		return nil, nil
	}
	// Guard against a caller asking for a decade of one-minute bars.
	const maxBars = 1500

	var out []Candle
	for t := req.Start; t.Before(req.End) && len(out) < maxBars; t = t.Add(step) {
		open := driftAt(req.Symbol, t)
		close := driftAt(req.Symbol, t.Add(step))
		mid := driftAt(req.Symbol, t.Add(step/2))

		high := math.Max(math.Max(open, close), mid) * 1.001
		low := math.Min(math.Min(open, close), mid) * 0.999

		out = append(out, Candle{
			Time:   t,
			Open:   decimal.NewFromFloat(open).Round(2),
			High:   decimal.NewFromFloat(high).Round(2),
			Low:    decimal.NewFromFloat(low).Round(2),
			Close:  decimal.NewFromFloat(close).Round(2),
			Volume: decimal.Zero,
		})
	}
	return out, nil
}

func startOfDay(t time.Time) time.Time {
	y, mo, d := t.Date()
	return time.Date(y, mo, d, 9, 15, 0, 0, t.Location()) // NSE open
}
