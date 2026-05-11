# Encrypted S3 Media Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add encrypted, redundant S3-backed note files to MentisEterna with append and drag/drop flows, authenticated `/file/:noteID/:fileID` serving, encrypted local cache, and job-driven replica repair/deletion.

**Architecture:** Keep HTTP orchestration in `internal/server`, add a focused `internal/media` package for encryption/S3/cache/reference parsing, extend SQLite schema in `internal/db`, and use the existing SQLite-backed job system for replica repair, delete propagation, and stale pending-inline cleanup. Track explicit attachment refs and inline markdown refs separately so file retention matches the approved lifecycle.

**Tech Stack:** Go HTTP server, SQLite, existing `internal/jobs` manager, Vue 3 + Vite frontend, AWS SDK for Go v2 against S3-compatible endpoints, AES-256 chunked AEAD object format, local encrypted disk cache.

---

## Locked Product Decisions

- Use `files` + `file_s3` + `files_refs`; per-endpoint lifecycle state lives in `file_s3.state`, so there is no separate `files_s3_state` table in the first implementation.
- Each file stores its own plaintext AES-256 key and per-file **base nonce** in `files`.
- Upload is **synchronous from the user’s perspective**.
- Each upload request performs a first-wave parallel PUT to every configured endpoint.
- A user-visible upload succeeds only if the DB transaction commits **and at least one endpoint** stores the encrypted object.
- If all endpoints fail on the first wave, the request fails and any partial remote/cache state from that attempt must be cleaned up.
- Failed endpoints are retried automatically until they become `uploaded`.
- `/file/:noteID/:fileID` must require normal authenticated requests.
- `noteID` in `/file/:noteID/:fileID` is cosmetic; auth + existing `fileID` control access.
- Drag/drop uploads are temporary pending files until the next save resolves markdown references.
- Each pending inline upload must record both the owning note and a timestamp so abandoned drops can be reaped later.
- Dropped files not referenced in the next saved body must be deleted.
- Images insert markdown image syntax; non-images insert normal markdown links.
- Append-file uploads create persistent attachments even when the note body contains no markdown link.
- Attachments render after the message body as a file list.
- Cross-note file references are a supported feature.
- Deleting a note must not delete a file if any other note still references or attaches it.
- Local cache must store **only encrypted bytes**.

## Important Design Adjustment

Do **not** implement single-shot whole-file AES-GCM. It is a poor fit for authenticated streaming because plaintext cannot be safely released until the full blob is verified.

Use a **chunked AES-256-GCM object format** instead:

- one per-file AES-256 key from `files.aes_key`
- one per-file base nonce from `files.aes_nonce`
- derive a unique nonce per chunk from `base_nonce + chunk_index`
- encrypt fixed-size chunks independently
- store one encrypted object per file per replica

This keeps the user’s “AES-256 with per-file key + nonce in DB” requirement intact while making cache-backed proxy reads and large-file handling practical.

## File Map

### Existing files to modify

- `internal/db/db.go`
  - extend migration with media tables and indexes
- `internal/server/server.go`
  - bootstrap media service, register media jobs, add routes
- `internal/server/auth.go`
  - treat `/file/` as an authenticated API path
- `internal/server/notes.go`
  - reconcile inline refs on note create/update; clean up orphaned files on note delete; enrich note payloads with attachments
- `internal/server/notes_test.go`
  - add save/delete behavior tests around inline refs and attachment retention
- `frontend/src/api.js`
  - add multipart upload helpers and file delete helpers
- `frontend/src/views/NotesView.vue`
  - add attach button, drag/drop handling, cursor insertion, attachment list rendering, upload-before-save for unsaved notes

### New backend files

- `internal/media/config.go`
  - parse media cache directory and S3 endpoint configuration from env
- `internal/media/types.go`
  - file metadata, attachment payloads, replica state enums, service interfaces
- `internal/media/crypto.go`
  - chunked AES-256-GCM encrypt/decrypt helpers
- `internal/media/cache.go`
  - encrypted local cache read/write/delete helpers
- `internal/media/refs.go`
  - extract `file_id`s from markdown body
- `internal/media/s3.go`
  - S3-compatible upload/download/delete client logic
- `internal/media/service.go`
  - orchestration for file create, replica sync, read-through cache, delete, retry payload building
- `internal/media/crypto_test.go`
  - encryption round-trip and tamper-detection tests
- `internal/media/refs_test.go`
  - markdown extraction tests
