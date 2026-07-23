package httpapi

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/gangrajat/groww-paper-trading/backend/internal/marketdata"
	"github.com/gangrajat/groww-paper-trading/backend/internal/store"
)

// orderDTO is the wire shape of an order. Nullable money fields become JSON
// null rather than 0 so the UI can tell "unfilled" from "filled at zero".
type orderDTO struct {
	ID              int64            `json:"id"`
	OrderRef        string           `json:"order_ref"`
	TradingSymbol   string           `json:"trading_symbol"`
	Exchange        string           `json:"exchange"`
	Segment         string           `json:"segment"`
	Product         string           `json:"product"`
	TransactionType string           `json:"transaction_type"`
	OrderType       string           `json:"order_type"`
	Quantity        int64            `json:"quantity"`
	LimitPrice      *decimal.Decimal `json:"limit_price"`
	TriggerPrice    *decimal.Decimal `json:"trigger_price"`
	StopLoss        *decimal.Decimal `json:"stop_loss"`
	Target          *decimal.Decimal `json:"target"`
	TrailBy         *decimal.Decimal `json:"trail_by"`
	OCOGroup        *string          `json:"oco_group"`
	Status          string           `json:"status"`
	FillPrice       *decimal.Decimal `json:"fill_price"`
	FilledQuantity  int64            `json:"filled_quantity"`
	Message         *string          `json:"message"`
	CreatedAt       time.Time        `json:"created_at"`
}

func toOrderDTO(o store.Order) orderDTO {
	return orderDTO{
		ID:              o.ID,
		OrderRef:        o.OrderRef,
		TradingSymbol:   o.TradingSymbol,
		Exchange:        o.Exchange,
		Segment:         o.Segment,
		Product:         o.Product,
		TransactionType: o.TransactionType,
		OrderType:       o.OrderType,
		Quantity:        o.Quantity,
		LimitPrice:      o.LimitPrice,
		TriggerPrice:    o.TriggerPrice,
		StopLoss:        o.StopLoss,
		Target:          o.Target,
		TrailBy:         o.TrailBy,
		OCOGroup:        o.OCOGroup,
		Status:          o.Status,
		FillPrice:       o.FillPrice,
		FilledQuantity:  o.FilledQuantity,
		Message:         o.Message,
		CreatedAt:       o.CreatedAt,
	}
}

func toOrderDTOs(orders []store.Order) []orderDTO {
	out := make([]orderDTO, 0, len(orders))
	for _, o := range orders {
		out = append(out, toOrderDTO(o))
	}
	return out
}

type tradeDTO struct {
	ID              int64           `json:"id"`
	OrderRef        string          `json:"order_ref"`
	TradingSymbol   string          `json:"trading_symbol"`
	TransactionType string          `json:"transaction_type"`
	Quantity        int64           `json:"quantity"`
	Price           decimal.Decimal `json:"price"`
	RealizedPnL     decimal.Decimal `json:"realized_pnl"`
	CreatedAt       time.Time       `json:"created_at"`
}

func toTradeDTOs(trades []store.Trade) []tradeDTO {
	out := make([]tradeDTO, 0, len(trades))
	for _, t := range trades {
		out = append(out, tradeDTO{
			ID:              t.ID,
			OrderRef:        t.OrderRef,
			TradingSymbol:   t.TradingSymbol,
			TransactionType: t.TransactionType,
			Quantity:        t.Quantity,
			Price:           t.Price.Round(2),
			RealizedPnL:     t.RealizedPnL.Round(2),
			CreatedAt:       t.CreatedAt,
		})
	}
	return out
}

type ltpDTO struct {
	Symbol   string          `json:"symbol"`
	Exchange string          `json:"exchange"`
	LTP      decimal.Decimal `json:"ltp"`
}

// quoteDTO is a quote plus the derived day-change and display name.
//
// OK/Error let a bulk watchlist request report a single unpriceable symbol
// without failing the whole response.
type quoteDTO struct {
	Symbol    string          `json:"symbol"`
	Name      string          `json:"name,omitempty"`
	Exchange  string          `json:"exchange"`
	LastPrice decimal.Decimal `json:"last_price"`
	Open      decimal.Decimal `json:"open"`
	High      decimal.Decimal `json:"high"`
	Low       decimal.Decimal `json:"low"`
	Close     decimal.Decimal `json:"close"`
	Volume    decimal.Decimal `json:"volume"`
	Change    decimal.Decimal `json:"change"`
	ChangePct decimal.Decimal `json:"change_pct"`
	OK        bool            `json:"ok"`
	Error     string          `json:"error,omitempty"`
}

type candleDTO struct {
	Time   time.Time       `json:"time"`
	Open   decimal.Decimal `json:"open"`
	High   decimal.Decimal `json:"high"`
	Low    decimal.Decimal `json:"low"`
	Close  decimal.Decimal `json:"close"`
	Volume decimal.Decimal `json:"volume"`
}

func toCandleDTOs(candles []marketdata.Candle) []candleDTO {
	out := make([]candleDTO, 0, len(candles))
	for _, c := range candles {
		out = append(out, candleDTO{
			Time:   c.Time,
			Open:   c.Open,
			High:   c.High,
			Low:    c.Low,
			Close:  c.Close,
			Volume: c.Volume,
		})
	}
	return out
}
