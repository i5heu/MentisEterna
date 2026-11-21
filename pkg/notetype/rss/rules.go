package rss

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// maxRuleOccurrences caps per-phrase occurrence counting so one pathological
// article cannot consume the entire score budget.
const maxRuleOccurrences = 10

// PenaltyEntry is one applied rule, for UI tooltips and auditability.
type PenaltyEntry struct {
	Phrase      string  `json:"phrase"`
	Points      float64 `json:"points"`
	Occurrences int     `json:"occurrences"`
}

// RulePenalty is one penalty-mapped phrase.
type RulePenalty struct {
	phrase string  // lower-cased match phrase
	points float64 // per occurrence (may be negative)
}

// RulesDoc is the parsed ban words / penalty points body of a reader note.
type RulesDoc struct {
	Bans      []string
	Penalties []RulePenalty
	Threshold *float64 // explicit `threshold: N` override (nil when absent)
	Warnings  []string // rule-shaped lines that failed to parse
}

// parseRules parses rule lines from a reader note's body. The body is user
// space: lines that don't look like rules (plain prose, comments, blanks)
// are ignored silently; rule-shaped lines that fail to parse are collected
// into Warnings and surfaced in the view.
func parseRules(body string) RulesDoc {
	var doc RulesDoc
	for lineNo, rawLine := range strings.Split(body, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lower := strings.ToLower(line)

		switch {
		case strings.HasPrefix(lower, "ban:"):
			phrase := strings.TrimSpace(line[len("ban:"):])
			if phrase == "" {
				doc.Warnings = append(doc.Warnings, fmt.Sprintf("line %d: empty ban phrase", lineNo+1))
				continue
			}
			doc.Bans = append(doc.Bans, strings.ToLower(phrase))

		case strings.HasPrefix(lower, "threshold:"):
			raw := strings.TrimSpace(line[len("threshold:"):])
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil || v < 0 {
				doc.Warnings = append(doc.Warnings, fmt.Sprintf("line %d: invalid threshold %q", lineNo+1, raw))
				continue
			}
			doc.Threshold = &v

		default:
			// Penalty: "<phrase>: <number>" — split at the LAST colon so
			// phrases containing colons (URLs etc.) work.
			idx := strings.LastIndex(line, ":")
			if idx <= 0 || idx == len(line)-1 {
				continue // ordinary prose, not a rule
			}
			phrase := strings.TrimSpace(line[:idx])
			points, err := strconv.ParseFloat(strings.TrimSpace(line[idx+1:]), 64)
			if err != nil {
				// Looked like a rule but is not — warn, don't ignore.
				doc.Warnings = append(doc.Warnings, fmt.Sprintf("line %d: invalid points %q", lineNo+1, strings.TrimSpace(line[idx+1:])))
				continue
			}
			if phrase == "" {
				doc.Warnings = append(doc.Warnings, fmt.Sprintf("line %d: empty phrase", lineNo+1))
				continue
			}
			doc.Penalties = append(doc.Penalties, RulePenalty{phrase: strings.ToLower(phrase), points: points})
		}
	}
	return doc
}

// RulesResult is the scoring outcome for one article.
type RulesResult struct {
	Score   float64        `json:"score"`
	Banned  bool           `json:"banned"`
	Entries []PenaltyEntry `json:"entries"`
}

// effectiveThreshold resolves the rejection threshold: an explicit rules-body
// `threshold:` wins over the feed config; 0 disables score rejection.
func (doc RulesDoc) effectiveThreshold(configured float64) float64 {
	if doc.Threshold != nil {
		return *doc.Threshold
	}
	return configured
}

// applyRules scores an article's title + content against the rules.
func applyRules(doc RulesDoc, title, content string) RulesResult {
	text := strings.ToLower(title + "\n" + content)
	res := RulesResult{Entries: []PenaltyEntry{}}

	for _, ban := range doc.Bans {
		if strings.Contains(text, ban) {
			res.Banned = true
			break
		}
	}

	for _, pen := range doc.Penalties {
		occurrences := 0
		if strings.Contains(text, pen.phrase) {
			occurrences = countOccurrences(text, pen.phrase)
			if occurrences > maxRuleOccurrences {
				occurrences = maxRuleOccurrences
			}
		}
		if occurrences == 0 {
			continue
		}
		res.Entries = append(res.Entries, PenaltyEntry{
			Phrase:      pen.phrase,
			Points:      pen.points,
			Occurrences: occurrences,
		})
		res.Score += pen.points * float64(occurrences)
	}
	return res
}

// countOccurrences counts non-overlapping case-insensitive substring hits.
func countOccurrences(haystack, needle string) int {
	count := 0
	for from := 0; ; {
		idx := strings.Index(haystack[from:], needle)
		if idx < 0 {
			break
		}
		count++
		from += idx + len(needle)
	}
	return count
}

// penaltyJSONString serializes rule entries for the penalty_json column.
func penaltyJSONString(entries []PenaltyEntry) string {
	if entries == nil {
		entries = []PenaltyEntry{}
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return "[]"
	}
	return string(b)
}