- `internal/media/service_test.go`
  - cache fallback / replica repair / lifecycle tests with fakes

### New server files

- `internal/server/files.go`
  - `POST /notes/:id/files`, `POST /notes/:id/files/inline`, `DELETE /notes/:id/files/:fileID`, `GET /file/:noteID/:fileID`
- `internal/server/files_test.go`
  - upload/proxy/auth/attachment endpoint tests

### New frontend files

- `frontend/src/components/NoteAttachments.vue`
  - render attachment list after the body, reuse for root note and child notes

## Configuration to Add

Add env-driven media config in `internal/media/config.go`:

```go
type EndpointConfig struct {
    ID               string
    Bucket           string
    Region           string
    Endpoint         string
    AccessKeyID      string
    SecretAccessKey  string
    ForcePathStyle   bool
}

type Config struct {
    CacheDir  string
    Endpoints []EndpointConfig
}
```

Use one JSON env var for endpoints so multiple S3-compatible backends are easy to define:

```text
MEDIA_S3_ENDPOINTS='[
  {"id":"primary","bucket":"mentis-media","region":"us-east-1","endpoint":"https://s3.example.com","access_key_id":"...","secret_access_key":"...","force_path_style":true},
  {"id":"backup","bucket":"mentis-media-b","region":"us-east-1","endpoint":"https://backup.example.com","access_key_id":"...","secret_access_key":"...","force_path_style":true}
]'
MEDIA_CACHE_DIR=media-cache
```

If `MEDIA_S3_ENDPOINTS` is empty, fail server startup clearly. This feature is storage-backed, not optional once implemented.

## Database Schema

Add these tables in `internal/db/db.go` after `migrateNotes()` creates `notes` / `updates`:

```sql
CREATE TABLE IF NOT EXISTS files (
    id                     INTEGER PRIMARY KEY AUTOINCREMENT,
    original_note_id       INTEGER REFERENCES notes(id) ON DELETE SET NULL,
    pending_inline_note_id INTEGER REFERENCES notes(id) ON DELETE CASCADE,
    pending_inline_at      DATETIME,
    storage_key            TEXT    NOT NULL UNIQUE,
    filename               TEXT    NOT NULL,
    mime_type              TEXT    NOT NULL,
    size_bytes             INTEGER NOT NULL,
    plaintext_sha256       TEXT,
    ciphertext_sha256      TEXT    NOT NULL,
    aes_key                BLOB    NOT NULL,
    aes_nonce              BLOB    NOT NULL,
    created_at             DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    deleted_at             DATETIME
);

CREATE TABLE IF NOT EXISTS file_s3 (
    file_id           INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    endpoint_id       TEXT    NOT NULL,
    state             TEXT    NOT NULL,
    remote_key        TEXT    NOT NULL,
    etag              TEXT,
    ciphertext_size   INTEGER,
    last_error        TEXT,
    retry_count       INTEGER NOT NULL DEFAULT 0,
    last_attempt_at   DATETIME,
    last_success_at   DATETIME,
    next_retry_at     DATETIME,
    updated_at        DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (file_id, endpoint_id)
);

CREATE TABLE IF NOT EXISTS files_refs (
    note_id      INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    file_id      INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    ref_kind     TEXT    NOT NULL,
    created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (note_id, file_id, ref_kind)
);

CREATE INDEX IF NOT EXISTS idx_files_pending_inline_note_id ON files(pending_inline_note_id);
CREATE INDEX IF NOT EXISTS idx_files_pending_inline_at ON files(pending_inline_at);
CREATE INDEX IF NOT EXISTS idx_files_deleted_at ON files(deleted_at);
CREATE INDEX IF NOT EXISTS idx_file_s3_next_retry ON file_s3(state, next_retry_at);
CREATE INDEX IF NOT EXISTS idx_files_refs_note_id ON files_refs(note_id);
CREATE INDEX IF NOT EXISTS idx_files_refs_file_id ON files_refs(file_id);
```

`pending_inline_note_id` ties a pending drop to the note that owns the next-save reconciliation step, while `pending_inline_at` gives the janitor job a time-based safety net when that save never happens.

## API Shape

Use these endpoints:

- `POST /notes/:id/files`
  - multipart upload for persistent attachment
- `POST /notes/:id/files/inline`
  - multipart upload for pending-inline file
- `DELETE /notes/:id/files/:fileID`
  - remove this note’s `attachment` ref; delete file only if no refs remain
