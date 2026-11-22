package rss

import "math"

// --- Significance-evaluation points ---
//
// Classified articles carry the LLM's significance evaluation (see
// classify.go): global significance, negativity, personal relevance, and
// humans-affected counts. computePoints condenses that evaluation into one
// 0–10 "points" value that ranks the timeline; articles of an evaluated
// item scoring below the body's `min_points:` floor are rejected.
//
//	points = 10 × (wSig·significance + wRel·relevance
//	                + wPos·(1−negativity) + wHum·humansTerm) − penalty
//
// The weights come from the note body (`weights:` rules, see rules.go);
// rule penalties subtract from the raw value.

const (
	// defaultMinPoints is the reject-below floor for evaluated items when
	// the body defines no `min_points:` rule. 0 disables points rejection.
	defaultMinPoints = 2.0

	// humansTermScale is the humans-affected total (in millions) that maps
	// to humansTerm = 1.0 — roughly the living world population.
	humansTermScale = 8000

	// maxPoints is the top of the points scale (matches the legacy 0–10
	// quality rating scale).
	maxPoints = 10.0
)

// ScoreWeights are the components of the points formula. Each weight is the
// share of the 0–10 scale its evaluation dimension contributes.
type ScoreWeights struct {
	Significance float64 `json:"significance"` // global significance 0..1
	Relevance    float64 `json:"relevance"`    // personal relevance 0..1
	Positivity   float64 `json:"positivity"`   // 1 − negativity
	Humans       float64 `json:"humans"`       // log-scaled affected-humans term
}

// defaultScoreWeights: significance carries the evaluation, personal
// relevance boosts local-interest stories, positivity rewards positive
// developments (the evaluation prompt already discounts casualty-heavy
// news inside significance — this adds a mild explicit tilt), and the
// humans-affected term adds reach.
func defaultScoreWeights() ScoreWeights {
	return ScoreWeights{Significance: 0.5, Relevance: 0.25, Positivity: 0.15, Humans: 0.10}
}

// normalize clamps every weight into [0, 1].
func (w ScoreWeights) normalize() ScoreWeights {
	w.Significance = clamp01(w.Significance)
	w.Relevance = clamp01(w.Relevance)
	w.Positivity = clamp01(w.Positivity)
	w.Humans = clamp01(w.Humans)
	return w
}

// evalMetrics is the stored evaluation of one article — the column mirror
// of the LLM output (see Classification.metrics).
type evalMetrics struct {
	Significance   float64
	Negativity     float64
	Relevance      float64
	NowDirect      float64 // millions of humans
	NowIndirect    float64
	FutureDirect   float64
	FutureIndirect float64
}

// totalAffectedHumans sums the four affected-humans dimensions.
func (m evalMetrics) totalAffectedHumans() float64 {
	return m.NowDirect + m.NowIndirect + m.FutureDirect + m.FutureIndirect
}

// humansTerm compresses millions of affected humans onto 0..1 with a log
// curve: 1M ≈ 0.10, 10M ≈ 0.23, 1000M ≈ 0.77, 8000M (world) ≈ 1.0.
func humansTerm(m evalMetrics) float64 {
	total := m.totalAffectedHumans()
	if total <= 0 {
		return 0
	}
	t := math.Log10(1+total) / math.Log10(1+humansTermScale)
	return clamp01(t)
}

// computePoints condenses an evaluation (+ rule penalty) into the rounded
// 0–10 points value stored on the item and shown in the UI.
func computePoints(m evalMetrics, w ScoreWeights, penalty float64) float64 {
	w = w.normalize()
	raw := maxPoints*(w.Significance*m.Significance+
		w.Relevance*m.Relevance+
		w.Positivity*(1-m.Negativity)+
		w.Humans*humansTerm(m)) - penalty
	if raw < 0 {
		return 0
	}
	if raw > maxPoints {
		return maxPoints
	}
	return math.Round(raw*100) / 100
}

// clamp01 clamps v into [0, 1].
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// clampHumans bounds a humans-affected (millions) value into [0, 1e6]
// (1e6 millions = one trillion humans — a sanity bound against model
// hallucinations, never a meaningful limit).
func clampHumans(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1_000_000 {
		return 1_000_000
	}
	return v
}
