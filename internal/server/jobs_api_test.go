package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/i5heu/MentisEterna/internal/jobs"
)

// enqueueAndWait starts a run of the given task inline (no manager
// workers needed) and waits until it is claimed: manual claim through
// the public API keeps the test hermetic.
func runJobInline(t *testing.T, s *Server, name string, task jobs.TaskFunc, payload []byte) int64 {
	t.Helper()
	if err := s.jobManager.RegisterAdHoc("test", []jobs.CronJob{{Name: name, Task: task}}); err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
	runID, err := s.jobManager.Enqueue("test", name, payload)
	if err != nil {
		t.Fatalf("enqueue %s: %v", name, err)
	}
	return runID
}

// TestJobsStatsEndpoint verifies GET /jobs/stats: worker pools, queue
// depth, the 24h window, and the per-definition ledger — through the
// real HTTP handler with the real manager running.
func TestJobsStatsEndpoint(t *testing.T) {
	s := newTestServer(t)

	// Two completed runs through the real job system.
	runID1 := runJobInline(t, s, "quick", func(context.Context, *jobs.RunContext) (string, error) {
		return "ok 1", nil
	}, nil)
	runID2 := runJobInline(t, s, "bad", func(context.Context, *jobs.RunContext) (string, error) {
		return "", fmt.Errorf("nope")
	}, []byte(`{"x":1}`))

	token := createTestSession(t, s)

	// Start the manager so the queued runs execute, then wait for both
	// to reach terminal states.
	if err := s.jobManager.Start(); err != nil {
		t.Fatalf("start job manager: %v", err)
	}
	defer s.jobManager.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := s.jobManager.ListRuns(10)
		if err == nil {
			states := map[int64]string{}
			for _, r := range resp.Runs {
				states[r.ID] = r.Status
			}
			if states[runID1] == jobs.StatusDone && states[runID2] == jobs.StatusErrored {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/jobs/stats", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	s.handleJobStats(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var stats jobs.StatsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if stats.Workers.Total < 1 || stats.Workers.FastTotal < 1 {
		t.Fatalf("worker stats = %+v", stats.Workers)
	}
	if stats.Last24h.Done != 1 || stats.Last24h.Errored != 1 {
		t.Fatalf("24h window = %+v", stats.Last24h)
	}

	byJob := map[string]jobs.DefinitionStats{}
	for _, d := range stats.Definitions {
		if d.PluginID == "test" {
			byJob[d.Name] = d
		}
	}
	quick, ok := byJob["quick"]
	if !ok {
		t.Fatalf("quick definition missing from %+v", stats.Definitions)
	}
	if quick.RunsTotal != 1 || quick.RunsDone != 1 || quick.SuccessRate != 1.0 {
		t.Fatalf("quick ledger = %+v", quick)
	}
	if quick.LastStatus == nil || *quick.LastStatus != jobs.StatusDone {
		t.Fatalf("quick last status = %+v", quick.LastStatus)
	}
	bad, ok := byJob["bad"]
	if !ok {
		t.Fatalf("bad definition missing from %+v", stats.Definitions)
	}
	if bad.RunsErrored != 1 || bad.SuccessRate != 0 {
		t.Fatalf("bad ledger = %+v", bad)
	}
	if bad.LastError == nil || *bad.LastError != "nope" {
		t.Fatalf("bad last error = %+v", bad.LastError)
	}
}

// TestJobsListEndpointShape verifies GET /jobs carries the v2 fields
// (progress, duration_ms, priority, worker_id).
func TestJobsListEndpointShape(t *testing.T) {
	s := newTestServer(t)
	token := createTestSession(t, s)

	runID := runJobInline(t, s, "progress_job", func(_ context.Context, rt *jobs.RunContext) (string, error) {
		rt.Progress(3, 5, "midway")
		return "worked", nil
	}, []byte(`{"k":1}`))

	if err := s.jobManager.Start(); err != nil {
		t.Fatalf("start job manager: %v", err)
	}
	defer s.jobManager.Stop()

	done := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !done {
		resp, err := s.jobManager.ListRuns(10)
		if err == nil {
			for _, r := range resp.Runs {
				if r.ID == runID && r.Status == jobs.StatusDone {
					done = true
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !done {
		t.Fatal("job never finished")
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/jobs", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	s.handleJobs(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var list jobs.ListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var found *jobs.Run
	for i := range list.Runs {
		if list.Runs[i].ID == runID {
			found = &list.Runs[i]
		}
	}
	if found == nil {
		t.Fatalf("run %d missing from /jobs", runID)
	}
	if found.DurationMS == nil || *found.DurationMS < 0 {
		t.Fatalf("duration_ms missing: %+v", found)
	}
	if found.Priority != 0 {
		t.Fatalf("priority = %d, want 0", found.Priority)
	}
	if found.RetryCount != 0 {
		t.Fatalf("retry_count = %d, want 0", found.RetryCount)
	}
}
