package server

import (
	"path/filepath"
	"testing"

	"github.com/i5heu/MentisEterna/internal/db"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d, ":0", nil, nil, nil)
}

func createTestSession(t *testing.T, s *Server) string {
	t.Helper()
	if err := s.db.SetAdminPassword("testpass"); err != nil {
		t.Fatalf("set admin password: %v", err)
	}
	token, _, err := s.db.CreateSession("admin")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return token
}
