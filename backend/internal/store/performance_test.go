package store

import "testing"

func ct(pnl float64) ClosedTrade { return ClosedTrade{RealizedPnL: pnl} }

func TestComputePerformance(t *testing.T) {
	p := ComputePerformance([]ClosedTrade{ct(100), ct(-50), ct(200), ct(-50), ct(50)})

	if p.ClosedTrades != 5 || p.Wins != 3 || p.Losses != 2 {
		t.Fatalf("counts wrong: %+v", p)
	}
	if p.TotalPnL != 250 || p.Expectancy != 50 {
		t.Errorf("pnl/expectancy wrong: %+v", p)
	}
	if p.ProfitFactor != 3.5 { // 350 gross win / 100 gross loss
		t.Errorf("profit factor = %v, want 3.5", p.ProfitFactor)
	}
	if p.AvgWin != 350.0/3 || p.AvgLoss != 50 {
		t.Errorf("avg win/loss wrong: %+v", p)
	}
	// Equity path: 100, 50, 250, 200, 250 → deepest fall is 100→50.
	if p.MaxDrawdown != 50 {
		t.Errorf("max drawdown = %v, want 50", p.MaxDrawdown)
	}
}

func TestComputePerformanceEmpty(t *testing.T) {
	p := ComputePerformance(nil)
	if p.ClosedTrades != 0 || p.WinRate != 0 || p.ProfitFactor != 0 {
		t.Errorf("empty ledger must be all zeros: %+v", p)
	}
}
