package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/i5heu/MentisEterna/internal/backup"
	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/internal/jobs"
	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/internal/media"
	"github.com/i5heu/MentisEterna/pkg/notetype"
	"github.com/i5heu/MentisEterna/pkg/printer"
)

type Server struct {
	db            *db.DB
	addr          string
	cfg           serverConfig
	llm           llm.Embedder
	chatClient    llm.Generator
	autoTagger    llm.AutoTagger
	ocrClient     llm.OCRer
	sttClient     llm.STTer
	webauthn      *webauthn.WebAuthn
	sessionStore  *webAuthnSessionStore
	loginThrottle *loginThrottle
	jobManager    *jobs.Manager
	mediaService  *media.Service
	backupService *backup.Service
	liveHub       *liveHub
}

func New(d *db.DB, addr string, embeddingClient llm.Embedder, chatClient llm.Generator, ocrClient llm.OCRer, sttClient llm.STTer) *Server {
	cfg := loadServerConfig(addr)
	wconfig := &webauthn.Config{
		RPID:                  cfg.WebAuthnRPID,
		RPDisplayName:         "MentisEterna",
		RPOrigins:             cfg.WebAuthnOrigins,
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

	jobMgr := jobs.NewManager(d.DB, envOrInt("JOB_WORKERS", 10))
	liveHub := newLiveHub()
	jobMgr.SetObserver(func(evt jobs.RunEvent) {
		liveHub.broadcast(liveMessage{
			Type:      liveTypeJobsChange,
			Timestamp: liveTimestamp(),
			Job:       &evt,
		})
	})

	// Media subsystem: set up cache, S3 store, and the service orchestrator.
	var mediaSvc *media.Service
	var mediaEndpoints []media.EndpointConfig
	mediaCfg, cfgErr := media.LoadConfigFromEnv()
	if cfgErr != nil {
		log.Printf("media: not enabled (%v)", cfgErr)
	} else {
		mediaSvc = media.NewService(d, mediaCfg)
		mediaEndpoints = mediaCfg.Endpoints
		// EnqueueFunc will be wired after jobMgr is started.
		log.Printf("media: enabled with %d endpoint(s), cache=%s", len(mediaCfg.Endpoints), mediaCfg.CacheDir)
	}

	// Backup subsystem: AES-256-GCM encrypted SQLite backups to S3.
	var backupSvc *backup.Service
	if hexKey := os.Getenv("BACKUP_ENCRYPTION_KEY"); hexKey != "" {
		key, keyErr := backup.KeyFromHex(hexKey)
		if keyErr != nil {
			log.Printf("backup: invalid BACKUP_ENCRYPTION_KEY (%v) — backups disabled", keyErr)
		} else if len(mediaEndpoints) == 0 {
			log.Printf("backup: encryption key set but no S3 endpoints configured — backups disabled")
		} else {
			backupSvc = backup.NewService(d.DB, media.NewS3Store(), mediaEndpoints, key)
			log.Printf("backup: enabled with %d S3 endpoint(s)", len(mediaEndpoints))
		}
	} else {
		log.Printf("backup: BACKUP_ENCRYPTION_KEY not set — backups disabled")
	}

	var autoTagger llm.AutoTagger
	if at, ok := chatClient.(llm.AutoTagger); ok {
		autoTagger = at
	}

	return &Server{
		db:            d,
		addr:          addr,
		cfg:           cfg,
		llm:           embeddingClient,
		chatClient:    chatClient,
		autoTagger:    autoTagger,
		ocrClient:     ocrClient,
		sttClient:     sttClient,
		webauthn:      w,
		sessionStore:  newWebAuthnSessionStore(),
		loginThrottle: newLoginThrottle(),
		jobManager:    jobMgr,
		mediaService:  mediaSvc,
		backupService: backupSvc,
		liveHub:       liveHub,
	}
}

func envOrInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		log.Printf("server: invalid %s=%q; using default %d", key, v, def)
		return def
	}
	return n
}

