# OCR (Optical Character Recognition)

OCR automatically extracts text from uploaded images using Ollama's multimodal `glm-ocr:latest` model.

## Architecture

```
files_ocr table  ←  media/ocr.go (OCR service)  ←  llm/ocr.go (OCR client)  →  Ollama /api/generate
       ↑                                                    ↑
       |                                          Job system triggers
  vss_files_ocr                                  ocr_file task after upload
  (VSS embedding)                                          ↓
       ↑                                        sync_ocr_embedding task
       |
  /notes/search  ←  vss_search on vss_files_ocr
                    (resolves file→note via files_refs)
```

## How It Works

1. **Upload triggers OCR** — When a file is uploaded (as an attachment or inline), the server enqueues a background `ocr_file` job if the MIME type is an image.
2. **OCR job runs** — The job decrypts the file, sends the binary image data (base64-encoded) to Ollama's `/api/generate` endpoint with the configured OCR model, and stores the result in the `files_ocr` table.
3. **Embedding generation** — After a successful OCR, a `sync_ocr_embedding` job generates a VSS vector from the OCR text and stores it in `vss_files_ocr` (separate from `vss_notes`).
4. **Search** — The `/notes/search` endpoint queries both `vss_notes` (note body) and `vss_files_ocr` (OCR text). OCR file hits are resolved to their parent notes via `files_refs`. Results are merged by minimum distance per note.
5. **Retrieve results** — Call `GET /files/:fileID/ocr` to get the recognized text.

## Database Tables

### `files_ocr` — OCR results

```sql
CREATE TABLE files_ocr (
    file_id    INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
    ocr_text   TEXT    NOT NULL DEFAULT '',
    model      TEXT    NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    error      TEXT
);
```

- `file_id` is the primary key, one row per file.
- `ocr_text` contains the recognized text.
- `model` records which Ollama model was used.
- `error` is set when OCR fails (e.g., unsupported MIME type, model error, connection timeout).

### `vss_files_ocr` — Semantic search vectors

Virtual table using `sqlite-vss`. One row per OCR'd file, `rowid = files.id`.

```sql
CREATE VIRTUAL TABLE vss_files_ocr USING vss0(
    ocr_embedding(2560)
);
```

VSS vectors are 2560-dimensional (same as `vss_notes`). The `rowid` directly references `files.id`, so resolving a search hit to a note requires a join through `files_refs`.

## HTTP Endpoint

### `GET /files/:fileID/ocr`

Returns the OCR result for a file.

**Response (200 OK)**:
```json
{
    "file_id": 42,
    "ocr_text": "The quick brown fox jumps over the lazy dog.",
    "model": "glm-ocr:latest"
}
```

**Response (404)** — No OCR result found (file doesn't exist, OCR hasn't run yet, or failed).

**Response (200 with error)** — OCR completed but with an error:
```json
{
    "file_id": 42,
    "ocr_text": "",
    "model": "glm-ocr:latest",
    "error": "unsupported MIME type for OCR: application/pdf"
}
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `OLLAMA_OCR_MODEL` | `glm-ocr:latest` | The multimodal model used for OCR |
| `OLLAMA_BASE_URL` | `http://localhost:11434` | Ollama instance URL (shared with embeddings/chat) |

## Supported Image Types

OCR is attempted for these MIME types:
- `image/png`
- `image/jpeg`
- `image/gif`
- `image/webp`
- `image/bmp`
- `image/tiff`
- `image/svg+xml`

Non-image types (PDF, text, video, etc.) are skipped gracefully and stored with an error message.

## Job System

| Task | Plugin | Type | Trigger |
|---|---|---|---|
| `ocr_file` | `_media` | Ad-hoc | File upload (image MIME types) |
| `sync_ocr_embedding` | `_system` | Ad-hoc | After successful OCR (generates VSS vector) |

OCR jobs appear in the `/jobs` list and can be retried or cancelled like any other job.

## Re-OCR

To re-run OCR on an existing file:
1. Delete the existing row from `files_ocr`:
   ```sql
   DELETE FROM files_ocr WHERE file_id = 42;
   ```
2. Also remove the old embedding:
   ```sql
   DELETE FROM vss_files_ocr WHERE rowid = 42;
   ```
3. Manually enqueue the OCR job via the job system or trigger a re-upload.

## Search Integration

OCR text is semantically searchable. The `/notes/search` endpoint queries both:
- `vss_notes` — note body embeddings
- `vss_files_ocr` — OCR text embeddings

When an OCR result matches a search query, the system resolves the file to its parent note(s) via `files_refs` and includes those notes in results. Distances are merged per-note (best/minimum distance wins).

This means searching for "invoice" will find notes that have scanned images containing the word "invoice", even if the note body doesn't mention it.

## Error Handling

OCR is **best-effort** — failures don't block file upload. Errors are logged and stored in the `files_ocr.error` column. Common failure modes:

- **Unsupported MIME type**: The file is not an image.
- **Model not found**: `glm-ocr:latest` isn't pulled in Ollama (`ollama pull glm-ocr:latest`).
- **Connection error**: Ollama is unreachable.
- **Decryption error**: File cache or S3 replica is unavailable.

## Testing

```bash
# Run all OCR-related tests
go test ./internal/db/ -run "TestFilesOCR" -v
go test ./internal/llm/ -run "TestOCR" -v
go test ./internal/media/ -run "TestSaveAndGetOCRResult|TestSaveOCRError|TestSaveOCRResultUpserts|TestIsOCRable" -v
go test ./internal/server/ -run "TestHandleFileOCR|TestIsAPIPath|TestSearchFindsNoteByOCRText" -v

# Run everything
go test ./internal/db/ ./internal/llm/ ./internal/media/ ./internal/server/ -v
```
