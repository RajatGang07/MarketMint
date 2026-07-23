package signals

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/gangrajat/groww-paper-trading/backend/internal/analytics"
	"github.com/gangrajat/groww-paper-trading/backend/internal/store"
)

func pos(symbol string, qty int64, avg string) store.Position {
	return store.Position{
		TradingSymbol: symbol,
		Quantity:      qty,
		AvgPrice:      decimal.RequireFromString(avg),
	}
}

func cand(rank int, lastClose, rsi float64) analytics.Candidate {
	return analytics.Candidate{
		Rank: rank,
		Features: analytics.Features{
			LastClose: lastClose,
			RSI14:     rsi,
		},
	}
}

const universeN = 200

func TestHoldingWithHealthyRankHolds(t *testing.T) {
	row := holdingRow(pos("GOODCO", 100, "1000"), cand(15, 1050, 62), true, universeN, true)
	if row.Action != "HOLD" {
		t.Fatalf("action = %s, want HOLD: %v", row.Action, row.Reasons)
	}
	if !row.ExitsArmed {
		t.Fatal("exits should be reported armed")
	}
	if row.UnrealizedPnL != 5000 {
		t.Fatalf("uPnL = %v, want 5000", row.UnrealizedPnL)
	}
}

func TestRankCollapseTriggersSell(t *testing.T) {
	// Rank 180 of 200 is bottom-tercile: the trend the position was bought
	// for no longer exists.
	row := holdingRow(pos("FADECO", 50, "500"), cand(180, 510, 55), true, universeN, true)
	if row.Action != "SELL" {
		t.Fatalf("action = %s, want SELL: %v", row.Action, row.Reasons)
	}
	if !containsSubstring(row.Reasons, "rank collapsed") {
		t.Fatalf("missing rank-collapse reason: %v", row.Reasons)
	}
}

func TestBlowOffRSITriggersSell(t *testing.T) {
	row := holdingRow(pos("HOTCO", 10, "2000"), cand(3, 2600, 87), true, universeN, true)
	if row.Action != "SELL" {
		t.Fatalf("action = %s, want SELL: %v", row.Action, row.Reasons)
	}
	if !containsSubstring(row.Reasons, "blow-off") {
		t.Fatalf("missing RSI reason: %v", row.Reasons)
	}
}

func TestUndefendedLoserTriggersSell(t *testing.T) {
	// Down 15% with no stop resting → the one thing the board must never
	// stay quiet about.
	row := holdingRow(pos("BAGHOLD", 200, "1000"), cand(50, 850, 45), true, universeN, false)
	if row.Action != "SELL" {
		t.Fatalf("action = %s, want SELL: %v", row.Action, row.Reasons)
	}
	if !containsSubstring(row.Reasons, "undefended") {
		t.Fatalf("missing undefended-loss reason: %v", row.Reasons)
	}
}

func TestProtectedLoserIsNotFlaggedForTheStopRule(t *testing.T) {
	// Same 15% drawdown, but a stop is resting: the bracket owns the exit.
	row := holdingRow(pos("DEFENDED", 200, "1000"), cand(50, 850, 45), true, universeN, true)
	if row.Action != "HOLD" {
		t.Fatalf("action = %s, want HOLD (stop is armed): %v", row.Action, row.Reasons)
	}
}

func TestUnscoredHoldingFallsBackGracefully(t *testing.T) {
	// Not in the F&O universe: no model opinion, but the undefended-loss
	// rule still applies on price alone. LastClose is zero → marked to avg,
	// so PnL reads flat and only the no-exits note fires.
	row := holdingRow(pos("SMALLCAP", 10, "120"), analytics.Candidate{}, false, universeN, false)
	if row.Action != "HOLD" {
		t.Fatalf("action = %s, want HOLD: %v", row.Action, row.Reasons)
	}
	if !containsSubstring(row.Reasons, "not scored by the model") {
		t.Fatalf("missing out-of-universe note: %v", row.Reasons)
	}
	if !containsSubstring(row.Reasons, "no exit orders") {
		t.Fatalf("missing unprotected note: %v", row.Reasons)
	}
}

func TestSortRowsPutsMoneyDecisionsFirst(t *testing.T) {
	rows := []Row{
		{Action: "HOLD", Symbol: "H", Rank: 5},
		{Action: "WATCH", Symbol: "W", Rank: 12},
		{Action: "SELL", Symbol: "S", Rank: 190},
		{Action: "BUY", Symbol: "B2", Rank: 2},
		{Action: "BUY", Symbol: "B1", Rank: 1},
	}
	sortRows(rows)
	got := ""
	for _, r := range rows {
		got += r.Action + " "
	}
	if got != "BUY BUY SELL WATCH HOLD " {
		t.Fatalf("order = %s", got)
	}
	if rows[0].Symbol != "B1" {
		t.Fatalf("BUY rows must be rank-ordered, got %s first", rows[0].Symbol)
	}
}

func containsSubstring(reasons []string, want string) bool {
	for _, r := range reasons {
		if strings.Contains(r, want) {
			return true
		}
	}
	return false
}