func (s *Server) Start(ctx context.Context) error {
	initAdminPassword(s.db)

	// Validate all registered plugins at startup — fail fast on capability mismatches.
	for _, plugin := range notetype.Registry {
		if err := notetype.ValidatePlugin(plugin); err != nil {
			log.Fatalf("plugin validation failed: %v", err)
		}
		log.Printf("plugin %s: validated", plugin.ID())
	}

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

	// Register ad-hoc maintenance jobs (on-demand, not cron).
	// Must happen before Start() so workers see the tasks.
	if s.llm != nil {
		systemJobs := []jobs.CronJob{{
			Name: "recalculate_recipe_ingredient_categories",
			Task: s.recalculateRecipeIngredientCategoriesTask,
		}}
		if s.db.VSSAvailable() {
			systemJobs = append(systemJobs,
				jobs.CronJob{Name: "vss_index", Task: s.syncEmbeddingTask},
				jobs.CronJob{Name: "sync_ocr_embedding", Task: s.syncOCREmbeddingTask},
				jobs.CronJob{Name: "sync_stt_embedding", Task: s.syncSTTEmbeddingTask},
			)
		}
		if err := s.jobManager.RegisterAdHoc("_system", systemJobs); err != nil {
			log.Fatalf("Failed to register system maintenance jobs: %v", err)
		}
	}

	// Register ad-hoc chat-model jobs.
	if s.chatClient != nil || s.autoTagger != nil {
		chatJobs := make([]jobs.CronJob, 0, 2)
		if s.chatClient != nil {
			chatJobs = append(chatJobs, jobs.CronJob{Name: "generate_title", Task: s.generateTitleTask})
		}
		if s.autoTagger != nil {
			chatJobs = append(chatJobs,
				jobs.CronJob{Name: "generate_auto_tags", Task: s.generateAutoTagsTask},
				jobs.CronJob{Name: "refresh_all_auto_tags", Task: s.refreshAllAutoTagsTask},
			)
		}
		if len(chatJobs) > 0 {
			if err := s.jobManager.RegisterAdHoc("_system", chatJobs); err != nil {
				log.Fatalf("Failed to register chat-model jobs: %v", err)
			}
		}
	}

	// Register media jobs (cron + ad-hoc). Must happen before Start().
	if s.mediaService != nil {
		s.mediaService.EnqueueFunc = s.jobManager.Enqueue
		if err := s.jobManager.UpsertDefinitions("_media", []jobs.CronJob{
			{Name: "repair_replicas", Schedule: "@every 30m", Task: s.mediaService.RepairSweepTask},
			{Name: "cleanup_pending_inline", Schedule: "@every 1h", Task: s.mediaService.PendingInlineCleanupTask},
		}); err != nil {
			log.Fatalf("Failed to register media cron jobs: %v", err)
		}
		if err := s.jobManager.RegisterAdHoc("_media", []jobs.CronJob{
			{Name: "repair_file_replica", Task: s.mediaService.RepairReplicaTask},
			{Name: "delete_file_replica", Task: s.mediaService.DeleteReplicaTask},
			{Name: "ocr_file", Task: s.ocrFileTask},
			{Name: "stt_file", Task: s.sttFileTask},
		}); err != nil {
			log.Fatalf("Failed to register media ad-hoc jobs: %v", err)
		}
		log.Printf("media: jobs registered")
	}

	// Register encrypted backup and retention purge cron jobs.
	if s.backupService != nil {
		if err := s.jobManager.UpsertDefinitions("_backup", []jobs.CronJob{
			{Name: "encrypted_backup", Schedule: "@every 12h", Task: s.backupTask},
			{Name: "retention_purge", Schedule: "@every 24h", Task: s.purgeTask},
		}); err != nil {
			log.Fatalf("Failed to register backup job: %v", err)
		}
		log.Printf("backup: jobs registered (encrypted_backup: @every 12h, retention_purge: @every 24h)")
	}

	// Start workers after all task registrations are complete.
	if err := s.jobManager.Start(); err != nil {
		log.Fatalf("Failed to start job manager: %v", err)
	}

	// Clean up any expired chunked upload sessions from a previous run.
	s.CleanupExpiredUploadSessions()

	mux := http.NewServeMux()
	protected := func(h http.HandlerFunc) http.Handler {
		return s.requireAuth(h)
	}

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		s.handleLogin(w, r)
	})
	mux.HandleFunc("/logout", s.handleLogout)
	mux.Handle("/session", protected(s.handleSession))
	mux.HandleFunc("/health", s.handleHealth)
	mux.Handle("/ws", protected(s.handleWebSocket))
	mux.Handle("/notes", protected(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.listNotes(w, r)
		case http.MethodPost:
			s.createNote(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	mux.Handle("/note-types", protected(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.handleNoteTypes(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	mux.Handle("/notes/search", protected(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.searchNotes(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	mux.Handle("/notes/", protected(func(w http.ResponseWriter, r *http.Request) {
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
		// POST /notes/:id/actions/:actionID (must be checked before /action)
		if idx := strings.LastIndex(r.URL.Path, "/actions/"); idx >= 0 {
			s.handlePluginActionV2(w, r)
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
		if strings.HasSuffix(r.URL.Path, "/auto-tags") {
			s.handleAutoTags(w, r)
			return
		}
		if strings.Contains(r.URL.Path, "/chunked/") {
			s.handleChunkedRoute(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/files/inline") {
			s.uploadInlineFile(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/files") {
			s.uploadAttachment(w, r)
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
	}))

	// WebAuthn routes
	mux.Handle("/webauthn/register/begin", protected(s.handleWebAuthnRegisterBegin))
	mux.Handle("/webauthn/register/finish", protected(s.handleWebAuthnRegisterFinish))
	mux.HandleFunc("/webauthn/login/begin", s.handleWebAuthnLoginBegin)
	mux.HandleFunc("/webauthn/login/finish", s.handleWebAuthnLoginFinish)

	// Tag autocomplete
	mux.Handle("/tags", protected(s.handleTags))

	// Job routes
	mux.Handle("/jobs", protected(s.handleJobs))
	mux.Handle("/jobs/", protected(s.handleJobByID))

	// On-demand backup trigger
	mux.Handle("/backup/trigger", protected(s.handleBackupTrigger))
	mux.Handle("/backup/purge", protected(s.handleBackupPurge))

	// Maintenance reindex endpoints
	mux.Handle("/maintenance/reindex", protected(s.handleReindexNotes))
	mux.Handle("/maintenance/reindex-ocr", protected(s.handleReindexOCR))
	mux.Handle("/maintenance/reindex-ocr-all", protected(s.handleReindexAllOCR))
	mux.Handle("/maintenance/reindex-stt", protected(s.handleReindexSTT))
	mux.Handle("/maintenance/reindex-stt-all", protected(s.handleReindexAllSTT))
	mux.Handle("/maintenance/refresh-auto-tags", protected(s.handleRefreshAllAutoTags))
	mux.Handle("/maintenance/recalculate-recipe-categories", protected(s.handleRecalculateRecipeIngredientCategories))
	mux.Handle("/maintenance/delete-unknown-s3-files", protected(s.handleDeleteUnknownS3Files))
	mux.Handle("/maintenance/delete-search-leftovers", protected(s.handleDeleteSearchLeftovers))

	// System status endpoints
	mux.Handle("/system/printer-status", protected(s.handlePrinterStatus))
	mux.Handle("/system/ai-status", protected(s.handleAIStatus))
	mux.Handle("/system/stats", protected(s.handleServerStats))

	mux.Handle("/file/", protected(s.serveFile))

	// Files routes: OCR and STT results
	mux.Handle("/files/", protected(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/stt") {
			if r.Method == http.MethodPost {
				s.handleTriggerSTT(w, r)
				return
			}
			s.handleFileSTT(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/ocr") {
			if r.Method == http.MethodPost {
				s.handleTriggerOCR(w, r)
				return
			}
			s.handleFileOCR(w, r)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))

	mux.Handle("/", newSPAHandler("./FrontEndDist"))

	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.withSecurityHeaders(s.requireTrustedRequest(mux)),
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
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

	listenURL := listenerURL(s.addr, s.cfg.TLSEnabled())
	if s.cfg.TLSEnabled() {
		log.Printf("listening on %s using TLS cert=%s key=%s", listenURL, s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
		if err := srv.ListenAndServeTLS(s.cfg.TLSCertFile, s.cfg.TLSKeyFile); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}

	log.Printf("listening on %s", listenURL)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func listenerURL(addr string, tlsEnabled bool) string {
	scheme := "http"
	if tlsEnabled {
		scheme = "https"
	}
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return scheme + "://localhost"
	}
	if strings.HasPrefix(addr, ":") {
		return scheme + "://localhost" + addr
	}
	return scheme + "://" + addr
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

// --- Backup Handler ---

func (s *Server) handleBackupTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.backupService == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Backups are not enabled. Set BACKUP_ENCRYPTION_KEY, MEDIA_CACHE_DIR, and MEDIA_S3_ENDPOINTS."})
		return
	}
	// Enqueue a one-shot backup via the job system so it shows up in /jobs.
	runID, err := s.jobManager.Enqueue("_backup", "encrypted_backup", nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to enqueue backup: %v", err)})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "queued",
		"run_id":  runID,
		"message": "Backup job enqueued. Check /jobs for progress.",
	})
}

func (s *Server) handleBackupPurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.backupService == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Backups are not enabled. Set BACKUP_ENCRYPTION_KEY, MEDIA_CACHE_DIR, and MEDIA_S3_ENDPOINTS."})
		return
	}
	runID, err := s.jobManager.Enqueue("_backup", "retention_purge", nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to enqueue purge: %v", err)})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "queued",
		"run_id":  runID,
		"message": "Retention purge enqueued. Check /jobs for progress.",
	})
}

// --- Maintenance Reindex Handlers ---

func (s *Server) handleRecalculateRecipeIngredientCategories(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.llm == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Embedding client not available"})
		return
	}

	runID, err := s.jobManager.Enqueue("_system", "recalculate_recipe_ingredient_categories", nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to enqueue ingredient category recalculation: %v", err)})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "queued",
		"run_id":  runID,
		"message": "Ingredient category recalculation enqueued. Check /jobs for progress.",
	})
}

// handleDeleteSearchLeftovers removes orphaned rows from all vector and chunk
// tables: rows that reference deleted notes or files. Returns the count of
// deleted rows per table.
func (s *Server) handleDeleteSearchLeftovers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.db.VSSAvailable() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "VSS not available"})
		return
	}

	result := map[string]int64{}

	// Delete vss_note_search vectors whose chunk no longer exists.
	if n, err := s.db.Exec(`DELETE FROM vss_note_search WHERE rowid NOT IN (SELECT id FROM note_search_chunks)`); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("vss_note_search: %v", err)})
		return
	} else {
		result["vss_note_search_orphaned"], _ = n.RowsAffected()
	}

	// Delete note_search_chunks whose note no longer exists.
	if n, err := s.db.Exec(`DELETE FROM note_search_chunks WHERE note_id NOT IN (SELECT id FROM notes)`); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("note_search_chunks: %v", err)})
		return
	} else {
		result["note_search_chunks_orphaned"], _ = n.RowsAffected()
	}

	// Delete legacy vss_notes vectors whose note no longer exists.
	if n, err := s.db.Exec(`DELETE FROM vss_notes WHERE rowid NOT IN (SELECT id FROM notes)`); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("vss_notes: %v", err)})
		return
	} else {
		result["vss_notes_orphaned"], _ = n.RowsAffected()
	}

	// Delete vss_files_ocr vectors whose file is deleted or doesn't exist.
	if n, err := s.db.Exec(`DELETE FROM vss_files_ocr WHERE rowid NOT IN (SELECT id FROM files WHERE deleted_at IS NULL)`); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("vss_files_ocr: %v", err)})
		return
	} else {
		result["vss_files_ocr_orphaned"], _ = n.RowsAffected()
	}

	// Delete vss_files_stt vectors whose file is deleted or doesn't exist.
	if n, err := s.db.Exec(`DELETE FROM vss_files_stt WHERE rowid NOT IN (SELECT id FROM files WHERE deleted_at IS NULL)`); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("vss_files_stt: %v", err)})
		return
	} else {
		result["vss_files_stt_orphaned"], _ = n.RowsAffected()
	}

	writeJSON(w, http.StatusOK, result)
}

