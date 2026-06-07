package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/i5heu/MentisEterna/internal/media"
)

const (
	bundleMagic        = "MENTISETERNA-BACKUP-BUNDLE\n"
	bundleVersion      = 1
	bundleManifestPath = "manifest.json"
	bundleDBPath       = "db.sqlite3"
	bundleMediaPrefix  = "media/"
)

type bundleManifest struct {
	Version   int                `json:"version"`
	CreatedAt string             `json:"created_at"`
	DBPath    string             `json:"db_path"`
	Media     []bundleMediaEntry `json:"media"`
}

type bundleMediaEntry struct {
	FileID           int64  `json:"file_id"`
	StorageKey       string `json:"storage_key"`
	CiphertextSHA256 string `json:"ciphertext_sha256"`
}

type RestoreResult struct {
	Format      string
	DBBytes     int
	MediaFiles  int
	MediaCopies int
}

func (s *Service) buildBundle(ctx context.Context, snapshot []byte) ([]byte, int, error) {
	manifest, err := buildBundleManifest(snapshot)
	if err != nil {
		return nil, 0, err
	}

	var buf bytes.Buffer
	buf.WriteString(bundleMagic)

	tw := tar.NewWriter(&buf)
	defer tw.Close()

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, 0, fmt.Errorf("marshal manifest: %w", err)
	}
	if err := writeTarFile(tw, bundleManifestPath, manifestBytes); err != nil {
		return nil, 0, err
	}
	if err := writeTarFile(tw, bundleDBPath, snapshot); err != nil {
		return nil, 0, err
	}

	for _, item := range manifest.Media {
		ciphertext, err := s.fetchMediaCiphertext(ctx, item.StorageKey, item.CiphertextSHA256)
		if err != nil {
			return nil, 0, err
		}
		if err := writeTarFile(tw, bundleMediaPrefix+item.StorageKey, ciphertext); err != nil {
			return nil, 0, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, 0, fmt.Errorf("close tar writer: %w", err)
	}

	return buf.Bytes(), len(manifest.Media), nil
}

func buildBundleManifest(snapshot []byte) (*bundleManifest, error) {
	db, tmpPath, err := openSnapshotDB(snapshot)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpPath)
	defer db.Close()

	rows, err := db.Query(`
        SELECT id, storage_key, ciphertext_sha256
        FROM files
        WHERE deleted_at IS NULL
        ORDER BY id ASC
    `)
	if err != nil {
		return nil, fmt.Errorf("query backup media manifest: %w", err)
	}
	defer rows.Close()

	manifest := &bundleManifest{
		Version:   bundleVersion,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		DBPath:    bundleDBPath,
	}

	for rows.Next() {
		var item bundleMediaEntry
		if err := rows.Scan(&item.FileID, &item.StorageKey, &item.CiphertextSHA256); err != nil {
			return nil, fmt.Errorf("scan backup media manifest: %w", err)
		}
		manifest.Media = append(manifest.Media, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate backup media manifest: %w", err)
	}

	return manifest, nil
}

func openSnapshotDB(snapshot []byte) (*sql.DB, string, error) {
	tmpFile, err := os.CreateTemp("", "mentis-backup-snapshot-*.db")
	if err != nil {
		return nil, "", fmt.Errorf("create snapshot temp db: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(snapshot); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return nil, "", fmt.Errorf("write snapshot temp db: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return nil, "", fmt.Errorf("close snapshot temp db: %w", err)
	}

	db, err := sql.Open("sqlite3", tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return nil, "", fmt.Errorf("open snapshot temp db: %w", err)
	}
	return db, tmpPath, nil
}

func (s *Service) fetchMediaCiphertext(ctx context.Context, storageKey, expectedSHA string) ([]byte, error) {
	var lastErr error
	for _, ep := range s.Endpoints {
		rc, err := s.Store.Get(ctx, ep, storageKey)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", ep.ID, err)
			continue
		}

		data, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("%s: read media %s: %w", ep.ID, storageKey, readErr)
			continue
		}
		if closeErr != nil {
			lastErr = fmt.Errorf("%s: close media %s: %w", ep.ID, storageKey, closeErr)
			continue
		}

		got := sha256.Sum256(data)
		gotHex := hex.EncodeToString(got[:])
		if expectedSHA != "" && gotHex != expectedSHA {
			lastErr = fmt.Errorf("%s: media %s hash mismatch: got %s want %s", ep.ID, storageKey, gotHex, expectedSHA)
			continue
		}
		return data, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("media %s not found on any endpoint", storageKey)
	}
	return nil, fmt.Errorf("fetch media %s: %w", storageKey, lastErr)
}

func writeTarFile(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o600,
		Size:    int64(len(data)),
		ModTime: time.Now().UTC(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write tar header %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("write tar body %s: %w", name, err)
	}
	return nil
}