- `GET /file/:noteID/:fileID`
  - authenticated proxy/cache/decrypt serve path

Do **not** add a separate `GET /notes/:id/files` route. Instead, enrich note responses with attachments so the existing selected-note and children-fetch paths can render attachments without extra round-trips.

## Note JSON Shape Change

Extend `internal/server/notes.go`:

```go
type NoteFile struct {
    ID        int64  `json:"id"`
    Filename  string `json:"filename"`
    MimeType  string `json:"mime_type"`
    SizeBytes int64  `json:"size_bytes"`
    URL       string `json:"url"`
    IsImage   bool   `json:"is_image"`
}
```

and add:

```go
Attachments []NoteFile `json:"attachments,omitempty"`
```

Populate `Attachments` for:

- `getNote`
- `createNote` response
- `updateNote` response
- `getNoteChildren`

Sidebar list/search endpoints do not need attachments.

---

## Task 1: Add schema, note attachment shape, and media config

**Files:**
- Modify: `internal/db/db.go`
- Modify: `internal/server/notes.go`
- Create: `internal/media/config.go`
- Test: `internal/db/db_test.go`
- Test: `internal/server/notes_test.go`

- [ ] **Step 1: Write failing migration tests for the new tables and foreign-key behavior**

Add assertions in `internal/db/db_test.go` for:

```go
func TestMediaTablesExist(t *testing.T) {
    d, err := OpenInMemory()
    if err != nil { t.Fatal(err) }
    defer d.Close()

    for _, table := range []string{"files", "file_s3", "files_refs"} {
        var name string
        err := d.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
        if err != nil { t.Fatalf("missing table %s: %v", table, err) }
    }
}

func TestFileRefsCascadeOnNoteDelete(t *testing.T) { /* create note, file, ref; delete note; ref count becomes 0 */ }
func TestFilesOriginalNoteUsesSetNull(t *testing.T) { /* delete original note; file row survives with original_note_id NULL */ }
```

- [ ] **Step 2: Implement the migration and indexes in `internal/db/db.go`**

Create an `ensureMediaTables()` function and call it from `migrate()` after `migrateNotes()` has established the `notes` table.

- [ ] **Step 3: Add media config parsing**

Create `internal/media/config.go` with:

```go
func LoadConfigFromEnv() (Config, error)
```

Validation rules:

- `MEDIA_CACHE_DIR` must not be empty
- `MEDIA_S3_ENDPOINTS` must decode to at least one endpoint
- each endpoint needs a unique `ID`
- each endpoint must provide `Bucket`, `Endpoint`, `AccessKeyID`, and `SecretAccessKey`

- [ ] **Step 4: Extend note payloads to carry attachments**

Add `NoteFile` and `Attachments` to `internal/server/notes.go`, plus a helper:

```go
func (s *Server) loadNoteAttachments(noteID int64) ([]NoteFile, error)
```

It should query only `files_refs.ref_kind = 'attachment'` and emit URLs as `/file/<noteID>/<fileID>`.

- [ ] **Step 5: Run the focused backend tests**

Run:

```bash
go test ./internal/db ./internal/server -run 'TestMediaTablesExist|TestFileRefsCascadeOnNoteDelete|TestFilesOriginalNoteUsesSetNull' -v
```

Expected: new tests pass; unrelated VSS-dependent tests may still skip.

- [ ] **Step 6: Commit**

```bash
git add internal/db/db.go internal/db/db_test.go internal/media/config.go internal/server/notes.go internal/server/notes_test.go
git commit -m "feat: add media schema and note attachment shape"
```

## Task 2: Implement the encrypted object format and local encrypted cache

**Files:**
- Create: `internal/media/types.go`
- Create: `internal/media/crypto.go`
- Create: `internal/media/cache.go`
- Test: `internal/media/crypto_test.go`
- Test: `internal/media/service_test.go`

- [ ] **Step 1: Write failing crypto tests first**

Add these tests:

```go
func TestChunkedEncryptDecryptRoundTrip(t *testing.T) { /* 3 chunks + partial tail */ }
func TestChunkTamperIsRejected(t *testing.T) { /* flip one byte in chunk 2 and expect auth failure */ }
func TestNonceDerivationIsUniquePerChunk(t *testing.T) { /* chunk 0 nonce != chunk 1 nonce */ }
```

- [ ] **Step 2: Define the encrypted object format**

