package server

import (
	"context"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/i5heu/MentisEterna/internal/jobs"
)

// waitForLiveJob reads websocket messages until one arrives that has
// the given live type, the given run id, and satisfies check.
// Unrelated traffic (other live types, other runs, earlier lifecycle
// events) is skipped — several jobs.changed events legitimately
// interleave with the event under test.
func waitForLiveJob(t *testing.T, conn *websocket.Conn, wantType string, runID int64, check func(*jobs.RunEvent) bool) liveMessage {
	t.Helper()
	// Generous overall budget: each read gets its own 3 s deadline,
	// and the expected events arrive within milliseconds.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msg := readLiveMessage(t, conn)
		if msg.Type != wantType || msg.Job == nil || msg.Job.RunID != runID {
			continue
		}
		if check != nil && !check(msg.Job) {
			continue
		}
		return msg
	}
	t.Fatalf("no %s event for run %d arrived in time", wantType, runID)
	return liveMessage{}
}

// TestJobsProgressStreamsOverWebSocket is the end-to-end wiring test
// for the job observer → live hub → websocket pipeline with a REAL
// running worker: lifecycle events (enqueued, running, done) must ride
// the jobs.changed type while live progress rides the dedicated
// jobs.progress type, with the run event payload intact. If the frame
// format drifts, this is the test that notices.
func TestJobsProgressStreamsOverWebSocket(t *testing.T) {
	s := newTestServer(t)
	token := createTestSession(t, s)
	httpServer, wsURL := newLiveTestHTTPServer(t, s)

	conn, _, err := dialLiveWebSocket(t, wsURL, token, httpServer.URL)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()
	requireLiveMessageType(t, conn, liveTypeReady)

	// A task that reports progress, blocks until the test has seen the
	// first progress frame, then finishes — the run stays executing
	// across the websocket assertions.
	inTask := make(chan struct{})
	release := make(chan struct{})
	if err := s.jobManager.RegisterAdHoc("test", []jobs.CronJob{{
		Name: "ws_progress",
		Task: func(_ context.Context, rt *jobs.RunContext) (string, error) {
			rt.Progress(1, 3, "step one")
			close(inTask)
			<-release
			rt.Progress(3, 3, "all done")
			return "ws ok", nil
		},
	}}); err != nil {
		t.Fatalf("register ad-hoc job: %v", err)
	}

	if err := s.jobManager.Start(); err != nil {
		t.Fatalf("start job manager: %v", err)
	}
	defer s.jobManager.Stop()

	runID, err := s.jobManager.Enqueue("test", "ws_progress", []byte(`{"k":1}`))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Routing rule: lifecycle events ride jobs.changed. The scans only
	// pass if enqueued AND running arrived on that exact type.
	_ = waitForLiveJob(t, conn, liveTypeJobsChange, runID, func(j *jobs.RunEvent) bool {
		return j.Type == "enqueued" && j.Status == jobs.StatusPlanned
	})
	_ = waitForLiveJob(t, conn, liveTypeJobsChange, runID, func(j *jobs.RunEvent) bool {
		return j.Type == "running" && j.Status == jobs.StatusRunning
	})

	// The progress event rides the dedicated type with the full payload.
	prog := waitForLiveJob(t, conn, liveTypeJobsProgress, runID, func(j *jobs.RunEvent) bool {
		return j.ProgressDone != nil && *j.ProgressDone == 1
	})
	if prog.Job == nil {
		t.Fatal("jobs.progress frame carries no job payload")
	}
	// Frame shape: identity, progress triple, and status.
	if prog.Job.PluginID != "test" || prog.Job.JobName != "ws_progress" {
		t.Fatalf("progress event identity = %s/%s", prog.Job.PluginID, prog.Job.JobName)
	}
	if prog.Job.ProgressTotal == nil || *prog.Job.ProgressTotal != 3 {
		t.Fatalf("progress total = %+v, want 3", prog.Job.ProgressTotal)
	}
	if prog.Job.ProgressMessage == nil || *prog.Job.ProgressMessage != "step one" {
		t.Fatalf("progress message = %+v, want %q", prog.Job.ProgressMessage, "step one")
	}
	if prog.Job.Status != jobs.StatusRunning {
		t.Fatalf("progress event status = %q, want running", prog.Job.Status)
	}
	if prog.Timestamp == "" {
		t.Error("jobs.progress frame has no timestamp")
	}

	// The task is still executing at this point — the frame we saw was
	// live, not a replay.
	select {
	case <-inTask:
	default:
		t.Fatal("task not started — progress frame was not live")
	}

	close(release)

	// Final progress (done == total always emits) and the terminal
	// done event on jobs.changed, with result and duration.
	_ = waitForLiveJob(t, conn, liveTypeJobsProgress, runID, func(j *jobs.RunEvent) bool {
		return j.ProgressDone != nil && *j.ProgressDone == 3 &&
			j.ProgressMessage != nil && *j.ProgressMessage == "all done"
	})
	done := waitForLiveJob(t, conn, liveTypeJobsChange, runID, func(j *jobs.RunEvent) bool {
		return j.Type == "done"
	})
	if done.Job.Status != jobs.StatusDone {
		t.Fatalf("done event status = %q", done.Job.Status)
	}
	if done.Job.Result == nil || *done.Job.Result != "ws ok" {
		t.Fatalf("done event result = %+v, want %q", done.Job.Result, "ws ok")
	}
	if done.Job.DurationMS == nil || *done.Job.DurationMS < 0 {
		t.Fatalf("done event duration = %+v, want non-negative", done.Job.DurationMS)
	}
}

// TestJobsCancelVisibleOverWebSocket closes the loop for the queue UI:
// cancelling a queued run through the public API must surface as a
// jobs.changed frame so every connected client refreshes its queue.
func TestJobsCancelVisibleOverWebSocket(t *testing.T) {
	s := newTestServer(t)
	token := createTestSession(t, s)
	httpServer, wsURL := newLiveTestHTTPServer(t, s)

	conn, _, err := dialLiveWebSocket(t, wsURL, token, httpServer.URL)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()
	requireLiveMessageType(t, conn, liveTypeReady)

	if err := s.jobManager.RegisterAdHoc("test", []jobs.CronJob{{
		Name: "ws_cancel",
		Task: func(context.Context, *jobs.RunContext) (string, error) {
			return "never", nil
		},
	}}); err != nil {
		t.Fatalf("register ad-hoc job: %v", err)
	}

	// Manager stays UNSTARTED: the planned run cannot be claimed, so
	// the cancel always wins the terminal transition.
	runID, err := s.jobManager.Enqueue("test", "ws_cancel", nil)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	_ = waitForLiveJob(t, conn, liveTypeJobsChange, runID, func(j *jobs.RunEvent) bool {
		return j.Type == "enqueued"
	})

	if err := s.jobManager.CancelRun(runID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	evt := waitForLiveJob(t, conn, liveTypeJobsChange, runID, func(j *jobs.RunEvent) bool {
		return j.Type == "cancelled"
	})
	if evt.Job.Status != jobs.StatusCancelled {
		t.Fatalf("cancel event status = %q", evt.Job.Status)
	}
}
