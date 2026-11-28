package jobs

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// --- schedule parsing ---

func TestParseSimpleSchedule(t *testing.T) {
	t.Parallel()

	// The scheduler's entire safety rails live here: an accepted
	// non-positive interval would spin a hot tick loop, a rejected
	// cron-matrix string must not silently disable a job that the
	// config default promised, and every shape parses to the interval
	// the docs claim.
	cases := map[string]struct {
		want   time.Duration
		wantOK bool
		why    string
	}{
		"@every 15m": {
			// The rss reader's own refresh cadence — the most common
			// schedule in the app.
			want: 15 * time.Minute, wantOK: true,
			why: "documented fixed-interval form",
		},
		"@every 1h30m": {
			// Compound durations must compose, not truncate.
			want: 90 * time.Minute, wantOK: true,
			why: "compound duration parsing",
		},
		"@every 10ms": {
			// Sub-second intervals are legitimate for tests and very
			// fast jobs.
			want: 10 * time.Millisecond, wantOK: true,
			why: "sub-second support",
		},
		"@daily": {
			// The builtin janitor uses this alias.
			want: 24 * time.Hour, wantOK: true,
			why: "daily alias",
		},
		"@hourly": {
			want: time.Hour, wantOK: true,
			why: "hourly alias",
		},
		"@every 0s": {
			// Prevents a hot spin loop: a zero interval accepted here
			// would make runScheduler tick with time.After(0) and
			// hammer the DB checks continuously.
			want: 0, wantOK: false,
			why: "zero interval is fatal, must be rejected",
		},
		"@every -5m": {
			// Prevents a negative interval short-circuiting every
			// tick delay (same hot-loop failure mode as zero).
			want: 0, wantOK: false,
			why: "negative interval is fatal, must be rejected",
		},
		"@every notaduration": {
			// Prevents config typos being ignored as "ad-hoc only" —
			// the definition must visibly stop being scheduled.
			want: 0, wantOK: false,
			why: "unparseable duration is unsupported",
		},
		"@every": {
			// Missing operand must not match the HasPrefix branch
			// into a zero parse.
			want: 0, wantOK: false,
			why: "missing duration operand",
		},
		"@weekly": {
			// Guards the documented feature boundary: unsupported
			// aliases are silently skipped today; if they gained
			// support this test pins the value.
			want: 0, wantOK: false,
			why: "unsupported alias",
		},
		"": {
			// Empty is valid (ad-hoc only) but never cron-scheduled.
			want: 0, wantOK: false,
			why: "empty is ad-hoc only",
		},
		"*/5 * * * *": {
			// Prevents true cron syntax being half-interpreted as
			// something exotic.
			want: 0, wantOK: false,
			why: "cron-matrix syntax unsupported",
		},
	}

	for schedule, tc := range cases {
		t.Run(schedule, func(t *testing.T) {
			t.Parallel()
			got, ok := parseSimpleSchedule(schedule)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("parseSimpleSchedule(%q) = (%v, %v), want (%v, %v): %s",
					schedule, got, ok, tc.want, tc.wantOK, tc.why)
			}
		})
	}
}

// TestNextTickDelayBounded pins the sleeping behavior of schedulers:
// the delay is always > 0 (a zero delay would busy-loop) and never
// exceeds the schedule interval.
func TestNextTickDelayBounded(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		schedule string
		interval time.Duration
		max      time.Duration
	}{
		// Fixed intervals sleep exactly the interval: drift must not
		// accrue into watchdog-timeout territory for long-running jobs.
		"@every": {schedule: "@every 5m", interval: 5 * time.Minute, max: 5 * time.Minute},
		// Calendar schedules align to the boundary: the wait is
		// bounded by the interval by construction.
		"@daily":  {schedule: "@daily", interval: 24 * time.Hour, max: 24 * time.Hour},
		"@hourly": {schedule: "@hourly", interval: time.Hour, max: time.Hour},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := nextTickDelay(tc.schedule, tc.interval)
			if d <= 0 {
				t.Fatalf("nextTickDelay(%q) = %v, want > 0 (a zero/negative delay busy-loops the scheduler)", tc.schedule, d)
			}
			if d > tc.max {
				t.Fatalf("nextTickDelay(%q) = %v, want <= %v", tc.schedule, d, tc.max)
			}
		})
	}

	// Property: repeated calendar delays must average about interval/N…
	// no — the honest invariant is each single delay ∈ (0, interval].
	rapid.Check(t, func(rt *rapid.T) {
		schedule := rapid.SampledFrom([]string{"@daily", "@hourly"}).Draw(rt, "schedule")
		interval := 24 * time.Hour
		if strings.HasPrefix(schedule, "@hourly") {
			interval = time.Hour
		}
		d := nextTickDelay(schedule, interval)
		if d <= 0 || d > interval {
			rt.Fatalf("nextTickDelay(%q) = %v, want in (0, %v]", schedule, d, interval)
		}
	})
}