Use a compact format in `internal/media/crypto.go`:

```go
const (
    objectMagic = "MEF1"
    chunkSize   = 1 << 20 // 1 MiB
)

// file layout:
// magic[4] | version[1] | chunk_size[4] | repeated(chunk_plain_len[4] | chunk_ciphertext[n])
```

`aes_nonce` from the DB is the base nonce. Derive chunk nonce by copying the base nonce and adding the chunk index into the last 8 bytes.

- [ ] **Step 3: Implement encrypt/decrypt helpers around files/streams**

Create these signatures:

```go
func EncryptToFile(src io.Reader, dst *os.File, key, baseNonce []byte) (ciphertextSHA256 string, plaintextSize int64, ciphertextSize int64, err error)
func DecryptToWriter(src io.Reader, dst io.Writer, key, baseNonce []byte) error
```

The decrypt path must authenticate each chunk before writing that chunk’s plaintext to `dst`.

- [ ] **Step 4: Add encrypted cache helpers**

Create `internal/media/cache.go`:

```go
type Cache struct { Root string }

func (c Cache) PathFor(fileID int64, ciphertextSHA256 string) string
func (c Cache) Put(fileID int64, ciphertextSHA256 string, src io.Reader) error
func (c Cache) Open(fileID int64, ciphertextSHA256 string) (*os.File, error)
func (c Cache) Delete(fileID int64, ciphertextSHA256 string) error
```

Use temp-file + rename so cache writes are atomic.

- [ ] **Step 5: Run focused media tests**

Run:

```bash
go test ./internal/media -run 'TestChunkedEncryptDecryptRoundTrip|TestChunkTamperIsRejected|TestNonceDerivationIsUniquePerChunk' -v
```

Expected: all crypto tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/media/types.go internal/media/crypto.go internal/media/cache.go internal/media/crypto_test.go internal/media/service_test.go
git commit -m "feat: add encrypted media object format and cache"
```

## Task 3: Implement S3 client logic and job-driven replica repair/delete

**Files:**
- Create: `internal/media/s3.go`
- Create: `internal/media/service.go`
- Modify: `internal/server/server.go`
- Modify: `internal/jobs/jobs_test.go`
- Test: `internal/media/service_test.go`

- [ ] **Step 1: Write failing service tests for replica state and repair**

Add these tests with fake replicas:

```go
func TestCreateFileMarksPerEndpointStates(t *testing.T) { /* one success, one failure */ }
func TestCreateFileFailsWhenAllReplicasFail(t *testing.T) { /* initial request must fail if every endpoint fails */ }
func TestRepairRetriesUploadFailedReplica(t *testing.T) { /* failed replica becomes uploaded */ }
func TestDeleteRetriesDeleteFailedReplica(t *testing.T) { /* delete_failed becomes deleted */ }
func TestReadFallsBackToHealthyReplicaThenCachesEncryptedBytes(t *testing.T) { /* no local cache, second replica works */ }
func TestPendingInlineCleanupDeletesAbandonedFile(t *testing.T) { /* stale pending file with no refs is reaped */ }
```

- [ ] **Step 2: Build the S3 client wrapper**

In `internal/media/s3.go`, wrap AWS SDK v2 so the rest of the app works against an interface:

```go
type ReplicaStore interface {
    Put(ctx context.Context, endpoint EndpointConfig, key string, src io.Reader, size int64) (etag string, err error)
    Get(ctx context.Context, endpoint EndpointConfig, key string) (io.ReadCloser, error)
    Delete(ctx context.Context, endpoint EndpointConfig, key string) error
}
```

Open a fresh file reader per endpoint upload from the encrypted temp file so the plaintext is encrypted exactly once.

- [ ] **Step 3: Implement media service orchestration**

Create `internal/media/service.go` with methods like:

```go
type Service struct { /* db, cache, config, replicaStore */ }