// handleReindexNotes enqueues vss_index jobs for all notes that are missing
// chunked search embeddings. Returns the count of enqueued jobs.
func (s *Server) handleReindexNotes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.db.VSSAvailable() || s.llm == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "VSS or embedding client not available"})
		return
	}

	rows, err := s.db.Query(`
		SELECT n.id
		FROM notes n
		WHERE NOT EXISTS (
			SELECT 1 FROM note_search_chunks c WHERE c.note_id = n.id
		)
		ORDER BY n.id ASC
	`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("query notes: %v", err)})
		return
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			log.Printf("reindex: scan note: %v", err)
			continue
		}
		payload, _ := json.Marshal(map[string]interface{}{
			"note_id": id,
		})
		if _, err := s.jobManager.Enqueue("_system", "vss_index", payload); err != nil {
			log.Printf("reindex: enqueue vss_index for note %d: %v", id, err)
			continue
		}
		count++
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "queued",
		"count":   count,
		"message": fmt.Sprintf("Enqueued %d reindex jobs. Check /jobs for progress.", count),
	})
}

// handleReindexOCR enqueues ocr_file jobs for image files that are missing OCR
// text OR have OCR text but are missing embeddings in vss_files_ocr.
func (s *Server) handleReindexOCR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.mediaService == nil || s.ocrClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "OCR service not configured"})
		return
	}

	rows, err := s.db.Query(`
		SELECT f.id, COALESCE(focr.ocr_text, '')
		FROM files f
		LEFT JOIN files_ocr focr ON focr.file_id = f.id
		LEFT JOIN vss_files_ocr v ON v.rowid = f.id
		WHERE f.deleted_at IS NULL
		  AND f.mime_type IN ('image/jpeg','image/png','image/gif','image/webp','image/bmp','image/tiff','image/svg+xml')
		  AND (
		    focr.file_id IS NULL
		    OR focr.error IS NOT NULL
		    OR (focr.ocr_text != '' AND v.rowid IS NULL)
		  )
		ORDER BY f.id ASC
	`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("query files: %v", err)})
		return
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var fileID int64
		var ocrText string
		if err := rows.Scan(&fileID, &ocrText); err != nil {
			log.Printf("reindex-ocr: scan file: %v", err)
			continue
		}
		// If we already have OCR text but no embedding, just enqueue the embedding.
		// Otherwise, enqueue a full ocr_file job (which chains embedding on success).
		if ocrText != "" {
			payload, _ := json.Marshal(map[string]interface{}{
				"file_id":  fileID,
				"ocr_text": ocrText,
			})
			if _, err := s.jobManager.Enqueue("_system", "sync_ocr_embedding", payload); err != nil {
				log.Printf("reindex-ocr: enqueue sync_ocr_embedding for file %d: %v", fileID, err)
				continue
			}
		} else {
			s.enqueueOCR(fileID)
		}
		count++
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "queued",
		"count":   count,
		"message": fmt.Sprintf("Enqueued %d OCR reindex jobs. Check /jobs for progress.", count),
	})
}

