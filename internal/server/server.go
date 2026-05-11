package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/internal/jobs"
	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/internal/media"
	"github.com/i5heu/MentisEterna/pkg/notetype"
)

type Server struct {
	db           *db.DB
	addr         string
	llm          llm.Embedder
	webauthn     *webauthn.WebAuthn
	sessionStore *webAuthnSessionStore
	jobManager   *jobs.Manager
	mediaService *media.Service
}

func New(d *db.DB, addr string, embeddingClient llm.Embedder) *Server {
	wconfig := &webauthn.Config{
		RPID:                  "localhost",
		RPDisplayName:         "MentisEterna",
		RPOrigins:             []string{"http://localhost:8080", "https://localhost:8080"},
		AttestationPreference: "none",
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			AuthenticatorAttachment: "platform",
			RequireResidentKey:      protocol.ResidentKeyRequired(),
			UserVerification:        protocol.VerificationRequired,
		},
		Timeouts: webauthn.TimeoutsConfig{
			Login: webauthn.TimeoutConfig{
				Enforce:    false,
				Timeout:    300000 * time.Millisecond,
				TimeoutUVD: 120000 * time.Millisecond,
			},
			Registration: webauthn.TimeoutConfig{
				Enforce:    false,
				Timeout:    300000 * time.Millisecond,
				TimeoutUVD: 120000 * time.Millisecond,
			},
		},
	}
	w, err := webauthn.New(wconfig)
	if err != nil {
		log.Fatalf("webauthn: %v", err)
	}

	jobMgr := jobs.NewManager(d.DB, 2)

	// Media subsystem: set up cache, S3 store, and the service orchestrator.
	var mediaSvc *media.Service
	mediaCfg, cfgErr := media.LoadConfigFromEnv()
	if cfgErr != nil {
		log.Printf("media: not enabled (%v)", cfgErr)
	} else {
		mediaSvc = media.NewService(d, mediaCfg)
		// EnqueueFunc will be wired after jobMgr is started.
		log.Printf("media: enabled with %d endpoint(s), cache=%s", len(mediaCfg.Endpoints), mediaCfg.CacheDir)
	}

	return &Server{
		db:           d,
		addr:         addr,
		llm:          embeddingClient,
		webauthn:     w,
		sessionStore: newWebAuthnSessionStore(),
		jobManager:   jobMgr,
		mediaService: mediaSvc,
	}
}