func (s *Service) CreateAttachment(ctx context.Context, noteID int64, filename, mime string, src io.Reader) (FileRecord, []ReplicaResult, error)
func (s *Service) CreatePendingInline(ctx context.Context, noteID int64, filename, mime string, src io.Reader) (FileRecord, []ReplicaResult, error)
func (s *Service) ReadFile(ctx context.Context, fileID int64, w io.Writer) (FileRecord, error)
func (s *Service) RemoveAttachment(ctx context.Context, noteID, fileID int64) error
func (s *Service) ReconcileInlineRefs(ctx context.Context, noteID int64, body string) (orphanedFileIDs []int64, err error)
func (s *Service) CollectDeletableFilesAfterNoteDelete(ctx context.Context, noteID int64) ([]int64, error)
```

Behavior:

- encrypt to temp file once
- insert `files` row + `file_s3` rows + `files_refs` row(s) in one DB transaction
- upload encrypted temp file to all endpoints concurrently for the initial request
- persist per-endpoint result state
- commit success only when at least one replica upload succeeded
- if all replicas fail, abort the request and clean up any partial remote/cache state from that attempt
- enqueue repair work after commit for failed endpoints

- [ ] **Step 4: Register media jobs in `internal/server/server.go`**

Use the existing manager in two ways:

```go
s.jobManager.UpsertDefinitions("_media", []jobs.CronJob{
    {Name: "repair_replicas", Schedule: "@every 1m", Task: s.mediaRepairSweepTask},
    {Name: "cleanup_pending_inline", Schedule: "@every 1h", Task: s.mediaPendingInlineCleanupTask},
})

s.jobManager.RegisterAdHoc("_media", []jobs.CronJob{
    {Name: "repair_file_replica", Task: s.mediaRepairReplicaTask},
    {Name: "delete_file_replica", Task: s.mediaDeleteReplicaTask},
})
```

The hourly cleanup job is a safety net for dropped files that never reach a save event because the tab is closed.

- [ ] **Step 5: Add one job-manager test proving ad-hoc enqueue still works for non-plugin media jobs**

Extend `internal/jobs/jobs_test.go` with a tiny `_media` registration/enqueue assertion instead of modifying core job behavior.

- [ ] **Step 6: Run focused tests**

Run:

```bash
go test ./internal/jobs ./internal/media -v
```

Expected: new media tests pass; existing job tests remain green.

- [ ] **Step 7: Commit**

```bash
git add internal/media/s3.go internal/media/service.go internal/media/service_test.go internal/server/server.go internal/jobs/jobs_test.go
git commit -m "feat: add media replica repair and delete jobs"
```

## Task 4: Add file upload/remove/proxy HTTP handlers and auth coverage

**Files:**
- Create: `internal/server/files.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/auth.go`
- Test: `internal/server/files_test.go`
- Test: `internal/server/auth_test.go`

- [ ] **Step 1: Write failing HTTP tests first**

Add tests for:

```go
func TestUploadAttachmentCreatesAttachmentRef(t *testing.T) {}
func TestUploadFailsWhenAllReplicasFail(t *testing.T) {}
func TestUploadInlineMarksPendingInline(t *testing.T) {}
func TestUploadInlineReturnsImageMarkdownWhenMimeIsImage(t *testing.T) {}
func TestServeFileRequiresAuth(t *testing.T) {}
func TestServeFileIgnoresCosmeticNoteID(t *testing.T) {}
func TestDeleteAttachmentRemovesOnlyThisNotesAttachmentRef(t *testing.T) {}
```

- [ ] **Step 2: Add `/file/` to authenticated API path detection**

In `internal/server/auth.go`, update `isAPIPath` so `/file/...` goes through `requireAuth`:

```go
return p == "/health" || p == "/notes" || strings.HasPrefix(p, "/notes/") ||
    p == "/jobs" || strings.HasPrefix(p, "/jobs/") ||
    strings.HasPrefix(p, "/webauthn/") || strings.HasPrefix(p, "/file/")