func RestorePayload(ctx context.Context, plaintext []byte, outputPath string, store media.ReplicaStore, endpoints []media.EndpointConfig) (RestoreResult, error) {
	if !bytes.HasPrefix(plaintext, []byte(bundleMagic)) {
		if err := os.WriteFile(outputPath, plaintext, 0o600); err != nil {
			return RestoreResult{}, fmt.Errorf("write legacy database: %w", err)
		}
		return RestoreResult{
			Format:  "legacy-db",
			DBBytes: len(plaintext),
		}, nil
	}

	tr := tar.NewReader(bytes.NewReader(plaintext[len(bundleMagic):]))

	var manifest bundleManifest
	var haveManifest bool
	var dbBytes []byte
	mediaSeen := map[string]bool{}
	uploadETags := map[string]map[string]string{}
	mediaSizes := map[string]int64{}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return RestoreResult{}, fmt.Errorf("read bundle entry: %w", err)
		}

		switch hdr.Name {
		case bundleManifestPath:
			body, err := io.ReadAll(tr)
			if err != nil {
				return RestoreResult{}, fmt.Errorf("read bundle manifest: %w", err)
			}
			if err := json.Unmarshal(body, &manifest); err != nil {
				return RestoreResult{}, fmt.Errorf("parse bundle manifest: %w", err)
			}
			if manifest.Version != bundleVersion {
				return RestoreResult{}, fmt.Errorf("unsupported bundle version: %d", manifest.Version)
			}
			haveManifest = true
		case bundleDBPath:
			body, err := io.ReadAll(tr)
			if err != nil {
				return RestoreResult{}, fmt.Errorf("read bundled database: %w", err)
			}
			dbBytes = body
		default:
			if len(hdr.Name) > len(bundleMediaPrefix) && hdr.Name[:len(bundleMediaPrefix)] == bundleMediaPrefix {
				if !haveManifest {
					return RestoreResult{}, fmt.Errorf("bundle media entry %s encountered before manifest", hdr.Name)
				}
				storageKey := hdr.Name[len(bundleMediaPrefix):]
				expectedSHA, ok := manifestMediaHash(manifest, storageKey)
				if !ok {
					return RestoreResult{}, fmt.Errorf("bundle media entry %s not declared in manifest", storageKey)
				}

				body, err := io.ReadAll(tr)
				if err != nil {
					return RestoreResult{}, fmt.Errorf("read bundled media %s: %w", storageKey, err)
				}
				got := sha256.Sum256(body)
				gotHex := hex.EncodeToString(got[:])
				if expectedSHA != "" && gotHex != expectedSHA {
					return RestoreResult{}, fmt.Errorf("bundled media %s hash mismatch: got %s want %s", storageKey, gotHex, expectedSHA)
				}

				mediaSeen[storageKey] = true
				mediaSizes[storageKey] = int64(len(body))
				for _, ep := range endpoints {
					etag, err := store.Put(ctx, ep, storageKey, bytes.NewReader(body), int64(len(body)))
					if err != nil {
						return RestoreResult{}, fmt.Errorf("restore media %s to %s: %w", storageKey, ep.ID, err)
					}
					if uploadETags[storageKey] == nil {
						uploadETags[storageKey] = map[string]string{}
					}
					uploadETags[storageKey][ep.ID] = etag
				}
			}
		}
	}

	if !haveManifest {
		return RestoreResult{}, fmt.Errorf("bundle missing manifest")
	}
	if len(dbBytes) == 0 {
		return RestoreResult{}, fmt.Errorf("bundle missing database payload")
	}
	for _, item := range manifest.Media {
		if !mediaSeen[item.StorageKey] {
			return RestoreResult{}, fmt.Errorf("bundle missing media payload for %s", item.StorageKey)
		}
	}

	if err := os.WriteFile(outputPath, dbBytes, 0o600); err != nil {
		return RestoreResult{}, fmt.Errorf("write restored database: %w", err)
	}
	if err := normalizeReplicaState(outputPath, manifest.Media, endpoints, uploadETags, mediaSizes); err != nil {
		return RestoreResult{}, err
	}

	return RestoreResult{
		Format:      fmt.Sprintf("bundle-v%d", manifest.Version),
		DBBytes:     len(dbBytes),
		MediaFiles:  len(manifest.Media),
		MediaCopies: len(manifest.Media) * len(endpoints),
	}, nil
}

func manifestMediaHash(manifest bundleManifest, storageKey string) (string, bool) {
	for _, item := range manifest.Media {
		if item.StorageKey == storageKey {
			return item.CiphertextSHA256, true
		}
	}
	return "", false
}

func normalizeReplicaState(outputPath string, mediaEntries []bundleMediaEntry, endpoints []media.EndpointConfig, uploadETags map[string]map[string]string, mediaSizes map[string]int64) error {
	if len(mediaEntries) == 0 {
		return nil
	}

	db, err := sql.Open("sqlite3", outputPath)
	if err != nil {
		return fmt.Errorf("open restored database for replica sync: %w", err)
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin replica sync transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM file_s3 WHERE file_id IN (SELECT id FROM files WHERE deleted_at IS NULL)`); err != nil {
		return fmt.Errorf("clear restored replica rows: %w", err)
	}

	stmt, err := tx.Prepare(`
        INSERT INTO file_s3 (
            file_id, endpoint_id, state, remote_key, etag, ciphertext_size,
            retry_count, last_attempt_at, last_success_at, next_retry_at, updated_at
        )
        VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, NULL, ?)
    `)
	if err != nil {
		return fmt.Errorf("prepare restored replica insert: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	for _, item := range mediaEntries {
		size := mediaSizes[item.StorageKey]
		for _, ep := range endpoints {
			etag := uploadETags[item.StorageKey][ep.ID]
			if _, err := stmt.Exec(
				item.FileID,
				ep.ID,
				string(media.ReplicaStateUploaded),
				item.StorageKey,
				etag,
				size,
				now,
				now,
				now,
			); err != nil {
				return fmt.Errorf("insert restored replica row for file %d endpoint %s: %w", item.FileID, ep.ID, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit restored replica sync: %w", err)
	}
	return nil
}
