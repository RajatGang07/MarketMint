package news

import "testing"

func TestLexiconScore(t *testing.T) {
	cases := []struct {
		title string
		sign  int // -1, 0, +1
	}{
		{"Shares surge after company beats estimates", +1},
		{"Stock plunges as regulator opens probe into fraud", -1},
		{"Company announces quarterly board meeting date", 0},
		{"Brokerage upgrades stock, sees strong results ahead", +1},
		{"Profit falls; brokerage downgrades on weak results", -1},
	}
	for _, c := range cases {
		got := lexiconScore(c.title)
		switch {
		case c.sign > 0 && got <= 0:
			t.Errorf("%q: want positive, got %v", c.title, got)
		case c.sign < 0 && got >= 0:
			t.Errorf("%q: want negative, got %v", c.title, got)
		case c.sign == 0 && got != 0:
			t.Errorf("%q: want neutral, got %v", c.title, got)
		}
	}
}

func TestParseScores(t *testing.T) {
	scores, err := parseScores("Here you go: [0.5, -0.25, 0]", 3)
	if err != nil {
		t.Fatal(err)
	}
	if scores[0] != 0.5 || scores[1] != -0.25 || scores[2] != 0 {
		t.Errorf("unexpected scores %v", scores)
	}

	if _, err := parseScores("[0.5]", 3); err == nil {
		t.Error("length mismatch must error")
	}
	if _, err := parseScores("no array here", 1); err == nil {
		t.Error("missing array must error")
	}
}