```

- [ ] **Step 3: Implement handlers in `internal/server/files.go`**

Add handlers with these responsibilities:

```go
func (s *Server) uploadAttachment(w http.ResponseWriter, r *http.Request)
func (s *Server) uploadInlineFile(w http.ResponseWriter, r *http.Request)
func (s *Server) deleteAttachment(w http.ResponseWriter, r *http.Request)
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request)
```

Use `multipart/form-data` parsing, sniff MIME from the first bytes of the uploaded content instead of trusting only the browser-provided type, and return JSON payloads including `id`, `filename`, `mime_type`, `is_image`, and `url`. Inline-upload responses should also include a server-derived `markdown` field so the frontend inserts exactly the link/image syntax implied by the canonical MIME classification.

- [ ] **Step 4: Register routes in `internal/server/server.go`**

Extend the `/notes/` switch to recognize:

- `POST /notes/:id/files`
- `POST /notes/:id/files/inline`
- `DELETE /notes/:id/files/:fileID`

and add:

```go
mux.HandleFunc("/file/", s.serveFile)
```

- [ ] **Step 5: Implement `serveFile` as authenticated cache-first proxy**

Flow:

1. parse `fileID`
2. look up file metadata by `fileID`
3. open encrypted local cache if present
4. otherwise fetch encrypted object from the first healthy replica and atomically cache it
5. decrypt chunk-by-chunk into the HTTP response
6. set `Content-Type` from DB metadata

Never write plaintext to disk.

- [ ] **Step 6: Run the HTTP/auth test slice**

Run:

```bash
go test ./internal/server -run 'TestUploadAttachmentCreatesAttachmentRef|TestUploadFailsWhenAllReplicasFail|TestUploadInlineMarksPendingInline|TestUploadInlineReturnsImageMarkdownWhenMimeIsImage|TestServeFileRequiresAuth|TestServeFileIgnoresCosmeticNoteID|TestDeleteAttachmentRemovesOnlyThisNotesAttachmentRef' -v
```

Expected: all new file-route tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/server/files.go internal/server/files_test.go internal/server/server.go internal/server/auth.go internal/server/auth_test.go
git commit -m "feat: add authenticated media upload and proxy routes"
```

## Task 5: Reconcile markdown refs on note save and preserve retention semantics

**Files:**
- Modify: `internal/server/notes.go`
- Create: `internal/media/refs.go`
- Test: `internal/media/refs_test.go`
- Test: `internal/server/notes_test.go`

- [ ] **Step 1: Write failing reference-extraction and note-lifecycle tests**

Add tests for:

```go
func TestExtractReferencedFileIDsFromMarkdown(t *testing.T) {}
func TestUpdateNoteConvertsPendingInlineToInlineRef(t *testing.T) {}
func TestUpdateNoteDeletesUnusedPendingInlineFiles(t *testing.T) {}
func TestDeleteNoteKeepsFileWhenAnotherNoteStillReferencesIt(t *testing.T) {}
func TestDeleteNoteDeletesFileWhenLastRefDisappears(t *testing.T) {}
```

- [ ] **Step 2: Implement markdown file-ID extraction**

In `internal/media/refs.go`, use a URL-focused regex over the raw markdown body:

```go
var fileURLRe = regexp.MustCompile(`/file/[^/\s)]+/([0-9]+)`)
```

Return a de-duplicated `[]int64`. Images and links both work because both contain the same URL shape.

- [ ] **Step 3: Reconcile inline refs inside `createNote` and `updateNote`**

After writing the new `updates` row but before committing:

- parse referenced file IDs
- delete existing `inline` refs for this note
- insert current `inline` refs
- clear `pending_inline_note_id` and `pending_inline_at` for newly referenced pending files belonging to this note
- identify pending files for this note that are still unreferenced
- soft-delete those unreferenced files and collect their IDs for post-commit delete jobs

Do **not** touch `attachment` refs during body reconciliation.

- [ ] **Step 4: Make note deletion retention-aware**

Refactor `deleteNote` to use a transaction:

1. collect `file_id`s currently referenced by that note plus any pending-inline files still owned by that note
2. delete the note
3. query which affected files still have any `files_refs`
4. soft-delete only the files with zero remaining refs
5. commit
6. enqueue replica delete jobs after commit

Because `files.original_note_id` uses `ON DELETE SET NULL`, deleting the original note will not destroy shared files.

- [ ] **Step 5: Run focused lifecycle tests**

Run:

```bash
go test ./internal/media ./internal/server -run 'TestExtractReferencedFileIDsFromMarkdown|TestUpdateNoteConvertsPendingInlineToInlineRef|TestUpdateNoteDeletesUnusedPendingInlineFiles|TestDeleteNoteKeepsFileWhenAnotherNoteStillReferencesIt|TestDeleteNoteDeletesFileWhenLastRefDisappears' -v
```

Expected: lifecycle tests pass and document the expected retention rules.

- [ ] **Step 6: Commit**

```bash
git add internal/media/refs.go internal/media/refs_test.go internal/server/notes.go internal/server/notes_test.go
git commit -m "feat: reconcile inline file refs on note save"
```

## Task 6: Add frontend upload, drag/drop, cursor insertion, and attachment rendering

**Files:**
- Modify: `frontend/src/api.js`
- Modify: `frontend/src/views/NotesView.vue`
- Create: `frontend/src/components/NoteAttachments.vue`

