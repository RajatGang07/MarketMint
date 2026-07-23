package analytics

import (
	"math"
	"testing"
)

func f64(t *testing.T, p Plan, pick func(Plan) float64) float64 {
	t.Helper()
	return pick(p)
}

func TestBuildPlanLandsInsideTheRiskBands(t *testing.T) {
	// A typical liquid large-cap: ₹1,250 with a ₹22 ATR.
	plan, ok := buildPlan(1250, 22, DefaultRiskBands, 10_000_000)
	if !ok {
		t.Fatal("expected a plan")
	}

	loss, _ := plan.LossAt.Float64()
	profit, _ := plan.ProfitAt.Float64()

	if loss < DefaultRiskBands.LossMin || loss > DefaultRiskBands.LossMax {
		t.Fatalf("loss at stop %.2f outside [%.0f, %.0f]", loss, DefaultRiskBands.LossMin, DefaultRiskBands.LossMax)
	}
	if profit < DefaultRiskBands.ProfitMin || profit > DefaultRiskBands.ProfitMax {
		t.Fatalf("profit at target %.2f outside [%.0f, %.0f]", profit, DefaultRiskBands.ProfitMin, DefaultRiskBands.ProfitMax)
	}

	stop, _ := plan.StopLoss.Float64()
	target, _ := plan.Target.Float64()
	entry, _ := plan.Entry.Float64()
	if !(stop < entry && entry < target) {
		t.Fatalf("expected stop < entry < target, got %v < %v < %v", stop, entry, target)
	}

	// Prices sit on the 5-paise tick.
	for _, v := range []float64{stop, target} {
		if math.Abs(v*20-math.Round(v*20)) > 1e-9 {
			t.Fatalf("price %v not on a 0.05 tick", v)
		}
	}
}

func TestBuildPlanAcrossThePriceSpectrum(t *testing.T) {
	// From a ₹80 PSU to a ₹25,000 heavyweight: every viable plan must respect
	// the loss ceiling.
	cases := []struct{ entry, atr float64 }{
		{80, 2.5}, {250, 6}, {600, 14}, {1500, 30}, {4000, 90}, {12000, 260}, {25000, 500},
	}
	for _, tc := range cases {
		plan, ok := buildPlan(tc.entry, tc.atr, DefaultRiskBands, 10_000_000)
		if !ok {
			continue // legitimately unplannable (share too coarse)
		}
		loss, _ := plan.LossAt.Float64()
		if loss > DefaultRiskBands.LossMax+1e-9 {
			t.Fatalf("entry %.0f atr %.0f: loss %.2f breaches the %.0f ceiling",
				tc.entry, tc.atr, loss, DefaultRiskBands.LossMax)
		}
		if plan.Quantity < 1 {
			t.Fatalf("entry %.0f: plan with zero quantity should have been rejected", tc.entry)
		}
	}
}

func TestBuildPlanCapsAtAvailableCash(t *testing.T) {
	// Risk budget says ~568 shares of a ₹2,000 stock (11.4L), but only ₹3L is
	// free — the plan must shrink and say so.
	plan, ok := buildPlan(2000, 22, DefaultRiskBands, 300_000)
	if !ok {
		t.Fatal("expected a capital-capped plan")
	}
	if !plan.CapitalCapped {
		t.Fatal("expected CapitalCapped to be set")
	}
	capital, _ := plan.Capital.Float64()
	if capital > 300_000 {
		t.Fatalf("capital required %.2f exceeds the %.2f available", capital, 300_000.0)
	}
}

func TestBuildPlanRejectsCoarseShares(t *testing.T) {
	// One share of a ₹90,000 stock with a ₹9,000 ATR risks ₹18k... the mid
	// budget (25k) buys 1 share = 18k loss, inside the band. Push ATR higher so
	// a single share breaches the ceiling.
	if _, ok := buildPlan(90_000, 16_000, DefaultRiskBands, 10_000_000); ok {
		t.Fatal("a single share losing 32k should not produce a plan")
	}
}

func TestBuildPlanRejectsDegenerateInputs(t *testing.T) {
	if _, ok := buildPlan(0, 10, DefaultRiskBands, 1e7); ok {
		t.Fatal("zero entry must not plan")
	}
	if _, ok := buildPlan(100, 0, DefaultRiskBands, 1e7); ok {
		t.Fatal("zero ATR must not plan")
	}
	if _, ok := buildPlan(100, 2, DefaultRiskBands, 0); ok {
		t.Fatal("zero cash must not plan")
	}
}

func TestRiskRewardComesFromTheBands(t *testing.T) {
	plan, ok := buildPlan(1250, 22, DefaultRiskBands, 1e7)
	if !ok {
		t.Fatal("expected a plan")
	}
	// 40k target on 25k risk.
	if math.Abs(plan.RiskRatio-1.6) > 1e-9 {
		t.Fatalf("risk:reward = %v, want 1.6", plan.RiskRatio)
	}
}