func (s *Server) Start(ctx context.Context) error {
	initAdminPassword(s.db)

	// Initialize all registered plugin schemas.
	for _, plugin := range notetype.Registry {
		if err := plugin.InitSchema(s.db.DB); err != nil {
			log.Fatalf("Failed to initialize schema for plugin %s: %v", plugin.ID(), err)
		}
		log.Printf("plugin %s initialized", plugin.ID())
	}

	// Initialize the job system: upsert plugin cron jobs and start workers.
	for _, plugin := range notetype.Registry {
		pluginJobs := plugin.CronJobs()
		if len(pluginJobs) == 0 {
			continue
		}
		// Convert notetype.CronJob → jobs.CronJob
		jobsForPlugin := make([]jobs.CronJob, len(pluginJobs))
		for i, j := range pluginJobs {
			jobsForPlugin[i] = jobs.CronJob{
				Name:     j.Name,
				Schedule: j.Schedule,
				Task:     j.Task,
			}
		}
		if err := s.jobManager.UpsertDefinitions(plugin.ID(), jobsForPlugin); err != nil {
			log.Fatalf("Failed to register jobs for plugin %s: %v", plugin.ID(), err)
		}
		log.Printf("plugin %s: %d job(s) registered", plugin.ID(), len(jobsForPlugin))
	}

	// Register ad-hoc VSS embedding index job (on-demand, not cron).
	// Must happen before Start() so workers see the task.
	if s.db.VSSAvailable() && s.llm != nil {
		if err := s.jobManager.RegisterAdHoc("_system", []jobs.CronJob{{
			Name: "vss_index",
			Task: s.syncEmbeddingTask,
		}}); err != nil {
			log.Fatalf("Failed to register VSS index job: %v", err)
		}
	}

	// Register media jobs (cron + ad-hoc). Must happen before Start().
	if s.mediaService != nil {
		s.mediaService.EnqueueFunc = s.jobManager.Enqueue
		if err := s.jobManager.UpsertDefinitions("_media", []jobs.CronJob{
			{Name: "repair_replicas", Schedule: "@every 1m", Task: s.mediaService.RepairSweepTask},
			{Name: "cleanup_pending_inline", Schedule: "@every 1h", Task: s.mediaService.PendingInlineCleanupTask},
		}); err != nil {
			log.Fatalf("Failed to register media cron jobs: %v", err)
		}
		if err := s.jobManager.RegisterAdHoc("_media", []jobs.CronJob{
			{Name: "repair_file_replica", Task: s.mediaService.RepairReplicaTask},
			{Name: "delete_file_replica", Task: s.mediaService.DeleteReplicaTask},
		}); err != nil {
			log.Fatalf("Failed to register media ad-hoc jobs: %v", err)
		}
		log.Printf("media: jobs registered")
	}

	// Start workers after all task registrations are complete.
	if err := s.jobManager.Start(); err != nil {
		log.Fatalf("Failed to start job manager: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		s.handleLogin(w, r)
	})
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/notes", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.listNotes(w, r)
		case http.MethodPost:
			s.createNote(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/notes/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.searchNotes(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/notes/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/history") {
			if r.Method == http.MethodGet {
				s.getNoteHistory(w, r)
			} else {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		if strings.HasSuffix(r.URL.Path, "/children") {
			if r.Method == http.MethodGet {
				s.getNoteChildren(w, r)
			} else {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		if strings.HasSuffix(r.URL.Path, "/ancestors") {
			if r.Method == http.MethodGet {
				s.getNoteAncestors(w, r)
			} else {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		if strings.HasSuffix(r.URL.Path, "/action") {
			s.handlePluginAction(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/pin") {
			s.setNotePin(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/files") {
			s.uploadAttachment(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/files/inline") {
			s.uploadInlineFile(w, r)
			return
		}
		// DELETE /notes/:id/files/:fileID
		if idx := strings.LastIndex(r.URL.Path, "/files/"); idx > 0 && r.Method == http.MethodDelete {
			s.deleteAttachment(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			s.getNote(w, r)
		case http.MethodPut:
			s.updateNote(w, r)
		case http.MethodDelete:
			s.deleteNote(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// WebAuthn routes
	mux.HandleFunc("/webauthn/register/begin", s.handleWebAuthnRegisterBegin)
	mux.HandleFunc("/webauthn/register/finish", s.handleWebAuthnRegisterFinish)
	mux.HandleFunc("/webauthn/login/begin", s.handleWebAuthnLoginBegin)
	mux.HandleFunc("/webauthn/login/finish", s.handleWebAuthnLoginFinish)

	// Job routes
	mux.HandleFunc("/jobs", s.handleJobs)
	mux.HandleFunc("/jobs/", s.handleJobByID)

	// File serving endpoint
	mux.HandleFunc("/file/", s.serveFile)

	mux.Handle("/", newSPAHandler("./FrontEndDist"))

	srv := &http.Server{
		Addr:         s.addr,
		Handler:      s.requireAuth(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		s.jobManager.Stop()
	}()

	log.Printf("listening on http://localhost%s", s.addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// --- Job Handlers ---

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.jobManager.ListRuns(50)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleJobByID(w http.ResponseWriter, r *http.Request) {
	// Parse /jobs/123/retry or /jobs/123/cancel
	path := strings.TrimPrefix(r.URL.Path, "/jobs/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "invalid job run id", http.StatusBadRequest)
		return
	}

	switch parts[1] {
	case "retry":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		newID, err := s.jobManager.RetryRun(id)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"retried":    true,
			"new_run_id": newID,
		})
	case "cancel":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.jobManager.CancelRun(id); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"cancelled": true,
		})
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}