- [ ] **Step 1: Add multipart-capable API helpers**

In `frontend/src/api.js`, keep JSON helpers for notes and add file helpers that do not force `Content-Type: application/json`:

```js
function authOnlyHeaders(token) {
    return { Authorization: `Bearer ${token}` };
}

export async function uploadAttachment(token, noteId, file) { /* FormData POST /notes/:id/files */ }
export async function uploadInlineFile(token, noteId, file) { /* FormData POST /notes/:id/files/inline */ }
export async function deleteAttachment(token, noteId, fileId) { /* DELETE /notes/:id/files/:fileId */ }
```

- [ ] **Step 2: Create the attachment list component**

Add `frontend/src/components/NoteAttachments.vue` to render files after the note body:

```vue
<template>
  <div v-if="attachments?.length" class="note-attachments">
    <h4>Attachments</h4>
    <ul>
      <li v-for="file in attachments" :key="file.id">
        <a :href="file.url" target="_blank" rel="noreferrer">{{ file.filename }}</a>
        <button v-if="editing" @click="$emit('remove', file)">Remove</button>
      </li>
    </ul>
  </div>
</template>
```

- [ ] **Step 3: Teach `NotesView.vue` to ensure the note exists before upload**

The current `save()` path already creates unsaved notes. Reuse that behavior with:

```js
async function ensureSelectedNoteSaved() {
    if (!selected.value?.id) await save();
    if (!selected.value?.id) throw new Error("Save the note before uploading files");
}
```

Call it before append-file and drag/drop uploads.

- [ ] **Step 4: Add append-file UI and removal flow**

Add an “Attach file” button near the editor actions. On selection:

1. ensure note is saved
2. call `uploadAttachment`
3. append returned file metadata into `selected.attachments`
4. show it below the message body using `NoteAttachments`

When removing, call `deleteAttachment` and refresh the selected note.

- [ ] **Step 5: Add drag/drop and cursor insertion**

Use a textarea ref rather than `document.querySelector` for insertion:

```js
const bodyTextarea = ref(null);

function insertAtCursor(text) {
    const el = bodyTextarea.value;
    const start = el.selectionStart;
    const end = el.selectionEnd;
    editBody.value = editBody.value.slice(0, start) + text + editBody.value.slice(end);
    nextTick(() => {
        el.focus();
        const pos = start + text.length;
        el.setSelectionRange(pos, pos);
    });
    dirty.value = true;
}
```

Add `@dragover.prevent` and `@drop.prevent="onBodyDrop"` to the editor textarea.

`onBodyDrop` should:

1. ensure note is saved
2. upload the dropped file with `uploadInlineFile`
3. insert the server-returned `markdown` string at the cursor
4. keep the note dirty so the next save reconciles refs and deletes unused drops

- [ ] **Step 6: Render attachments after note bodies in root and child messages**

Use `NoteAttachments` in both the selected note block and the child-note loop so attachment behavior is consistent across the thread view.

- [ ] **Step 7: Build the frontend**

Run:

```bash
npm --prefix frontend run build
```

