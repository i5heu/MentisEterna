package media

import (
	"regexp"
	"strconv"
)

// fileURLRe matches /file/<noteID>/<fileID> in markdown.
// Both images (![...](/file/...)) and links ([...](/file/...)) are captured.
var fileURLRe = regexp.MustCompile(`/file/[^/\s)]+/([0-9]+)`)

// ExtractReferencedFileIDs returns a deduplicated slice of file IDs
// referenced in the given markdown body.
func ExtractReferencedFileIDs(body string) []int64 {
	matches := fileURLRe.FindAllStringSubmatch(body, -1)
	seen := make(map[int64]bool)
	var ids []int64
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		id, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}
