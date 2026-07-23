package analytics

import (
	"math"

	"github.com/shopspring/decimal"
)

// RiskBands is the user's brief: how much a position may lose at the stop and
// how much it should make at the target.
type RiskBands struct {
	LossMin   float64 `json:"loss_min"`
	LossMax   float64 `json:"loss_max"`
	ProfitMin float64 `json:"profit_min"`
	ProfitMax float64 `json:"profit_max"`
}

// DefaultRiskBands encodes the requested envelope: risk ₹20–30k to make
// ₹30–50k.
var DefaultRiskBands = RiskBands{
	LossMin: 20_000, LossMax: 30_000,
	ProfitMin: 30_000, ProfitMax: 50_000,
}

// mid returns the midpoints the sizing aims at.
func (r RiskBands) mid() (loss, profit float64) {
	return (r.LossMin + r.LossMax) / 2, (r.ProfitMin + r.ProfitMax) / 2
}

// Plan is a fully sized trade: entry, exits and the money at stake.
type Plan struct {
	Entry     decimal.Decimal `json:"entry"`
	StopLoss  decimal.Decimal `json:"stop_loss"`
	Target    decimal.Decimal `json:"target"`
	Quantity  int64           `json:"quantity"`
	Capital   decimal.Decimal `json:"capital_required"`
	LossAt    decimal.Decimal `json:"loss_at_stop"`
	ProfitAt  decimal.Decimal `json:"profit_at_target"`
	RiskRatio float64         `json:"risk_reward"`
	// CapitalCapped is set when available cash, not the risk budget, decided
	// the quantity — the loss/profit will then sit below the requested bands.
	CapitalCapped bool `json:"capital_capped"`
}

// stopMultiple and targetMultiple define the exit geometry in ATR units. A
// 2×ATR stop survives ordinary daily noise; the target multiple is derived
// from the user's own bands (₹40k target on ₹25k risk ⇒ 1.6R).
const stopMultiple = 2.0

// buildPlan sizes one candidate against the risk budget and available cash.
// ok=false when no whole-share quantity can satisfy the brief.
func buildPlan(entry, atr float64, bands RiskBands, availableCash float64) (Plan, bool) {
	if entry <= 0 || atr <= 0 {
		return Plan{}, false
	}

	lossMid, profitMid := bands.mid()
	rewardRatio := profitMid / lossMid // 1.6 with the default bands

	stopDist := roundTick(stopMultiple * atr)
	targetDist := roundTick(stopMultiple * atr * rewardRatio)
	if stopDist <= 0 {
		return Plan{}, false
	}

	// Quantity from the risk budget…
	qty := math.Floor(lossMid / stopDist)
	capped := false

	// …then capped by the cash actually available.
	if qty*entry > availableCash {
		qty = math.Floor(availableCash / entry)
		capped = true
	}
	if qty < 1 {
		return Plan{}, false
	}

	// A share that is too coarse to land inside the loss band is rejected
	// rather than silently over-risked (e.g. one share already loses 40k).
	if !capped && qty*stopDist > bands.LossMax {
		return Plan{}, false
	}

	stop := roundTick(entry - stopDist)
	target := roundTick(entry + targetDist)
	if stop <= 0 {
		return Plan{}, false
	}

	return Plan{
		Entry:         dec(entry),
		StopLoss:      dec(stop),
		Target:        dec(target),
		Quantity:      int64(qty),
		Capital:       dec(qty * entry),
		LossAt:        dec(qty * (entry - stop)),
		ProfitAt:      dec(qty * (target - entry)),
		RiskRatio:     rewardRatio,
		CapitalCapped: capped,
	}, true
}

// roundTick snaps a price to the NSE 5-paise tick.
func roundTick(p float64) float64 {
	return math.Round(p*20) / 20
}

func dec(v float64) decimal.Decimal {
	return decimal.NewFromFloat(v).Round(2)
}