// handleReindexSTT enqueues stt_file jobs for audio files that are missing STT
// text OR have STT text but are missing embeddings in vss_files_stt.
func (s *Server) handleReindexSTT(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.mediaService == nil || s.sttClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "STT service not configured"})
		return
	}

	rows, err := s.db.Query(`
		SELECT f.id, COALESCE(fstt.stt_text, '')
		FROM files f
		LEFT JOIN files_stt fstt ON fstt.file_id = f.id
		LEFT JOIN vss_files_stt v ON v.rowid = f.id
		WHERE f.deleted_at IS NULL
		  AND f.mime_type IN ('audio/mpeg','audio/mp3','audio/wav','audio/ogg','audio/flac','audio/aac','audio/wma','audio/m4a')
		  AND (
		    fstt.file_id IS NULL
		    OR fstt.error IS NOT NULL
		    OR (fstt.stt_text != '' AND v.rowid IS NULL)
		  )
		ORDER BY f.id ASC
	`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("query files: %v", err)})
		return
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var fileID int64
		var sttText string
		if err := rows.Scan(&fileID, &sttText); err != nil {
			log.Printf("reindex-stt: scan file: %v", err)
			continue
		}
		// If we already have STT text but no embedding, just enqueue the embedding.
		// Otherwise, enqueue a full stt_file job (which chains embedding on success).
		if sttText != "" {
			payload, _ := json.Marshal(map[string]interface{}{
				"file_id":  fileID,
				"stt_text": sttText,
			})
			if _, err := s.jobManager.Enqueue("_system", "sync_stt_embedding", payload); err != nil {
				log.Printf("reindex-stt: enqueue sync_stt_embedding for file %d: %v", fileID, err)
				continue
			}
		} else {
			s.enqueueSTT(fileID)
		}
		count++
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "queued",
		"count":   count,
		"message": fmt.Sprintf("Enqueued %d STT reindex jobs. Check /jobs for progress.", count),
	})
}

