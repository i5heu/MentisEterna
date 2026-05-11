package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/pkg/notetype"
)

type Server struct {
	db           *db.DB
	addr         string
	llm          llm.Embedder
	webauthn     *webauthn.WebAuthn
	sessionStore *webAuthnSessionStore
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
	return &Server{
		db:           d,
		addr:         addr,
		llm:          embeddingClient,
		webauthn:     w,
		sessionStore: newWebAuthnSessionStore(),
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

	// Start centralized cron scheduler for plugin background jobs.
	s.startCronJobs()

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
	}()

	log.Printf("listening on http://localhost%s", s.addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// startCronJobs iterates over all registered plugins and schedules their
// CronJobs using a simple goroutine-based scheduler. For production use,
// consider github.com/robfig/cron/v3, but a lightweight approach keeps
// dependencies minimal.
func (s *Server) startCronJobs() {
	for _, plugin := range notetype.Registry {
		for _, job := range plugin.CronJobs() {
			scheduleCron(job.Schedule, func() {
				if err := job.Task(s.db.DB); err != nil {
					log.Printf("cron [%s]: %v", plugin.ID(), err)
				}
			})
		}
	}
}

// scheduleCron runs a function on a simple interval-based schedule.
// Supported formats:
//   - "@every 1h", "@every 30m", "@every 24h" — Go duration after @every
//   - "@daily" — runs once per day at midnight
//   - "@hourly" — runs once per hour
//   - For full cron expressions, use robfig/cron (see AGENTS.md).
func scheduleCron(schedule string, fn func()) {
	d, ok := parseSimpleSchedule(schedule)
	if !ok {
		log.Printf("cron: unsupported schedule %q, skipping", schedule)
		return
	}
	go func() {
		// Sleep until the next aligned tick for @daily/@hourly, or just loop for @every.
		for {
			if strings.HasPrefix(schedule, "@every ") {
				time.Sleep(d)
			} else {
				now := time.Now()
				next := now.Truncate(d).Add(d)
				if next.Before(now) || next.Equal(now) {
					next = next.Add(d)
				}
				time.Sleep(next.Sub(now))
			}
			fn()
			if !strings.HasPrefix(schedule, "@every ") {
				// For @daily/@hourly, recalculate next tick after each run.
				time.Sleep(d)
			}
		}
	}()
}

func parseSimpleSchedule(schedule string) (time.Duration, bool) {
	switch {
	case strings.HasPrefix(schedule, "@every "):
		d, err := time.ParseDuration(strings.TrimPrefix(schedule, "@every "))
		if err != nil {
			return 0, false
		}
		return d, true
	case schedule == "@daily":
		return 24 * time.Hour, true
	case schedule == "@hourly":
		return time.Hour, true
	default:
		return 0, false
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}
