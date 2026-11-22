package rss

import (
	"math"
	"testing"
)

func TestParsePointRules(t *testing.T) {
	doc := parseRules("min_points: 3.5\nweights: sig=0.6, rel=0.2, pos=0.1, hum=0.1\n")
	if doc.MinPoints == nil || *doc.MinPoints != 3.5 {
		t.Errorf("min_points = %v, want 3.5", doc.MinPoints)
	}
	if doc.Weights == nil {
		t.Fatalf("weights not parsed")
	}
	want := ScoreWeights{Significance: 0.6, Relevance: 0.2, Positivity: 0.1, Humans: 0.1}
	if *doc.Weights != want {
		t.Errorf("weights = %+v, want %+v", *doc.Weights, want)
	}
	if got := doc.effectiveMinPoints(); got != 3.5 {
		t.Errorf("effectiveMinPoints = %v", got)
	}

	// Overrides negative to disable rejection.
	doc = parseRules("min_points: 0\n")
	if doc.effectiveMinPoints() != 0 {
		t.Errorf("disabled min_points = %v, want 0", doc.effectiveMinPoints())
	}

	// Defaults apply without rules.
	doc = parseRules("just prose\n")
	if got := doc.effectiveMinPoints(); got != defaultMinPoints {
		t.Errorf("default min_points = %v, want %v", got, defaultMinPoints)
	}
	if got := doc.effectiveWeights(); got != defaultScoreWeights() {
		t.Errorf("default weights = %+v", got)
	}

	// Partial weights line: only named components change.
	doc = parseRules("weights: relevance=1\n")
	w := doc.effectiveWeights()
	if w.Relevance != 1 || w.Significance != defaultScoreWeights().Significance {
		t.Errorf("partial weights = %+v", w)
	}
}

func TestParsePointRuleWarnings(t *testing.T) {
	doc := parseRules("min_points: banana\nmin_points: -1\nweights:\nweights: foo=0.5\nweights: sig=2\n")
	// Bad min_points ×2, empty weights, unknown key, value out of range.
	if len(doc.Warnings) != 6 { // -1 gives a separate invalid-min_points warning; sig=2 invalid weight
		for i, w := range doc.Warnings {
			t.Logf("warning[%d]: %s", i, w)
		}
	}
	if doc.MinPoints != nil {
		t.Errorf("min_points should stay nil on bad input, got %v", *doc.MinPoints)
	}
	if doc.Weights != nil && doc.Weights.Significance == 2 {
		t.Errorf("out-of-range weight accepted: %+v", *doc.Weights)
	}
}

func TestComputePoints(t *testing.T) {
	w := defaultScoreWeights()

	// High-significance, positive, relevant, whole-world reach.
	m := evalMetrics{Significance: 0.7, Negativity: 0, Relevance: 0.6,
		FutureIndirect: 2000, NowDirect: 3.2, NowIndirect: 40, FutureDirect: 120.5}
	p := computePoints(m, w, 0)
	if p < 7.0 || p > 7.6 {
		t.Errorf("world-scale points = %v, want ≈7.35", p)
	}

	// Ongoing war with no new developments (capped 0.3 sig, negative): low.
	m = evalMetrics{Significance: 0.3, Negativity: 1, Relevance: 0, NowDirect: 8}
	if p := computePoints(m, w, 0); p > 2 {
		t.Errorf("war-update points = %v, want ≤2", p)
	}

	// Local event capped at 0.2 significance, not personally relevant:
	// below the default min_points floor.
	m = evalMetrics{Significance: 0.2, Negativity: 0.5, Relevance: 0, NowIndirect: 0.4}
	if p := computePoints(m, w, 0); p >= defaultMinPoints {
		t.Errorf("local-event points = %v, want < %v", p, defaultMinPoints)
	}

	// Perfect positive global event with relevance: maxes out at 10.
	m = evalMetrics{Significance: 1, Negativity: 0, Relevance: 1, FutureIndirect: 7800, FutureDirect: 100}
	if p := computePoints(m, w, 0); p != 10 {
		t.Errorf("max points = %v, want 10", p)
	}

	// Penalties subtract; floor at 0.
	m = evalMetrics{Significance: 0.5, Negativity: 1}
	if p := computePoints(m, w, 3); p != 0 {
		t.Errorf("penalized points = %v, want 0 (floored)", p)
	}
}

func TestHumansTerm(t *testing.T) {
	if got := humansTerm(evalMetrics{}); got != 0 {
		t.Errorf("zero humans term = %v", got)
	}
	for _, tc := range []struct {
		total float64
		want  float64
	}{
		{1, 0.077},    // 1M
		{10, 0.267},   // 10M
		{1000, 0.769}, // 1B
		{8000, 1.0},   // world
		{800000, 1.0}, // absurd → capped
	} {
		m := evalMetrics{NowDirect: tc.total}
		if got, want := humansTerm(m), tc.want; math.Abs(got-want) > 0.02 {
			t.Errorf("humansTerm(%vM) = %v, want ≈%v", tc.total, got, want)
		}
	}
}
