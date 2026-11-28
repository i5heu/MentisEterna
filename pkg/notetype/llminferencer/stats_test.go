package llminferencer

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/internal/jobs"
	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/pkg/notetype"
	"pgregory.net/rapid"
)

// callSpec is one synthetic call for the ledger tests.
type callSpec struct {
	usage      string
	source     string
	provider   string
	baseURL    string
	model      string
	ok         bool
	errText    string
	prompt     int
	completion int
	total      int
	duration   int64
}

func (c callSpec) stats() llm.CallStats {
	cs := llm.CallStats{
		Usage:            c.usage,
		Source:           c.source,
		Provider:         c.provider,
		BaseURL:          c.baseURL,
		Model:            c.model,
		OK:               c.ok,
		Error:            c.errText,
		PromptTokens:     c.prompt,
		CompletionTokens: c.completion,
		TotalTokens:      c.total,
		DurationMS:       c.duration,
	}
	if !c.ok {
		// Transport contract: failed calls carry no token counts.
		cs.PromptTokens, cs.CompletionTokens, cs.TotalTokens = 0, 0, 0
	}
	return cs
}

// record plays a batch of calls into the ledger through the real
// RecordCall path (the same one the server wires into the global sink).
func record(t *testing.T, db *sql.DB, calls ...callSpec) {
	t.Helper()
	p := &Plugin{}
	p.SetCallStatsDB(db)
	for _, c := range calls {
		p.RecordCall(c.stats())
	}
}

