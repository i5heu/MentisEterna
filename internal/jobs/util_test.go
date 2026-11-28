package jobs

import (
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

// --- util.go: timestamps ---

func TestParseTimePtr(t *testing.T) {
	t.Parallel()

	// Covers both DB timestamp shapes (whole seconds and the fractional
	// format SQLite's strftime('%...%fZ') writes), plus every rejection:
	// a nil or empty pointer must yield nil, and garbage must never panic.
	cases := map[string]struct {
		in      *string
		want    time.Time
		wantNil bool
	}{
		// Prevents a nil-timestamp column crashing callers that assume a
		// parsed value (e.g. CancelRun on rows without started_at).
		"nil pointer": {in: nil, wantNil: true},
		// Prevents empty-string placeholders being parsed as zero times.
		"empty string": {in: ptrStr(""), wantNil: true},
		// Prevents whole-second legacy timestamps (pre-migration rows)
		// being silently dropped from API responses.
		"whole seconds": {in: ptrStr("2026-05-12T03:04:05Z"), want: time.Date(2026, 5, 12, 3, 4, 5, 0, time.UTC)},
		// Prevents the canonical strftime('%...%fZ') shape failing to
		// round-trip — it is what every write in this package produces.
		"millisecond precision": {in: ptrStr("2026-05-12T03:04:05.123Z"), want: time.Date(2026, 5, 12, 3, 4, 5, 123000000, time.UTC)},
		// Prevents corrupted/edited timestamps panicking or returning a
		// bogus non-nil time.
		"garbage": {in: ptrStr("not-a-timestamp"), wantNil: true},
		// Prevents dates-only strings (SQLite DATE columns) leaking into
		// Run fields as half-parsed times.
		"date only": {in: ptrStr("2026-05-12"), wantNil: true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := parseTimePtr(tc.in)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("parseTimePtr(%v) = %v, want nil", derefString(tc.in), got)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseTimePtr(%v) = nil, want %v", derefString(tc.in), tc.want)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("parseTimePtr(%v) = %v, want %v", derefString(tc.in), got, tc.want)
			}
		})
	}
}

// TestDBTimestampRoundTrip pins the invariant that every timestamp this
// package writes via dbTimestamp is recoverable by parseTimePtr — the
// JSON API surface (StartedAt etc.) depends on it for any random time.
func TestDBTimestampRoundTrip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		// Prevents precision drift: a random instant losing anything
		// through the DB text format would make ordered displays
		// (retries due "in 3s") lie by up to a second.
		ts := rapid.Custom(func(r *rapid.T) time.Time {
			return time.Unix(rapid.Int64Range(-1<<33, 1<<33).Draw(r, "unix"),
				int64(rapid.IntRange(0, 999).Draw(r, "millis"))*int64(time.Millisecond)).UTC()
		}).Draw(rt, "ts")

		s := dbTimestamp(ts)
		got := parseTimePtr(&s)
		if got == nil {
			rt.Fatalf("dbTimestamp(%v) = %q failed to parse back", ts, s)
		}
		// dbTimestamp truncates to milliseconds; compare at that precision.
		if !ts.Truncate(time.Millisecond).Equal(*got) {
			rt.Fatalf("round-trip mismatch: wrote %v got %v", ts.Truncate(time.Millisecond), *got)
		}
	})
}

// --- util.go: log formatting ---

func TestFormatJobMeta(t *testing.T) {
	t.Parallel()

	// Every log line in the package funnels through formatJobMeta; these
	// shapes guarantee the fallbacks never print misleading junk like
	// "job=/ def_id=7" for partially-known definitions.
	cases := map[string]struct {
		meta jobDefMeta
		want string
	}{
		// Prevents zero-value metadata (a run whose definition row was
		// deleted) logging as "job=/ def_id=0" — def_id=0 is the honest
		// answer and log-greppable.
		"zero value": {meta: jobDefMeta{}, want: "def_id=0"},
		// Prevents definitions known only by row id (lookupDefinition
		// fallback failed mid-scan) logging an empty job path.
		"id only": {meta: jobDefMeta{ID: 42}, want: "def_id=42"},
		// The common full shape — guards the parsing of log scrapes and
		// keeps plugin/name in the canonical order.
		"full": {meta: jobDefMeta{ID: 7, PluginID: "rss", Name: "refresh_feeds"},
			want: "job=rss/refresh_feeds def_id=7"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := formatJobMeta(tc.meta); got != tc.want {
				t.Fatalf("formatJobMeta(%+v) = %q, want %q", tc.meta, got, tc.want)
			}
		})
	}
}

