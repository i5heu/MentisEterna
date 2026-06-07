# Backups

MentisEterna backs up the SQLite database **and all active media ciphertext blobs stored in S3** using AES-256-GCM encryption and stores the resulting encrypted bundle in S3-compatible object storage. Backups run automatically every 12 hours and are safe to take while the database is being written to (WAL mode).

## Table of Contents

- [Encryption Format](#encryption-format)
- [Snapshot Mechanism](#snapshot-mechanism)
- [S3 Object Naming](#s3-object-naming)
- [Retention Policy](#retention-policy)
- [On-Disk Format Summary (Restoration Guide)](#on-disk-format-summary-restoration-guide)
- [Setup](#setup)
- [Usage](#usage)
  - [Running Backups](#running-backups)
  - [Triggering On-Demand](#triggering-on-demand)
  - [Restoring a Backup](#restoring-a-backup)
- [Architecture](#architecture)
- [Security Considerations](#security-considerations)

---

## Encryption Format

```
[12-byte random nonce][ciphertext + 16-byte GCM auth tag]
```

Every backup uses **AES-256-GCM** — an authenticated encryption mode that provides both confidentiality and integrity verification. A fresh 12-byte (96-bit) random nonce is generated for each backup via `crypto/rand`, so identical plaintext always produces different ciphertext.

Key details:

| Property | Value |
|---|---|
| Algorithm | AES-256-GCM |
| Key size | 256 bits (32 bytes) |
| Nonce size | 96 bits (12 bytes) |
| Auth tag size | 128 bits (16 bytes) |
| Nonce source | `crypto/rand.Read` |
| Total overhead | 28 bytes (12 nonce + 16 tag) |

### GCM Authentication

GCM produces an authentication tag alongside the ciphertext. During decryption, the tag is verified before any plaintext is returned. If the ciphertext has been tampered with, or the wrong key is used, decryption fails with an error — it does **not** silently return garbage. This is the critical difference between authenticated encryption (AES-GCM) and unauthenticated modes (AES-CTR, AES-CBC without HMAC).

### Code References

| File | What |
|---|---|
| `internal/backup/crypto.go` | `Encrypt`, `Decrypt`, `KeyFromHex`, `GenerateKey` |
| `internal/backup/crypto_test.go` | Round-trip, corruption detection, wrong key, nonce uniqueness, large data (10 MB), bad key sizes |

```go
// Encrypt takes plain bytes, returns [12-byte nonce][ciphertext + 16-byte auth tag].
func Encrypt(plaintext []byte, key []byte) ([]byte, error)

// Decrypt takes the encrypted blob, returns plain bytes.
// Returns error if authentication fails (tampered data or wrong key).
func Decrypt(data []byte, key []byte) ([]byte, error)
```

---

## Snapshot Mechanism

Backups use **SQLite's Online Backup API** (`sqlite3_backup_init` → `step(-1)` → `finish` via `go-sqlite3`) rather than a raw file copy. This is the same mechanism used by the `sqlite3` CLI's `.backup` command. After the database snapshot is taken, MentisEterna reads the active `files` rows from that snapshot and bundles the corresponding encrypted S3 media objects alongside the database so the backup is self-contained.

### Why This Matters

The database runs in **WAL mode** (`_journal_mode=WAL`). A naive `cp mentis.db` while writes are in flight would:

- Capture a partially-written page → corrupt backup
- Miss WAL content not yet checkpointed into the main database file
- Capture the WAL file itself in an inconsistent state

The Online Backup API instead:

1. Opens a connection to the live database
2. Opens a second connection to a temporary destination database
3. Calls `backup_init("main", src, "main")` to set up the copy
4. Calls `step(-1)` to atomically copy all remaining pages
5. Calls `finish()` to release locks

This produces a consistent point-in-time snapshot — every page in the backup corresponds to a single committed database state, even while new writes arrive.

### Code Reference

```go
// internal/backup/backup.go
func (s *Service) snapshot() ([]byte, error)
```

The snapshot is created as a temporary file, read into memory, and the temp file is immediately cleaned up (`defer os.Remove`). The entire snapshot lives in memory during encryption and upload — acceptable for a personal notes database but not suitable for multi-GB databases.

---

## S3 Object Naming

Current backups are stored as:

```
backups/mentis-YYYY-MM-DDTHH-MM-SS.bundle.enc
```

Legacy backups used the older database-only format:

```
backups/mentis-YYYY-MM-DDTHH-MM-SS.db.enc
```

The timestamp is UTC (`time.Now().UTC().Format("2006-01-02T15-04-05")`).

Examples:

```
backups/mentis-2026-07-22T03-00-05.bundle.enc
backups/mentis-2026-07-22T15-00-03.bundle.enc
```

The `backups/` prefix is used by the purger to list and classify objects. Anything outside the `backups/mentis-*.(bundle|db).enc` patterns (with the exact timestamp format) is silently skipped during retention cleanup — it will never be deleted.

### Code Reference

```go
// internal/backup/retention.go
remoteKey := backupObjectKey(time.Now().UTC())

// internal/backup/retention.go
func parseBackupTime(key string) (time.Time, error) {
    s := strings.TrimPrefix(key, backupPrefix)  // "backups/mentis-"
    s = strings.TrimSuffix(s, backupSuffix)      // ".db.enc"
    return time.Parse("2006-01-02T15-04-05", s)
}
```

---

## Retention Policy

Backups are automatically cleaned up by a `retention_purge` cron job that runs **every 24 hours**.

### Default Policy

| Window | Rule |
|---|---|
| Last 7 days | Keep **max 3 per calendar day** (newest) |
| 7 days – 3 months | Keep **1 per ISO week** (newest in each) |
| 3 months – 5 years | Keep **1 per calendar month** (newest in each) |
| Older than 5 years | **Delete all** |

### Algorithm

Backups are processed **newest-first**. Each time bucket greedily keeps the newest backup that falls within it:

1. For backups within the last 7 days: bucket by calendar day (`2006-01-02`). Keep up to 3 newest per day.
2. For backups within the last 3 months (but older than 7 days): bucket by ISO week (`2006-W01`). Keep 1 per week.
3. For backups within the last 5 years (but older than 3 months): bucket by calendar month (`2006-01`). Keep 1 per month.
4. Everything older than 5 years: deleted.

Because the algorithm iterates newest-first, the newest backup in each bucket is always the one kept. Keys that don't match the `backups/mentis-YYYY-MM-DDTHH-MM-SS.(bundle|db).enc` naming patterns are silently skipped (they are not ours to delete).

### Purge Scope

Purge lists objects under the `backups/` prefix on **each configured S3 endpoint independently**. If listing fails on one endpoint, the others are still processed. Each delete is logged individually.

### Code Reference

| File | What |
|---|---|
| `internal/backup/retention.go` | `ClassifyBackups`, `DefaultRetentionPolicy`, `parseBackupTime` |
| `internal/backup/retention_test.go` | 11 tests covering all windows, boundaries, bucket collation, determinism |

---

## On-Disk Format Summary (Restoration Guide)

A current `.bundle.enc` file in your S3 bucket contains:

```
 Byte 0..11   : 12-byte random nonce (not secret, must be unique per backup)
 Byte 12..N-17: AES-256-GCM ciphertext
 Byte N-16..N : 16-byte GCM authentication tag
```

After decryption, the plaintext is a MentisEterna bundle with this structure:

```
MENTISETERNA-BACKUP-BUNDLE\n
TAR:
  manifest.json          # bundle version + media manifest
  db.sqlite3             # SQLite snapshot
  media/files/...        # encrypted media blobs exactly as stored in S3
```

Legacy `.db.enc` backups decrypt directly to a raw SQLite database and are still supported by the restore tool.

To restore manually (without the CLI tool):

1. Download the `.bundle.enc` or legacy `.db.enc` file from S3
2. Split: `nonce = data[0:12]`, `ciphertext = data[12:]`
3. Decrypt with AES-256-GCM using the 32-byte key and the extracted nonce
4. If the plaintext begins with `MENTISETERNA-BACKUP-BUNDLE\n`, treat the remaining bytes as a TAR archive containing `db.sqlite3` plus `media/...`
5. Otherwise, treat the plaintext as a legacy SQLite database

The built-in `restore` tool does this automatically:

```bash
# Set required environment
export BACKUP_ENCRYPTION_KEY="<64-character hex key>"
export MEDIA_S3_ENDPOINTS='[{"id":"primary","bucket":"...","endpoint":"...",...}]'

# Download + decrypt + restore DB + re-upload media to configured endpoints
go run ./cmd/restore/ backup-object-key.bundle.enc mentis_restored.db

# Or with a pre-built binary:
./restore backups/mentis-2026-07-22T03-00-05.bundle.enc mentis_restored.db
```

The restore tool tries each configured S3 endpoint in order until one succeeds. For current bundle backups it also re-uploads every bundled media object to each configured endpoint and rewrites `file_s3` rows in the restored database to match those endpoints. The resulting `.db` file is then ready to use with `DB_PATH=mentis_restored.db`.

### Code Reference

```go
// cmd/restore/main.go — standalone CLI tool
//   go run ./cmd/restore/ <s3-key> <output.db>
```

---

## Setup

### 1. Generate an Encryption Key

```bash
# Using the built-in helper:
go run -exec '' 2>/dev/null - <<'EOF'
package main
import ("fmt"; "github.com/i5heu/MentisEterna/internal/backup")
func main() { k, _ := backup.GenerateKey(); fmt.Println(k) }
EOF

# Or with openssl:
openssl rand -hex 32
```

This produces a 64-character hex string (32 bytes = 256 bits).

### 2. Configure Environment Variables

```bash
export BACKUP_ENCRYPTION_KEY="<64-character hex key>"
export MEDIA_CACHE_DIR="/path/to/media/cache"
export MEDIA_S3_ENDPOINTS='[
  {
    "id": "primary",
    "bucket": "my-bucket",
    "region": "us-east-1",
    "endpoint": "https://s3.amazonaws.com",
    "access_key_id": "AKIA...",
    "secret_access_key": "...",
    "force_path_style": false
  }
]'
```

| Variable | Required | Purpose |
|---|---|---|
| `BACKUP_ENCRYPTION_KEY` | Yes | 64-char hex AES-256 key |
| `MEDIA_CACHE_DIR` | Yes | Local directory for media cache |
| `MEDIA_S3_ENDPOINTS` | Yes | JSON array of S3 endpoint configs |

### 3. What Happens If Env Vars Are Missing

- If `BACKUP_ENCRYPTION_KEY` is not set → backups are **silently disabled**. The server logs: `backup: BACKUP_ENCRYPTION_KEY not set — backups disabled`
- If the key is invalid (wrong length, not hex) → `backup: invalid BACKUP_ENCRYPTION_KEY (...) — backups disabled`
- If no S3 endpoints are configured → `backup: encryption key set but no S3 endpoints configured — backups disabled`

The server always starts and functions normally in these cases — only the backup subsystem is disabled.

---

## Usage

### Running Backups

Backups run automatically on a **`@every 12h`** schedule (twice daily, starting from server startup time). The `retention_purge` job runs on a **`@every 24h`** schedule.

Both jobs appear in the built-in job queue UI (the ⚙ panel) alongside all other jobs (title generation, VSS indexing, media repair).

### Triggering On-Demand

```bash
# Trigger an immediate backup:
curl -X POST http://localhost:8080/backup/trigger

# Trigger an immediate retention purge:
curl -X POST http://localhost:8080/backup/purge
```

Both return:

```json
{
  "status": "queued",
  "run_id": 42,
  "message": "Retention purge enqueued. Check /jobs for progress."
}
```

### Restoring a Backup

```bash
go run ./cmd/restore/ <s3-backup-key> <output.db>
```

Example:

```bash
go run ./cmd/restore/ backups/mentis-2026-07-22T03-00-05.bundle.enc mentis_restored.db
```

The tool:

1. Reads `BACKUP_ENCRYPTION_KEY` and `MEDIA_S3_ENDPOINTS` from the environment
2. Tries each configured S3 endpoint in order
3. Downloads the encrypted backup from the first endpoint that responds
4. Decrypts with AES-256-GCM (fails if the key is wrong or data is corrupted)
5. Restores the SQLite database to the output path
6. For bundle backups, re-uploads each bundled media object to every configured endpoint and rewrites `file_s3` rows accordingly

Then you can start MentisEterna with `DB_PATH=mentis_restored.db`.

---

## Architecture

```
┌──────────────────────────────────────────────────────┐
│                  cmd/server/main.go                  │
│  Starts backup.Service, registers cron jobs         │
│                                                      │
│  @every 12h  → backupTask  → backup.Service.Run()   │
│  @every 24h  → purgeTask   → backup.Service.Purge() │
│  POST /backup/trigger → on-demand backup             │
│  POST /backup/purge    → on-demand purge             │
└──────────────┬───────────────────────────────────────┘
               │
┌──────────────▼───────────────────────────────────────┐
│              internal/backup/backup.go               │
│                                                      │
│  Service.Run():                                      │
│    1. snapshot() → SQLite Backup API → []byte        │
│    2. buildBundle() → DB + active S3 media → []byte  │
│    3. Encrypt(bundle, key) → []byte                  │
│    4. Store.Put(endpoint, key, encrypted)            │
│                                                      │
│  Service.Purge():                                    │
│    1. Store.List(endpoint, "backups/") → keys        │
│    2. ClassifyBackups(keys, now, policy) → toDelete  │
│    3. Store.Delete(endpoint, key) for each toDelete  │
└──────┬───────────────┬───────────────────────────────┘
       │               │
       ▼               ▼
┌──────────────┐  ┌────────────────────────────────────┐
│ crypto.go    │  │ retention.go                       │
│              │  │                                    │
│ Encrypt()    │  │ DefaultRetentionPolicy()           │
│ Decrypt()    │  │ ClassifyBackups(keys, now, policy) │
│ GenerateKey()│  │ parseBackupTime(key)               │
│ KeyFromHex() │  │                                    │
└──────────────┘  └────────────────────────────────────┘

┌──────────────────────────────────────────────────────┐
│                cmd/restore/main.go                   │
│                                                      │
│  1. Store.Get(endpoint, remoteKey) → encrypted       │
│  2. Decrypt(encrypted, key) → plaintext bundle/db    │
│  3. RestorePayload(...) → db write + media re-upload │
└──────────────────────────────────────────────────────┘
```

### Source Files

| File | Purpose |
|---|---|
| `internal/backup/crypto.go` | `Encrypt`, `Decrypt`, `KeyFromHex`, `GenerateKey` |
| `internal/backup/crypto_test.go` | 10 tests: round-trip, corruption, wrong key, nonce uniqueness, large data |
| `internal/backup/backup.go` | `Service.Run()` (snapshot + bundle + encrypt + upload), `Service.Purge()` (retention cleanup), `snapshot()` (SQLite Backup API) |
| `internal/backup/bundle.go` | Bundle format, media inclusion, `RestorePayload`, `file_s3` normalization on restore |
| `internal/backup/retention.go` | `ClassifyBackups`, `DefaultRetentionPolicy`, `parseBackupTime`, `backupObjectKey` |
| `internal/backup/retention_test.go` | Retention tests for bundle + legacy backup key formats |
| `internal/server/server.go` | `handleBackupTrigger`, `handleBackupPurge` handlers, cron job registration |
| `internal/server/notes.go` | `backupTask`, `purgeTask` job handlers |
| `internal/media/s3.go` | `ReplicaStore` interface + `S3Store` implementation (`Put`, `Get`, `Delete`, `List`) |
| `cmd/restore/main.go` | Standalone CLI restoration tool |

---

## Security Considerations

### Strengths

- **AES-256-GCM** with random 96-bit nonces — authenticated encryption, no nonce reuse
- **Tamper detection**: GCM authentication tag is verified before any plaintext is returned; corrupted or tampered backups fail to decrypt with an explicit error
- **Safe snapshots**: SQLite Online Backup API guarantees consistent point-in-time database state, even under concurrent writes in WAL mode
- **Self-contained restores**: current backups include active encrypted media blobs, so a restored database is not left pointing at missing S3 objects
- **S3 transport**: SigV4-signed HTTPS requests with configurable endpoints
- **Multi-endpoint redundancy**: Each backup is uploaded to every configured S3 endpoint; a single endpoint failure doesn't block backups on the others

### Practical Risks (Personal Notes App)

- **Key in environment variable**: The encryption key lives in `BACKUP_ENCRYPTION_KEY`. Anyone with access to the environment (shell history, `.profile`, systemd unit file, `docker inspect`) can read it. For a personal app this is acceptable; for multi-tenant deployments, use a secrets manager.
- **No key rotation**: There is no mechanism to rotate keys or re-encrypt existing backups. If the key is compromised, all historical backups are compromised.
- **Memory-based processing**: The database snapshot, backup bundle, encrypted blob, and any fetched media objects all pass through memory during backup/restore. For a multi-GB library, this would require adjusting the approach (streaming archive/encryption, buffered uploads).
- **No client-side key derivation**: The key is raw bytes from a hex string. There is no PBKDF2/Argon2 stretching against a passphrase. The 64-char hex key **is** the key material.
