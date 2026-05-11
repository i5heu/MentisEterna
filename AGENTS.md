# MentisEterna — Agent Guide

Personal note-taking app with vector search. Go backend + Vue 3 frontend, SQLite + VSS extensions, Ollama embeddings.

## Architecture

```
cmd/server/      — main binary (Go HTTP server)
cmd/backfill/    — one-shot tool: generate missing embeddings for existing notes
internal/db/     — SQLite wrapper, migrations, auth, session management
internal/llm/    — Ollama embedding client (interface: Embedder)
internal/server/ — HTTP handlers, WebAuthn, SPA static serving
frontend/        — Vue 3 + Vite app (dev proxy → :8080)
FrontEndDist/    — Vite build output (served by Go server at runtime)
lib/             — Pre-built SQLite extensions: vector0.so, vss0.so (do NOT regenerate)
```

Notes store content history in `updates` table (body was migrated out of `notes`). VSS table `vss_notes` holds 2560-dim embeddings indexed by `rowid = notes.id`.

## Commands

### Go backend
```bash
go build ./cmd/server/       # build server binary
go run ./cmd/server/         # run server (default: :8080, DB: mentis.db)
go run ./cmd/backfill/       # backfill embeddings for notes missing them
go test ./...                # run all tests
go test ./internal/db/       # test a specific package
go test ./internal/server/ -run TestNotesSearch  # run a single test
```

### Frontend
```bash
cd frontend
npm install
npm run dev      # dev server with proxy to :8080
npm run build    # outputs to ../FrontEndDist (what the Go server serves)
```

## Environment Variables

| Variable | Default | Purpose |
|---|---|---|
| `DB_PATH` | `mentis.db` | SQLite database path |
| `ADDR` | `:8080` | HTTP listen address |
| `OLLAMA_BASE_URL` | `http://localhost:11434` | Ollama instance URL |
| `OLLAMA_EMBEDDING_MODEL` | `hf.co/Qwen/Qwen3-Embedding-4B-GGUF:Q4_K_M` | Embedding model |
| `VSS_EXT_PATH` | auto-detected | Directory containing `vector0.so` and `vss0.so` |

## Key Quirks

**VSS extension loading**: `db.Open()` gracefully falls back to standard SQLite if `.so` files aren't loadable. Tests that require VSS auto-skip with `t.Skip(...)` when VSS is unavailable — this is intentional, not a bug.

**VSS upserts**: `vss0` does NOT support `UPDATE` or `INSERT OR REPLACE`. Always `DELETE` then `INSERT`:
```sql
DELETE FROM vss_notes WHERE rowid = ?;
INSERT INTO vss_notes(rowid, body_embedding) VALUES (?, ?);
```

**WebAuthn**: RPID is hardcoded to `localhost`, origins locked to `http://localhost:8080` and `https://localhost:8080`. Changing the host/port requires updating `internal/server/server.go`.

**Embedding dimension**: 2560 (Qwen3-Embedding-4B). The mock embedder in tests also uses 2560 — keep them in sync if you change models.

**Auth**: Password-based (SHA-512, stored in `auth` table) + WebAuthn passkeys. Sessions last 24 hours. `initAdminPassword()` runs at server startup.

**Static serving**: Go server serves `FrontEndDist/` as an SPA (falls back to `index.html` for unknown paths). Must `npm run build` to reflect frontend changes in production mode.

## Testing Conventions

- DB tests use `db.Open(t.TempDir()+"/test.db")` — real SQLite on temp files
- Server tests use `db.OpenInMemory()` with a `mockEmbedder` (deterministic, no Ollama needed)
- `newTestServer(t)` — basic server with no embedder (nil)
- `newTestServerWithEmbedder(t)` — server with mock embedder + VSS (skips if VSS unavailable)
- `createTestSession(t, s)` — sets admin password "testpass" and returns a session token

## Database Schema Notes

- `notes`: id, title, parent_id, created_at — **no body/updated_at columns** (migrated out)
- `updates`: id, note_id, body, created_at — append-only history
- `vss_notes`: rowid (= notes.id), body_embedding (JSON float array, 2560-dim)
- `auth`: id, username, password_hash (SHA-512 hex)
- `sessions`: token, username, expires_at
- DB opened with `_journal_mode=WAL&_foreign_keys=on`