func TestPreviewText(t *testing.T) {
	t.Parallel()

	// Bounded truncation keeps logs finite even for multi-megabyte RSS
	// payloads; the invariant is: result is a prefix of the input and
	// never longer than maxRunes+3 runes (the "..." allowance).
	cases := map[string]struct {
		in       string
		maxRunes int
		want     string
	}{
		// Prevents a zero/negative budget turning into a full-string
		// return (log flooding) or an index panic.
		"non-positive budget returns empty": {in: "hello", maxRunes: 0, want: ""},
		// Prevents empty inputs growing the "..." suffix (would turn
		// every no-payload job into a misleading "..." log).
		"empty input stays empty": {in: "", maxRunes: 10, want: ""},
		// Prevents off-by-one truncation of short strings.
		"short input is verbatim": {in: "abc", maxRunes: 10, want: "abc"},
		// Prevents rune-blind slicing (byte slicing would corrupt
		// multi-byte characters mid-rune).
		"multi-byte runes are respected": {in: "µµµµµ", maxRunes: 2, want: "µµ..."},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := previewText(tc.in, tc.maxRunes); got != tc.want {
				t.Fatalf("previewText(%q, %d) = %q, want %q", tc.in, tc.maxRunes, got, tc.want)
			}
		})
	}

	rapid.Check(t, func(rt *rapid.T) {
		// Prevents regressions where truncation stops being a prefix
		// (e.g. trimming from the middle) or the ellipsis allowance is
		// dropped, either of which breaks log-based debugging.
		s := rapid.StringN(0, 0, 200).Draw(rt, "s")
		max := rapid.IntRange(-1, 50).Draw(rt, "max")
		got := previewText(s, max)
		if len([]rune(got)) > max+3 {
			rt.Fatalf("preview %d runes exceeds budget %d+3", len([]rune(got)), max)
		}
		if got != "" && len(s) > 0 {
			if trimmed, ok := cutSuffix(got, "..."); !ok {
				// No ellipsis: must be the input verbatim.
				if got != s {
					rt.Fatalf("unmarked preview %q is not the verbatim input", got)
				}
			} else if !hasPrefix(s, trimmed) {
				rt.Fatalf("preview %q is not a prefix of the input", got)
			}
		}
	})
}

// hasPrefix/cutSuffix are tiny local helpers so the rapid property above
// stays readable.
func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

func cutSuffix(s, suffix string) (string, bool) {
	if len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)], true
	}
	return "", false
}

// --- util.go / definitions.go: error helpers ---

func TestFmtErrorWrapsCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("disk i/o error")
	err := fmtError("persist job definitions", cause)
	// Prevents the uniform wrap sites from losing the cause chain:
	// every fmtError user (Start, registration) relies on errors.Is
	// unwrapping to the underlying sqlite failure for diagnosis.
	if !errors.Is(err, cause) {
		t.Fatalf("fmtError result %v does not wrap cause %v", err, cause)
	}
}

func TestPtrHelpers(t *testing.T) {
	t.Parallel()

	// The ptr helpers are used to build Run/RunEvent fields; the pointer
	// must carry the exact value or JSON payloads would lie about e.g.
	// attempt counts.
	if got := derefString(ptrStr("x")); got != "x" {
		t.Fatalf("derefString(ptrStr) = %q", got)
	}
	if got := derefString(nil); got != "" {
		t.Fatalf("derefString(nil) = %q, want empty", got)
	}
	if got := ptrInt64(5); got == nil || *got != 5 {
		t.Fatalf("ptrInt64(5) = %v", got)
	}
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := timePtr(ts); got == nil || !got.Equal(ts) {
		t.Fatalf("timePtr round-trip failed: %v", got)
	}
	if diff := cmp.Diff(ptrStr("a"), ptrStr("a")); diff != "" {
		t.Fatalf("ptrStr diff: %s", diff)
	}
}
