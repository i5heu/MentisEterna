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
	llm           llm.Embedder
	chatClient    llm.Generator
	ocrClient     llm.OCRer
	sttClient     llm.STTer
	webauthn      *webauthn.WebAuthn
	sessionStore  *webAuthnSessionStore
	jobManager    *jobs.Manager
	mediaService  *media.Service
	backupService *backup.Service
}

func New(d *db.DB, addr string, embeddingClient llm.Embedder, chatClient llm.Generator, ocrClient llm.OCRer, sttClient llm.STTer) *Server {
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

	return &Server{
		db:            d,
		addr:          addr,
		llm:           embeddingClient,
		chatClient:    chatClient,
		ocrClient:     ocrClient,
		sttClient:     sttClient,
		webauthn:      w,
		sessionStore:  newWebAuthnSessionStore(),
		jobManager:    jobMgr,
		mediaService:  mediaSvc,
		backupService: backupSvc,
	}
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

	// Register ad-hoc VSS embedding index job (on-demand, not cron).
	// Must happen before Start() so workers see the task.
	if s.db.VSSAvailable() && s.llm != nil {
		if err := s.jobManager.RegisterAdHoc("_system", []jobs.CronJob{
			{Name: "vss_index", Task: s.syncEmbeddingTask},
			{Name: "sync_ocr_embedding", Task: s.syncOCREmbeddingTask},
			{Name: "sync_stt_embedding", Task: s.syncSTTEmbeddingTask},
		}); err != nil {
			log.Fatalf("Failed to register VSS index job: %v", err)
		}
	}

	// Register ad-hoc title generation job (for notes saved without a title).
	if s.chatClient != nil {
		if err := s.jobManager.RegisterAdHoc("_system", []jobs.CronJob{{
			Name: "generate_title",
			Task: s.generateTitleTask,
		}}); err != nil {
			log.Fatalf("Failed to register title generation job: %v", err)
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
	mux.HandleFunc("/note-types", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.handleNoteTypes(w, r)
		} else {
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

	// Tag autocomplete
	mux.HandleFunc("/tags", s.handleTags)

	// Job routes
	mux.HandleFunc("/jobs", s.handleJobs)
	mux.HandleFunc("/jobs/", s.handleJobByID)

	// On-demand backup trigger
	mux.HandleFunc("/backup/trigger", s.handleBackupTrigger)
	mux.HandleFunc("/backup/purge", s.handleBackupPurge)

	// Maintenance reindex endpoints
	mux.HandleFunc("/maintenance/reindex", s.handleReindexNotes)
	mux.HandleFunc("/maintenance/reindex-ocr", s.handleReindexOCR)
	mux.HandleFunc("/maintenance/reindex-stt", s.handleReindexSTT)

	// System status endpoints
	mux.HandleFunc("/system/printer-status", s.handlePrinterStatus)
	mux.HandleFunc("/system/ai-status", s.handleAIStatus)

	mux.HandleFunc("/file/", s.serveFile)

	// Files routes: OCR and STT results
	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/stt") {
			s.handleFileSTT(w, r)
			return
		}
		s.handleFileOCR(w, r)
	})

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

// handleReindexNotes enqueues vss_index jobs for all notes that are missing
// embeddings in vss_notes. Returns the count of enqueued jobs.
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
		SELECT n.id, n.title, COALESCE(u.body, '') AS body
		FROM notes n
		LEFT JOIN updates u ON u.id = (
			SELECT id FROM updates WHERE note_id = n.id ORDER BY id DESC LIMIT 1
		)
		WHERE n.id NOT IN (SELECT rowid FROM vss_notes)
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
		var title, body string
		if err := rows.Scan(&id, &title, &body); err != nil {
			log.Printf("reindex: scan note: %v", err)
			continue
		}
		payload, _ := json.Marshal(map[string]interface{}{
			"note_id": id,
			"title":   title,
			"body":    body,
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

// handleReindexOCR enqueues sync_ocr_embedding jobs for all files that have OCR
// text but are missing embeddings in vss_files_ocr.
func (s *Server) handleReindexOCR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.db.VSSAvailable() || s.llm == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "VSS or embedding client not available"})
		return
	}

	rows, err := s.db.Query(`
		SELECT focr.file_id, focr.ocr_text
		FROM files_ocr focr
		WHERE focr.ocr_text != ''
		  AND focr.file_id NOT IN (SELECT rowid FROM vss_files_ocr)
		ORDER BY focr.file_id ASC
	`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("query files_ocr: %v", err)})
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
		payload, _ := json.Marshal(map[string]interface{}{
			"file_id":  fileID,
			"ocr_text": ocrText,
		})
		if _, err := s.jobManager.Enqueue("_system", "sync_ocr_embedding", payload); err != nil {
			log.Printf("reindex-ocr: enqueue sync_ocr_embedding for file %d: %v", fileID, err)
			continue
		}
		count++
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "queued",
		"count":   count,
		"message": fmt.Sprintf("Enqueued %d OCR reindex jobs. Check /jobs for progress.", count),
	})
}

// handleReindexSTT enqueues sync_stt_embedding jobs for all files that have STT
// text but are missing embeddings in vss_files_stt.
func (s *Server) handleReindexSTT(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.db.VSSAvailable() || s.llm == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "VSS or embedding client not available"})
		return
	}

	rows, err := s.db.Query(`
		SELECT fstt.file_id, fstt.stt_text
		FROM files_stt fstt
		WHERE fstt.stt_text != ''
		  AND fstt.file_id NOT IN (SELECT rowid FROM vss_files_stt)
		ORDER BY fstt.file_id ASC
	`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("query files_stt: %v", err)})
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
		payload, _ := json.Marshal(map[string]interface{}{
			"file_id":  fileID,
			"stt_text": sttText,
		})
		if _, err := s.jobManager.Enqueue("_system", "sync_stt_embedding", payload); err != nil {
			log.Printf("reindex-stt: enqueue sync_stt_embedding for file %d: %v", fileID, err)
			continue
		}
		count++
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "queued",
		"count":   count,
		"message": fmt.Sprintf("Enqueued %d STT reindex jobs. Check /jobs for progress.", count),
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
		_, err := s.llm.GenerateEmbedding("test")
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
		_, err := s.chatClient.GenerateTitle("test")
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
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM vss_notes`).Scan(&count); err != nil {
			vssError = fmt.Sprintf("vss_notes query error: %v", err)
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