Expected: Vite build completes and outputs to `FrontEndDist/`.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/api.js frontend/src/views/NotesView.vue frontend/src/components/NoteAttachments.vue FrontEndDist
git commit -m "feat: add note attachment and inline file UI"
```

## Task 7: End-to-end behavior verification

**Files:**
- Modify: `internal/server/files_test.go`
- Modify: `internal/server/notes_test.go`
- Modify: `internal/media/service_test.go`
- Modify: `frontend/src/views/NotesView.vue` (only if fixes are needed)

- [ ] **Step 1: Add behavior-first test coverage for the full approved feature set**

Ensure the test suite covers all of these behaviors explicitly:

```text
1. Uploading an attachment creates a persistent attachment ref even without markdown.
2. Dropping an image inserts ![](/file/:noteID/:fileID).
3. Dropping a non-image inserts [filename](/file/:noteID/:fileID).
4. Saving a note converts referenced pending-inline files into inline refs.
5. Saving a note deletes pending-inline files that are absent from the final body.
6. Upload can succeed while one replica is upload_failed.
7. Upload fails when every replica rejects the first-wave write.
8. Failed replicas are retried until they become uploaded.
9. /file/:noteID/:fileID rejects unauthenticated requests.
10. /file/:wrongNoteID/:fileID still serves when auth passes.
11. Read path uses encrypted local cache and never persists plaintext.
12. Deleting note A does not delete the file while note B still attaches or links it.
13. Deleting the last reference deletes cache + all replicas eventually.
14. The stale pending-inline janitor deletes abandoned drops that never reach a save event.
```

- [ ] **Step 2: Run the backend verification suite**

Run:

```bash
go test ./internal/media ./internal/server ./internal/db ./internal/jobs -v
```

Expected: new media tests pass; any VSS-dependent tests may skip instead of fail.

- [ ] **Step 3: Run the broad Go suite**

Run:

```bash
go test ./...
```

Expected: full repository passes, with only intentional VSS skips if the extension is unavailable.

- [ ] **Step 4: Run the frontend build one more time after backend/test fixes**

Run:

```bash
npm --prefix frontend run build
```

- [ ] **Step 5: Manual smoke checklist**

Verify manually in the browser:

```text
- Attach a PDF to a note without linking it in markdown; it appears in the attachment list.
- Drop a PNG into the editor; markdown image syntax is inserted at the cursor.
- Remove the inserted markdown before save; after save, the dropped file disappears.
- Copy the file URL into a second note; deleting the first note does not break the file.
- Shut down one S3 endpoint; upload still succeeds and the failed endpoint appears as upload_failed.
- Bring the endpoint back; repair job eventually marks it uploaded.
- Leave a dropped file unused and abandon the tab/session; the janitor job eventually deletes it.
```

- [ ] **Step 6: Final commit**

```bash
git add .
git commit -m "feat: add encrypted redundant media backend"
```

---

## Behavior-First Test Matrix

Use this matrix to check plan coverage while implementing:

| Behavior | Primary tests |
|---|---|
| Attachments survive without markdown | `TestUploadAttachmentCreatesAttachmentRef`, note payload attachment assertions |
| Inline dropped file removed when unused | `TestUpdateNoteDeletesUnusedPendingInlineFiles` |
| Cross-note references are supported | `TestDeleteNoteKeepsFileWhenAnotherNoteStillReferencesIt` |
| Replica failure does not block save once one replica succeeds | `TestCreateFileMarksPerEndpointStates` |
| All-replica initial failure rejects the request | `TestCreateFileFailsWhenAllReplicasFail`, `TestUploadFailsWhenAllReplicasFail` |
| Failed endpoints repair later | `TestRepairRetriesUploadFailedReplica` |
| `/file/` requires auth | `TestServeFileRequiresAuth` + auth path coverage |
| `noteID` is cosmetic | `TestServeFileIgnoresCosmeticNoteID` |
| Only encrypted bytes hit disk | cache/service tests + manual inspection |
| Images insert image markdown | `TestUploadInlineReturnsImageMarkdownWhenMimeIsImage`, frontend/manual smoke |
| Non-images insert normal link markdown | frontend/manual smoke |
| Abandoned dropped files are eventually reaped | `TestPendingInlineCleanupDeletesAbandonedFile` + manual smoke |

## Implementation Notes to Keep the Feature Safe

- Generate the `files` row before upload so `file_id` is available for `storage_key` and returned URL.
- Set `pending_inline_at` on inline uploads and clear it only when the next save successfully reconciles the file into an active inline ref.
- Use `original_note_id` only as provenance; do not use it for authorization or retention.
- Keep attachment refs and inline refs separate; they solve different product requirements.
- Initial upload success requires at least one live replica; failures on the other replicas become repair work, not user-visible failure.
- If the first wave fails on every endpoint, clean up any partial cache/remote state before returning an error.
- Enqueue jobs **after** the transaction commits so workers never chase uncommitted rows.
- On serve, prefer local cache first, then any healthy replica, then return 502/503 if nothing is readable.
- Never trust browser- or S3-provided metadata for mime type or filename; use server sniffing + the DB as the source of truth.
- Because plaintext AES keys live in SQLite by explicit product choice, avoid logging file keys, nonces, or decrypted content anywhere.

## Self-Review

- Schema coverage: includes `files`, `file_s3`, and `files_refs`, plus the approved retention semantics.
- Upload flows: append + drag/drop are both planned, including unsaved-note handling.
- Delete flows: note delete, attachment remove, pending-inline cleanup, and replica delete retry are all covered.
- Read path: authenticated, cache-backed, encrypted-at-rest locally, note ID cosmetic.
- Tests: all requested behavior is represented directly in unit/integration/manual verification steps.