// countRows counts the raw ledger after a batch.
func countRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ct_llm_inferencer_stats`).Scan(&n); err != nil {
		t.Fatalf("count stats: %v", err)
	}
	return n
}

// TestRecordCallAndLoadStatsView: the core scenario — a mixed batch of
// remote (note), local-pin, and config-fallback calls, successes and
// failures — must aggregate into exactly one row per usage×routing×model
// with correct token sums, and roll up into totals plus the local↔remote
// split. Failure mode prevented: mis-grouped or double-counted token
// accounting silently lying to the user about provider spend.
func TestRecordCallAndLoadStatsView(t *testing.T) {
	d := testDB(t) // notes + plugin schema (includes the stats table)

	record(t, d,
		// Remote rss_classify call through this note's provider.
		callSpec{usage: "rss_classify", source: llm.ChatSourceNote, provider: "openrouter",
			baseURL: "https://openrouter.ai/api", model: "prov/small", ok: true,
			prompt: 100, completion: 50, total: 150, duration: 120},
		// Same usage on the config.toml system (local side of the split).
		callSpec{usage: "rss_classify", source: llm.ChatSourceConfig, model: "gemma-4-e2b",
			ok: true, prompt: 10, completion: 5, total: 15, duration: 80},
		// A failed title call: counted as a call + error, zero tokens.
		callSpec{usage: "title", source: llm.ChatSourceConfig, model: "granite-4.1-3b",
			ok: false, errText: "llm request to http://x/v1/chat/completions returned 500: boom", duration: 5},
		// The note's connection-test pseudo usage is a real provider call.
		callSpec{usage: "test", source: llm.ChatSourceNote, provider: "openrouter", model: "prov/big",
			ok: true, prompt: 3, completion: 2, total: 5, duration: 640},
	)

	view, err := loadStatsView(d)
	if err != nil {
		t.Fatalf("loadStatsView: %v", err)
	}

	wantRows := []StatsRow{
		{Usage: "rss_classify", Label: "RSS Evaluation", Source: llm.ChatSourceNote,
			Provider: "openrouter", Model: "prov/small", Calls: 1, Errors: 0,
			PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150, AvgDurationMS: 120, LastUsedAt: ""},
		{Usage: "rss_classify", Label: "RSS Evaluation", Source: llm.ChatSourceConfig,
			Model: "gemma-4-e2b", Calls: 1, Errors: 0,
			PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, AvgDurationMS: 80},
		{Usage: "test", Label: "Connection Test", Source: llm.ChatSourceNote,
			Provider: "openrouter", Model: "prov/big", Calls: 1, Errors: 0,
			PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5, AvgDurationMS: 640},
		{Usage: "title", Label: "Note Title Generation", Source: llm.ChatSourceConfig,
			Model: "granite-4.1-3b", Calls: 1, Errors: 1,
			PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0, AvgDurationMS: 0},
	}
	if diff := cmp.Diff(wantRows, view.Rows, sortRows(), cmpopts.IgnoreFields(StatsRow{}, "LastUsedAt")); diff != "" {
		t.Fatalf("rows mismatch (-want +got):\n%s", diff)
	}

	// Totals: 4 calls, 1 error, 113/57/170 tokens.
	wantTotals := StatsSplit{Calls: 4, Errors: 1, PromptTokens: 113, CompletionTokens: 57, TotalTokens: 170}
	if diff := cmp.Diff(wantTotals, view.Totals); diff != "" {
		t.Fatalf("totals mismatch (-want +got):\n%s", diff)
	}
	// Remote = note-routed; local = config/local pins (including errors).
	wantRemote := StatsSplit{Calls: 2, Errors: 0, PromptTokens: 103, CompletionTokens: 52, TotalTokens: 155}
	wantLocal := StatsSplit{Calls: 2, Errors: 1, PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	if diff := cmp.Diff(wantRemote, view.Remote); diff != "" {
		t.Fatalf("remote split mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantLocal, view.Local); diff != "" {
		t.Fatalf("local split mismatch (-want +got):\n%s", diff)
	}

	// Recent: newest first, the connection test is the latest call.
	if len(view.Recent) != 4 {
		t.Fatalf("recent = %d entries, want 4", len(view.Recent))
	}
	if view.Recent[0].Usage != "test" || !view.Recent[0].OK || view.Recent[0].Label != "Connection Test" {
		t.Fatalf("recent[0] = %+v, want the latest test call", view.Recent[0])
	}
	var failed *StatsRecentCall
	for i := range view.Recent {
		if !view.Recent[i].OK {
			failed = &view.Recent[i]
		}
	}
	if failed == nil || !strings.Contains(failed.Error, "returned 500") {
		t.Fatalf("failed recent call = %+v, want error text from the provider reply", failed)
	}

	// The ledger window is populated.
	if view.FirstRecordedAt == "" || view.LastRecordedAt == "" {
		t.Fatalf("empty window: first=%q last=%q", view.FirstRecordedAt, view.LastRecordedAt)
	}
	if view.RetentionDays != statsRetentionDays {
		t.Fatalf("retention days = %d, want %d", view.RetentionDays, statsRetentionDays)
	}
}

// sortRows normalizes row order for comparisons: the view sorts by
// (usage, source, model), but LastUsedAt-stripped rows can differ in
// ordering only there — sort both sides by the same key.
func sortRows() cmp.Option {
	return cmpopts.SortSlices(func(a, b StatsRow) bool {
		if a.Usage != b.Usage {
			return a.Usage < b.Usage
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		return a.Model < b.Model
	})
}

// TestLoadStatsViewEmpty: a fresh app (or after clear_stats) must render
// a valid, zeroed statistics block — never nil rows or a JSON error.
// Failure mode prevented: an empty ledger breaking the note view.
func TestLoadStatsViewEmpty(t *testing.T) {
	d := testDB(t)
	view, err := loadStatsView(d)
	if err != nil {
		t.Fatalf("loadStatsView on empty ledger: %v", err)
	}
	if len(view.Rows) != 0 || len(view.Recent) != 0 {
		t.Fatalf("empty ledger produced rows: %+v", view)
	}
	if view.RetentionDays != statsRetentionDays {
		t.Fatalf("retention = %d", view.RetentionDays)
	}
	if view.FirstRecordedAt != "" || view.LastRecordedAt != "" {
		t.Fatalf("empty window: %q..%q", view.FirstRecordedAt, view.LastRecordedAt)
	}
}

// TestRecordCallWithoutDB / TestRecordCallClosedDB: recording is
// best-effort by contract — before startup (nil db) and on a dead
// handle it must silently drop the call, never panic or fail the LLM
// request it is reporting for. Failure mode prevented: stats recording
// turning into hard failures in the request path.
func TestRecordCallWithoutDB(t *testing.T) {
	d := testDB(t)
	p := &Plugin{}                                                        // never wired: statsDB stays nil (pre-startup state)
	p.RecordCall(llm.CallStats{OK: true, Usage: "title", TotalTokens: 3}) // no panic
	if n := countRows(t, d); n != 0 {
		t.Fatalf("nil-db RecordCall wrote %d rows", n)
	}
}

func TestRecordCallClosedDB(t *testing.T) {
	d := testDB(t)
	p := &Plugin{}
	p.SetCallStatsDB(d)
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	p.RecordCall(llm.CallStats{OK: true, Usage: "title"}) // swallowed, no panic
}

// TestRecordCallTrimsError: provider error bodies can be huge; the
// persisted copy must be bounded so one 4 MB error reply cannot bloat
// the ledger. Failure mode prevented: stats growth proportional to
// provider error payload sizes.
func TestRecordCallTrimsError(t *testing.T) {
	d := testDB(t)
	p := &Plugin{}
	p.SetCallStatsDB(d)
	p.RecordCall(llm.CallStats{OK: false, Error: strings.Repeat("x", 4000)})

	var stored string
	if err := d.QueryRow(`SELECT error FROM ct_llm_inferencer_stats`).Scan(&stored); err != nil {
		t.Fatalf("select error: %v", err)
	}
	if got := utf8.RuneCountInString(stored); got > 501 {
		t.Fatalf("stored %d runes, want ≤ 501 (500 + ellipsis)", got)
	}
	if !strings.HasSuffix(stored, "…") {
		t.Fatalf("trimmed text lacks ellipsis: %q", stored)
	}
}

// TestClearStatsAction: the clear_stats action wipes the whole ledger,
// unknown actions 404 via notetype.ErrUnknownAction, and a missing
// database yields a BadRequestError (400), not a 500. Failure mode
// prevented: a destructive action reachable in a broken state.
func TestClearStatsAction(t *testing.T) {
	p := notetype.Registry[PluginID].(*Plugin)

	t.Run("clears", func(t *testing.T) {
		d := testDB(t)
		record(t, d,
			callSpec{usage: "title", source: llm.ChatSourceConfig, model: "m", ok: true, total: 3, duration: 1},
			callSpec{usage: "rss_classify", source: llm.ChatSourceNote, provider: "openrouter", model: "m", ok: true, total: 10, duration: 1},
		)
		if n := countRows(t, d); n != 2 {
			t.Fatalf("setup wrote %d rows, want 2", n)
		}
		res, err := p.HandleAction(context.Background(), d, 1, 0, "clear_stats", nil)
		if err != nil {
			t.Fatalf("clear_stats: %v", err)
		}
		if m, ok := res.(map[string]any); !ok || m["cleared"] != true {
			t.Fatalf("clear_stats result = %+v", res)
		}
		if n := countRows(t, d); n != 0 {
			t.Fatalf("clear_stats left %d rows", n)
		}
	})

	t.Run("requires_db", func(t *testing.T) {
		_, err := p.HandleAction(context.Background(), nil, 1, 0, "clear_stats", nil)
		var bad *notetype.BadRequestError
		if !errors.As(err, &bad) {
			t.Fatalf("want BadRequestError for nil db, got %v", err)
		}
	})

	t.Run("unknown_action_errors", func(t *testing.T) {
		d := testDB(t)
		_, err := p.HandleAction(context.Background(), d, 1, 0, "definitely_not_an_action", nil)
		if !errors.Is(err, notetype.ErrUnknownAction) {
			t.Fatalf("want ErrUnknownAction, got %v", err)
		}
	})
}

// TestStatsRetentionSweep: only rows older than the retention window are
// deleted; fresh rows survive; a nil db is a no-op. Failure mode
// prevented: the daily job eating fresh statistics (or panicking when
// a one-shot tool calls it without a db).
func TestStatsRetentionSweep(t *testing.T) {
	t.Run("nil_db_noop", func(t *testing.T) {
		if n, err := sweepStats(nil); err != nil || n != 0 {
			t.Fatalf("sweepStats(nil) = (%d, %v), want (0, nil)", n, err)
		}
	})

	t.Run("sweeps_only_old", func(t *testing.T) {
		d := testDB(t)
		for i := 0; i < 3; i++ {
			p := &Plugin{}
			p.SetCallStatsDB(d)
			p.RecordCall(llm.CallStats{OK: true, Usage: "title"})
		}
		// Backdate the first row far past the window.
		if _, err := d.Exec(
			`UPDATE ct_llm_inferencer_stats SET created_at = datetime('now', ?) WHERE id = 1`,
			strings.ReplaceAll("-100 days", "'", "")); err != nil {
			t.Fatalf("backdate: %v", err)
		}

		n, err := sweepStats(d)
		if err != nil {
			t.Fatalf("sweepStats: %v", err)
		}
		if n != 1 {
			t.Fatalf("sweepStats deleted %d rows, want exactly the backdated one", n)
		}
		if left := countRows(t, d); left != 2 {
			t.Fatalf("fresh rows eaten: %d left, want 2", left)
		}

		// Idempotence: a second sweep is a no-op.
		if n, err := sweepStats(d); err != nil || n != 0 {
			t.Fatalf("second sweep = (%d, %v), want (0, nil)", n, err)
		}
	})
}

// TestStatsCronJob: the retention job's manifest contract — exactly one
// named daily job whose task runs against a database and reports a
// human-readable result. Failure mode prevented: a cron entry that
// panics the scheduler or an unrunnable task.
func TestStatsCronJob(t *testing.T) {
	cronJobs := (&Plugin{}).CronJobs()
	if len(cronJobs) != 1 {
		t.Fatalf("want exactly 1 cron job, got %d", len(cronJobs))
	}
	job := cronJobs[0]
	if job.Name != "stats_retention" || job.Schedule == "" || job.Task == nil {
		t.Fatalf("cron contract broken: %+v", job)
	}

	d := testDB(t)
	res, err := job.Task(context.Background(), &jobs.RunContext{DB: d})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	if !strings.HasPrefix(res, "deleted 0") {
		t.Fatalf("task result = %q, want a 'deleted 0 …' summary", res)
	}
}

// TestStatsUsageLabel: ledger labels resolve from the usage catalog,
// with the pseudo usages given their own labels and unknown ids passed
// through verbatim. Failure mode prevented: statistics rows rendering
// raw ids (or crashing) for every new usage added to the catalog.
func TestStatsUsageLabel(t *testing.T) {
	cases := map[string]struct {
		id   string
		want string
	}{
		// Catalog usage ids get their label.
		"rss_classify": {id: "rss_classify", want: "RSS Evaluation"},
		"title":        {id: "title", want: "Note Title Generation"},
		"tags":         {id: "tags", want: "Auto Tags"},
		"subtasks":     {id: "subtasks", want: "Subtask Generation"},
		// The note's connection-test pseudo usage.
		"test": {id: "test", want: "Connection Test"},
		// Untagged clients must be visible as such, not as "".
		"untagged": {id: "", want: "(untagged)"},
		// Unknown ids surface raw instead of vanishing.
		"unknown": {id: "future-usage", want: "future-usage"},
	}
	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := statsUsageLabel(tc.id); got != tc.want {
				t.Fatalf("statsUsageLabel(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

// TestBuildViewAttachesStats: the statistics block ships with the note
// view — with a saved config, without one (the ledger is app-wide and
// predates the note), and stays present (empty but valid) on error
// paths rather than failing the whole view. Failure mode prevented:
// the note view breaking because stats exist (or don't).
func TestBuildViewAttachesStats(t *testing.T) {
	p := notetype.Registry[PluginID].(notetype.ViewBuilder)

	t.Run("with_config", func(t *testing.T) {
		d := testDB(t)
		noteID := createNote(t, d, "LLM", PluginID, 0)
		saveCfg(t, d, noteID, validPayload)
		record(t, d, callSpec{usage: "rss_classify", source: llm.ChatSourceNote, provider: "openrouter",
			model: "prov/small", ok: true, prompt: 1, completion: 1, total: 2, duration: 3})

		view, err := p.BuildView(context.Background(), d, 1, noteID)
		if err != nil {
			t.Fatalf("BuildView: %v", err)
		}
		v := view.(View)
		if v.Stats == nil || len(v.Stats.Rows) != 1 {
			t.Fatalf("stats = %+v, want 1 row", v.Stats)
		}
		if v.Stats.Rows[0].Usage != "rss_classify" || v.Stats.Remote.TotalTokens != 2 {
			t.Fatalf("row = %+v", v.Stats.Rows[0])
		}
	})

	t.Run("without_config", func(t *testing.T) {
		d := testDB(t)
		noteID := createNote(t, d, "LLM", PluginID, 0)
		record(t, d, callSpec{usage: "title", source: llm.ChatSourceConfig, model: "m",
			ok: true, prompt: 7, completion: 8, total: 15, duration: 1})

		view, err := p.BuildView(context.Background(), d, 1, noteID)
		if err != nil {
			t.Fatalf("BuildView: %v", err)
		}
		v := view.(View)
		if v.Stats == nil || len(v.Stats.Rows) != 1 {
			t.Fatalf("stats = %+v, want app-wide stats despite unsaved note", v.Stats)
		}
		if v.Stats.Local.TotalTokens != 15 {
			t.Fatalf("local tokens = %d, want 15", v.Stats.Local.TotalTokens)
		}
	})

	t.Run("empty", func(t *testing.T) {
		d := testDB(t)
		noteID := createNote(t, d, "LLM", PluginID, 0)
		view, err := p.BuildView(context.Background(), d, 1, noteID)
		if err != nil {
			t.Fatalf("BuildView: %v", err)
		}
		v := view.(View)
		if v.Stats == nil || len(v.Stats.Rows) != 0 {
			t.Fatalf("stats = %+v, want empty-but-present", v.Stats)
		}
	})
}

// TestStatsAggregationPBT: property-based — for arbitrary batches of
// calls across usages, routings, models, and outcomes, the aggregates
// must satisfy: (1) totals equal the sum over rows, (2) local + remote
// equal totals per metric, (3) every (usage, source, model) group's
// calls/errors/tokens match the batch, (4) recent shows the newest
// first and is capped, (5) failed groups contribute zero tokens.
// Failure mode prevented: aggregate drift at any batch shape.
func TestStatsAggregationPBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 40).Draw(t, "n")

		// Fresh ledger per property run.
		handle, err := db.OpenInMemory()
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		defer handle.Close()
		if _, err := handle.Exec(`CREATE TABLE IF NOT EXISTS notes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'standard',
			pinned INTEGER NOT NULL DEFAULT 0,
			parent_id INTEGER REFERENCES notes(id) ON DELETE SET NULL
		)`); err != nil {
			t.Fatalf("notes: %v", err)
		}
		p := &Plugin{}
		if err := p.InitSchema(handle.DB); err != nil {
			t.Fatalf("schema: %v", err)
		}
		p.SetCallStatsDB(handle.DB)

		// Generate the batch: usage/source/model/outcome mix.
		type group struct {
			calls, errors             int64
			prompt, completion, total int64
		}
		expected := map[[4]string]*group{}
		var expectRecent []callSpec

		for i := 0; i < n; i++ {
			c := callSpec{
				usage:      rapid.SampledFrom([]string{"rss_classify", "title", "tags", "subtasks", "test", "custom"}).Draw(t, "usage"),
				source:     rapid.SampledFrom([]string{llm.ChatSourceNote, llm.ChatSourceLocal, llm.ChatSourceConfig}).Draw(t, "source"),
				provider:   rapid.SampledFrom([]string{"", "openrouter", "mistral", "compatible"}).Draw(t, "provider"),
				baseURL:    rapid.SampledFrom([]string{"http://localhost:8080", "https://openrouter.ai/api"}).Draw(t, "base"),
				model:      rapid.StringMatching(`model-[a-z0-9]{1,8}`).Draw(t, "model"),
				ok:         rapid.Bool().Draw(t, "ok"),
				prompt:     rapid.IntRange(0, 5000).Draw(t, "prompt"),
				completion: rapid.IntRange(0, 5000).Draw(t, "completion"),
				duration:   int64(rapid.IntRange(0, 30_000).Draw(t, "dur")),
			}
			if c.ok {
				c.total = c.prompt + c.completion
				c.errText = ""
			} else {
				c.errText = "boom " + c.model
			}
			// Provider only matters for note-routed calls.
			if c.source != llm.ChatSourceNote {
				c.provider = ""
			}

			p.RecordCall(c.stats())
			expectRecent = append(expectRecent, c)

			key := [4]string{c.usage, c.source, c.provider, c.model}
			g := expected[key]
			if g == nil {
				g = &group{}
				expected[key] = g
			}
			g.calls++
			if !c.ok {
				g.errors++
			} else {
				g.prompt += int64(c.prompt)
				g.completion += int64(c.completion)
				g.total += int64(c.total)
			}
		}

		view, err := loadStatsView(handle.DB)
		if err != nil {
			t.Fatalf("loadStatsView: %v", err)
		}

		// (1)+(2) roll-ups from the batch, not from the rows.
		var tot, loc, rem StatsSplit
		for _, c := range expectRecent {
			// Transport contract: failed calls carry no token counts
			// (stats() zeroes them), so the roll-ups only add them for
			// successful calls.
			tot.Calls++
			if c.ok {
				tot.PromptTokens += int64(c.prompt)
				tot.CompletionTokens += int64(c.completion)
				tot.TotalTokens += int64(c.total)
			} else {
				tot.Errors++
			}
			dst := &loc
			if c.source == llm.ChatSourceNote {
				dst = &rem
			}
			dst.Calls++
			if c.ok {
				dst.PromptTokens += int64(c.prompt)
				dst.CompletionTokens += int64(c.completion)
				dst.TotalTokens += int64(c.total)
			} else {
				dst.Errors++
			}
		}
		if diff := cmp.Diff(tot, view.Totals); diff != "" {
			t.Fatalf("totals drift (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(rem, view.Remote); diff != "" {
			t.Fatalf("remote split drift (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(loc, view.Local); diff != "" {
			t.Fatalf("local split drift (-want +got):\n%s", diff)
		}

		// (3) per-group aggregates. Rows are grouped by
		// usage×routing×provider×model — same usage/source/model on two
		// different providers are two rows (each shows its provider).
		if len(view.Rows) != len(expected) {
			t.Fatalf("%d rows, want %d groups", len(view.Rows), len(expected))
		}
		for _, r := range view.Rows {
			g, ok := expected[[4]string{r.Usage, r.Source, r.Provider, r.Model}]
			if !ok {
				t.Fatalf("unexpected group %+v", r)
			}
			if r.Calls != g.calls || r.Errors != g.errors {
				t.Fatalf("group %v calls/errors = (%d,%d), want (%d,%d)",
					r.Usage, r.Calls, r.Errors, g.calls, g.errors)
			}
			if r.PromptTokens != g.prompt || r.CompletionTokens != g.completion || r.TotalTokens != g.total {
				t.Fatalf("group %v tokens = (%d,%d,%d), want (%d,%d,%d)",
					r.Usage, r.PromptTokens, r.CompletionTokens, r.TotalTokens,
					g.prompt, g.completion, g.total)
			}
			// (5) failed groups: prompt and completion are zero.
			if g.calls == g.errors && (r.PromptTokens != 0 || r.CompletionTokens != 0) {
				t.Fatalf("all-failed group %v carries tokens %+v", r.Usage, r)
			}
		}

		// (4) recent = newest first, capped at 15.
		want := n
		if want > 15 {
			want = 15
		}
		if len(view.Recent) != want {
			t.Fatalf("recent = %d, want %d", len(view.Recent), want)
		}
		if want > 0 {
			last := expectRecent[len(expectRecent)-1]
			got := view.Recent[0]
			if got.Usage != last.usage || got.Source != last.source || got.Model != last.model || got.OK != last.ok {
				t.Fatalf("recent[0] = %+v, want last call %+v", got, last)
			}
		}
	})
}