// handleReindexAllOCR enqueues ocr_file jobs for ALL image files, regardless of
// whether they have already been OCR'd or have embeddings.
func (s *Server) handleReindexAllOCR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.mediaService == nil || s.ocrClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "OCR service not configured"})
		return
	}

	rows, err := s.db.Query(`
		SELECT f.id
		FROM files f
		WHERE f.deleted_at IS NULL
		  AND f.mime_type IN ('image/jpeg','image/png','image/gif','image/webp','image/bmp','image/tiff','image/svg+xml')
		ORDER BY f.id ASC
	`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("query files: %v", err)})
		return
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var fileID int64
		if err := rows.Scan(&fileID); err != nil {
			log.Printf("reindex-all-ocr: scan file: %v", err)
			continue
		}
		s.enqueueOCR(fileID)
		count++
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "queued",
		"count":   count,
		"message": fmt.Sprintf("Enqueued %d OCR reindex-all jobs. Check /jobs for progress.", count),
	})
}

// handleReindexAllSTT enqueues stt_file jobs for ALL audio files, regardless of
// whether they have already been STT'd or have embeddings.
func (s *Server) handleReindexAllSTT(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.mediaService == nil || s.sttClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "STT service not configured"})
		return
	}

	rows, err := s.db.Query(`
		SELECT f.id
		FROM files f
		WHERE f.deleted_at IS NULL
		  AND f.mime_type IN ('audio/mpeg','audio/mp3','audio/wav','audio/ogg','audio/flac','audio/aac','audio/wma','audio/m4a')
		ORDER BY f.id ASC
	`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("query files: %v", err)})
		return
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var fileID int64
		if err := rows.Scan(&fileID); err != nil {
			log.Printf("reindex-all-stt: scan file: %v", err)
			continue
		}
		s.enqueueSTT(fileID)
		count++
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "queued",
		"count":   count,
		"message": fmt.Sprintf("Enqueued %d STT reindex-all jobs. Check /jobs for progress.", count),
	})
}

func (s *Server) handleRefreshAllAutoTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.autoTagger == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Chat model not available for auto-tag refresh"})
		return
	}

	runID, err := s.jobManager.Enqueue("_system", "refresh_all_auto_tags", nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to enqueue auto-tag refresh: %v", err)})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "queued",
		"run_id":  runID,
		"message": "Auto-tag refresh enqueued. Check /jobs for progress.",
	})
}

// --- Helpers ---

// --- System Status Handlers ---

func (s *Server) handlePrinterStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Use the same printer.FindPrinter() that actual printing uses.
	// This covers THERMAL_PRINTER_DEVICE, /dev/usb/lp*, and THERMAL_PRINTER_USB_ID.
	prDev, err := printer.FindPrinter()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"connected":   false,
			"device_path": "",
			"error":       err.Error(),
			"code_page":   printer.ConfiguredCodePageName(),
			"checked":     []string{"THERMAL_PRINTER_DEVICE", "/dev/usb/lp0-lp2", "THERMAL_PRINTER_USB_ID", "THERMAL_PRINTER_CODEPAGE"},
		})
		return
	}

	// Close immediately - we only needed to verify connectivity.
	prDev.Close()

	writeJSON(w, http.StatusOK, map[string]any{
		"connected":   true,
		"device_path": "", // raw USB devices don not have a simple path
		"error":       "",
		"method":      "Printer detected via FindPrinter()",
		"code_page":   printer.ConfiguredCodePageName(),
	})
}