// --- scheduler lifecycle ---

func TestRunSchedulerExitsOnUnsupportedSchedule(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	// An alias that gains a typo in code ("@weekly" today) must stop
	// the scheduler goroutine cleanly — silently keeping it alive
	// with no ticks hides a scheduling outage.
	if err := m.UpsertDefinitions("test", []CronJob{{
		Name:     "broken",
		Schedule: "@weekly",
		Task:     func(context.Context, *RunContext) (string, error) { return "", nil },
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	returned := make(chan struct{})
	go func() { m.runScheduler(1, "@weekly", &wg); close(returned) }()

	select {
	case <-returned:
		wg.Wait() // must decrement exactly once: no goroutine leak
	case <-time.After(5 * time.Second):
		t.Fatal("runScheduler did not exit on an unsupported schedule")
	}
}

func TestRegisterAdHocNeverSchedules(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	// Ad-hoc registration is the contract for interactive jobs: even
	// if a caller forgets to blank the schedule, the manager must —
	// otherwise an ad-hoc job (e.g. per-note title generation)
	// would also fire on a timer without anyone asking.
	if err := m.RegisterAdHoc("interactive", []CronJob{{
		Name:     "title",
		Schedule: "@hourly", // caller error: must be neutralized
		Task:     func(context.Context, *RunContext) (string, error) { return "generated", nil },
	}}); err != nil {
		t.Fatalf("RegisterAdHoc: %v", err)
	}

	var stored string
	if err := d.QueryRow(`SELECT schedule FROM job_definitions WHERE plugin_id = 'interactive' AND name = 'title'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "" {
		t.Fatalf("ad-hoc definition persisted schedule %q, want empty", stored)
	}

	events := make(chan RunEvent, 8)
	m.SetObserver(func(evt RunEvent) {
		if evt.PluginID == "interactive" {
			events <- evt
		}
	})
	startManager(t, m)

	// The task is wired and runnable through Enqueue only.
	runID, err := m.Enqueue("interactive", "title", []byte(`{"note_id":5}`))
	if err != nil {
		t.Fatalf("enqueue ad-hoc job: %v", err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt := <-events:
			if evt.Type == "done" && evt.RunID == runID {
				return // ad-hoc execution works
			}
		case <-deadline:
			t.Fatal("ad-hoc job never executed via Enqueue")
		}
	}
}

func TestSchedulerSkipsDisabledAndDeletedDefinitions(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	firstTick := make(chan struct{}, 1)
	var once sync.Once
	m.SetObserver(func(evt RunEvent) {
		if evt.PluginID == "test" && evt.Type == "enqueued" {
			once.Do(func() { firstTick <- struct{}{} })
		}
	})

	if err := m.UpsertDefinitions("test", []CronJob{{
		Name:     "ticker",
		Schedule: "@every 20ms",
		Task:     func(context.Context, *RunContext) (string, error) { return "ok", nil },
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	// Wait for the first scheduler-produced run — event-driven, no
	// time.Sleep sync.
	select {
	case <-firstTick:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler never enqueued its first tick")
	}

	// Disable: ticks must stop within a few intervals (the enabled
	// check is re-read each tick; disabled must not enqueue).
	if _, err := d.Exec(`UPDATE job_definitions SET enabled = 0 WHERE plugin_id = 'test'`); err != nil {
		t.Fatalf("disable definition: %v", err)
	}
	count := func() int {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM job_runs`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	// Observation window of ~15 tick intervals; runs accumulate fast
	// while enabled (~50/s here), so stability proves the stop.
	time.Sleep(300 * time.Millisecond)
	if after := count(); after != before {
		t.Fatalf("runs grew %d → %d while the definition was disabled", before, after)
	}

	// Delete the definition row entirely: the scheduler must survive
	// a failing enabled-lookup (continue loop, no goroutine death).
	if _, err := d.Exec(`DELETE FROM job_definitions WHERE plugin_id = 'test'`); err != nil {
		t.Fatalf("delete definition: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	// Still alive means Stop can shut the manager down cleanly (the
	// deferred Stop in startManager asserts there is no hang).
}
