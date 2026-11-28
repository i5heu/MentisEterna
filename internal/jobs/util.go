package jobs

import (
	"fmt"
	"time"
)

// dbTimestamp renders t in the canonical DB timestamp format (matches
// SQLite's strftime('%Y-%m-%dT%H:%M:%fZ','now')).
func dbTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// parseTimePtr parses a nullable DB timestamp ('Z', with or without
// fractional seconds).
func parseTimePtr(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02T15:04:05Z", *s)
	if err != nil {
		// Try with fractional seconds.
		t, err = time.Parse("2006-01-02T15:04:05.000Z", *s)
		if err != nil {
			return nil
		}
	}
	return &t
}

// formatJobMeta renders a definition for log lines.
func formatJobMeta(meta jobDefMeta) string {
	if meta.ID == 0 && meta.PluginID == "" && meta.Name == "" {
		return "def_id=0"
	}
	if meta.PluginID == "" && meta.Name == "" {
		return fmt.Sprintf("def_id=%d", meta.ID)
	}
	return fmt.Sprintf("job=%s/%s def_id=%d", meta.PluginID, meta.Name, meta.ID)
}

// previewText truncates s to maxRunes with an ellipsis — for logs.
func previewText(s string, maxRunes int) string {
	if maxRunes <= 0 || s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "..."
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func ptrStr(s string) *string        { return &s }
func ptrInt64(v int64) *int64        { return &v }
func timePtr(t time.Time) *time.Time { return &t }