func (s *Server) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	baseURL := os.Getenv("LOCALAI_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}

	embeddingModel := os.Getenv("LOCALAI_EMBEDDING_MODEL")
	if embeddingModel == "" {
		embeddingModel = "text-embedding-ada-002"
	}

	chatModel := os.Getenv("LOCALAI_CHAT_MODEL")
	if chatModel == "" {
		chatModel = "gpt-3.5-turbo"
	}

	ocrModel := os.Getenv("LOCALAI_OCR_MODEL")
	if ocrModel == "" {
		ocrModel = "gpt-4o-mini"
	}

	sttModel := os.Getenv("LOCALAI_STT_MODEL")
	if sttModel == "" {
		sttModel = "nemo-parakeet-tdt-0.6b"
	}

	// Test embedding connectivity
	embeddingOK := false
	embeddingErr := ""
	if s.llm != nil {
		release := llm.BeginBackendUse(s.llm)
		_, err := s.llm.GenerateEmbedding("test")
		release()
		if err != nil {
			embeddingErr = err.Error()
		} else {
			embeddingOK = true
		}
	} else {
		embeddingErr = "Embedding client not configured"
	}

	// Test chat connectivity
	chatOK := false
	chatErr := ""
	if s.chatClient != nil {
		release := llm.BeginBackendUse(s.chatClient)
		_, err := s.chatClient.GenerateTitle("test")
		release()
		if err != nil {
			chatErr = err.Error()
		} else {
			chatOK = true
		}
	} else {
		chatErr = "Chat client not configured"
	}

	// OCR and STT: verify client exists (actual test requires real media files)
	ocrOK := s.ocrClient != nil
	ocrErr := ""
	if !ocrOK {
		ocrErr = "OCR client not configured"
	}

	sttOK := s.sttClient != nil
	sttErr := ""
	if !sttOK {
		sttErr = "STT client not configured"
	}

	vssAvailable := s.db.VSSAvailable()

	// Detailed VSS diagnostics
	vssNotesCount := int64(-1) // -1 = not checked
	vssOCRFilesCount := int64(-1)
	vssSTTFilesCount := int64(-1)
	vssError := ""
	vssTablesExist := false

	if vssAvailable {
		var count int64
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM vss_note_search`).Scan(&count); err != nil {
			vssError = fmt.Sprintf("vss_note_search query error: %v", err)
		} else {
			vssTablesExist = true
			vssNotesCount = count
		}

		if err := s.db.QueryRow(`SELECT COUNT(*) FROM vss_files_ocr`).Scan(&count); err == nil {
			vssOCRFilesCount = count
		}

		if err := s.db.QueryRow(`SELECT COUNT(*) FROM vss_files_stt`).Scan(&count); err == nil {
			vssSTTFilesCount = count
		}

		if vssError == "" && !vssTablesExist {
			vssError = "VSS extension loaded but no vss tables exist (migration may have failed)"
		}
	} else {
		vssError = "sqlite-vec extension (vec0) not loaded — vector search is unavailable"
	}

	allOK := embeddingOK && chatOK && ocrOK && sttOK

	writeJSON(w, http.StatusOK, map[string]any{
		"base_url":      baseURL,
		"all_ok":        allOK,
		"vss_available": vssAvailable,
		"vss": map[string]any{
			"available":       vssAvailable,
			"error":           vssError,
			"notes_count":     vssNotesCount,
			"ocr_files_count": vssOCRFilesCount,
			"stt_files_count": vssSTTFilesCount,
		},
		"embedding": map[string]any{
			"model": embeddingModel,
			"ok":    embeddingOK,
			"error": embeddingErr,
		},
		"chat": map[string]any{
			"model": chatModel,
			"ok":    chatOK,
			"error": chatErr,
		},
		"ocr": map[string]any{
			"model": ocrModel,
			"ok":    ocrOK,
			"error": ocrErr,
		},
		"stt": map[string]any{
			"model": sttModel,
			"ok":    sttOK,
			"error": sttErr,
		},
	})
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

// handleDeleteUnknownS3Files lists all objects under files/ on each configured
// S3 endpoint, compares them against the active storage_key values in the DB,
// and deletes any objects that are no longer referenced.
func (s *Server) handleDeleteUnknownS3Files(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.mediaService == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Media not enabled. Set MEDIA_CACHE_DIR and MEDIA_S3_ENDPOINTS."})
		return
	}

	result, err := s.mediaService.DeleteUnknownS3Files(context.Background())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleServerStats returns aggregate server statistics: embedding counts,
// total notes, database file size, media storage usage, and backup info.
func (s *Server) handleServerStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := map[string]any{}

	// Embedding counts.
	vssNotesCount := int64(-1)
	vssOCRCount := int64(-1)
	vssSTTCount := int64(-1)
	if s.db.VSSAvailable() {
		var count int64
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM vss_note_search`).Scan(&count); err == nil {
			vssNotesCount = count
		}
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM vss_files_ocr`).Scan(&count); err == nil {
			vssOCRCount = count
		}
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM vss_files_stt`).Scan(&count); err == nil {
			vssSTTCount = count
		}
	}
	resp["vss_notes_count"] = vssNotesCount
	resp["vss_ocr_count"] = vssOCRCount
	resp["vss_stt_count"] = vssSTTCount
	resp["vss_available"] = s.db.VSSAvailable()

	// Total notes.
	var totalNotes int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&totalNotes); err != nil {
		resp["total_notes"] = int64(-1)
	} else {
		resp["total_notes"] = totalNotes
	}

	// Database file size.
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "mentis.db"
	}
	dbSize := int64(-1)
	if fi, err := os.Stat(dbPath); err == nil {
		dbSize = fi.Size()
	}
	resp["db_size_bytes"] = dbSize

	// Media space usage: sum of size_bytes for non-deleted files.
	mediaSize := int64(-1)
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(size_bytes), 0) FROM files WHERE deleted_at IS NULL`).Scan(&mediaSize); err != nil {
		mediaSize = -1
	}
	resp["media_size_bytes"] = mediaSize

	// Backup stats.
	if s.backupService != nil {
		stats, err := s.backupService.Stats(context.Background())
		if err != nil {
			resp["backup_count"] = int64(-1)
			resp["backup_size_bytes"] = int64(-1)
			resp["backup_error"] = err.Error()
		} else {
			resp["backup_count"] = stats.Count
			resp["backup_size_bytes"] = stats.TotalSizeBytes
		}
	} else {
		resp["backup_count"] = int64(-1)
		resp["backup_size_bytes"] = int64(-1)
		resp["backup_error"] = "Backups not enabled"
	}

	writeJSON(w, http.StatusOK, resp)
}
