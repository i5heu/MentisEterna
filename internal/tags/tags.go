package tags

import "strings"

// NormalizeName canonicalizes a tag name for storage and comparisons.
//
// Rules:
//   - trim surrounding whitespace
//   - strip any leading # prefix
//   - collapse internal whitespace to a single space
//   - lowercase the final result
func NormalizeName(name string) string {
	name = strings.TrimSpace(name)
	for strings.HasPrefix(name, "#") {
		name = strings.TrimSpace(strings.TrimPrefix(name, "#"))
	}
	if name == "" {
		return ""
	}
	name = strings.Join(strings.Fields(name), " ")
	name = strings.ToLower(name)
	return strings.TrimSpace(name)
}

// NormalizeNames normalizes, de-duplicates, and preserves the first-seen order.
func NormalizeNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		normalized := NormalizeName(name)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	return out
}
