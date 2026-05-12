package media

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/i5heu/MentisEterna/internal/llm"
)

// STTResult holds the result of an STT operation on a file.
type STTResult struct {
	FileID  int64  `json:"file_id"`
	STTText string `json:"stt_text"`
	Model   string `json:"model"`
	Error   string `json:"error,omitempty"`
}

// audioMIMETypes is the set of MIME types for which STT is attempted.
var audioMIMETypes = map[string]bool{
	"audio/mpeg":  true,
	"audio/mp3":   true,
	"audio/wav":   true,
	"audio/wave":  true,
	"audio/x-wav": true,
	"audio/ogg":   true,
	"audio/mp4":   true,
	"audio/m4a":   true,
	"audio/webm":  true,
	"audio/flac":  true,
	"audio/aac":   true,
}

// IsSTTable returns true if the given MIME type is an audio file that can be transcribed.
func IsSTTable(mimeType string) bool {
	return audioMIMETypes[mimeType]
}

// RunSTTForFile runs STT on a file. It decrypts the file, sends the plaintext
// audio to the STT model, and stores the result in files_stt.
func (s *Service) RunSTTForFile(ctx context.Context, fileID int64, sttClient llm.STTer) (*STTResult, error) {
	// Load file record to check MIME type and get keys.
	rec, err := s.loadFileRecord(fileID)
	if err != nil {
		return nil, fmt.Errorf("stt: load file %d: %w", fileID, err)
	}

	model := "nemo-parakeet-tdt-0.6b"
	if sc, ok := sttClient.(*llm.STTClient); ok {
		model = sc.Model
	}

	// Check if this is an audio type that can be transcribed.
	if !IsSTTable(rec.MimeType) {
		return &STTResult{
			FileID:  fileID,
			STTText: "",
			Model:   model,
			Error:   fmt.Sprintf("unsupported MIME type for STT: %s", rec.MimeType),
		}, nil
	}

	// Decrypt file to memory.
	var plainBuf bytes.Buffer
	ctReader, cacheErr := s.Cache.Open(fileID, rec.CiphertextSHA256)
	if cacheErr == nil {
		defer ctReader.Close()
		if err := DecryptToWriter(ctReader, &plainBuf, rec.AESKey, rec.AESNonce); err != nil {
			return s.saveSTTError(fileID, model, fmt.Errorf("stt: decrypt cache: %w", err))
		}
	} else {
		// Cache miss: fetch from a replica.
		fetched := false
		for _, ep := range s.Config.Endpoints {
			body, fetchErr := s.Store.Get(ctx, ep, rec.StorageKey)
			if fetchErr != nil {
				continue
			}
			ctData, readErr := io.ReadAll(body)
			body.Close()
			if readErr != nil {
				continue
			}
			if err := DecryptToWriter(bytes.NewReader(ctData), &plainBuf, rec.AESKey, rec.AESNonce); err != nil {
				continue
			}
			// Cache for future use.
			if cacheErr := s.Cache.Put(fileID, rec.CiphertextSHA256, bytes.NewReader(ctData)); cacheErr != nil {
				log.Printf("media/stt: cache put file %d: %v", fileID, cacheErr)
			}
			fetched = true
			break
		}
		if !fetched {
			return s.saveSTTError(fileID, model, fmt.Errorf("stt: file %d unavailable from any replica", fileID))
		}
	}

	// Send to STT model.
	sttText, err := sttClient.RunSTT(plainBuf.Bytes(), rec.Filename)
	if err != nil {
		return s.saveSTTError(fileID, model, fmt.Errorf("stt: model error: %w", err))
	}

	// Store successful result.
	return s.saveSTTResult(fileID, sttText, model)
}

// saveSTTResult stores a successful STT result in the database.
func (s *Service) saveSTTResult(fileID int64, sttText, model string) (*STTResult, error) {
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	_, err := s.DB.Exec(`
		INSERT INTO files_stt (file_id, stt_text, model, error, created_at, updated_at)
		VALUES (?, ?, ?, NULL, ?, ?)
		ON CONFLICT(file_id) DO UPDATE SET
			stt_text = excluded.stt_text,
			model = excluded.model,
			error = NULL,
			updated_at = excluded.updated_at
	`, fileID, sttText, model, now, now)
	if err != nil {
		return nil, fmt.Errorf("stt: save result: %w", err)
	}

	return &STTResult{
		FileID:  fileID,
		STTText: sttText,
		Model:   model,
	}, nil
}

// saveSTTError stores an STT error in the database and returns the result.
func (s *Service) saveSTTError(fileID int64, model string, err error) (*STTResult, error) {
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	errStr := err.Error()
	_, dbErr := s.DB.Exec(`
		INSERT INTO files_stt (file_id, stt_text, model, error, created_at, updated_at)
		VALUES (?, '', ?, ?, ?, ?)
		ON CONFLICT(file_id) DO UPDATE SET
			stt_text = '',
			model = excluded.model,
			error = excluded.error,
			updated_at = excluded.updated_at
	`, fileID, model, errStr, now, now)
	if dbErr != nil {
		log.Printf("stt: failed to save error for file %d: %v", fileID, dbErr)
	}
	return &STTResult{
		FileID: fileID,
		Model:  model,
		Error:  errStr,
	}, nil
}

// GetSTTResult retrieves the STT result for a file from the database.
func (s *Service) GetSTTResult(fileID int64) (*STTResult, error) {
	var result STTResult
	var sttText, model, errStr sql.NullString
	err := s.DB.QueryRow(`
		SELECT file_id, stt_text, model, error FROM files_stt WHERE file_id = ?
	`, fileID).Scan(&result.FileID, &sttText, &model, &errStr)
	if err != nil {
		return nil, err
	}
	if sttText.Valid {
		result.STTText = sttText.String
	}
	if model.Valid {
		result.Model = model.String
	}
	if errStr.Valid {
		result.Error = errStr.String
	}
	return &result, nil
}

// GetSTTTextForNoteFiles returns the concatenated STT text for all files
// referenced by a note (both attachments and inline refs).
func (s *Service) GetSTTTextForNoteFiles(noteID int64) (string, error) {
	rows, err := s.DB.Query(`
		SELECT COALESCE(fs.stt_text, '')
		FROM files_stt fs
		JOIN files_refs fr ON fr.file_id = fs.file_id
		WHERE fr.note_id = ? AND fs.error IS NULL AND fs.stt_text != ''
	`, noteID)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return "", err
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, "\n"), nil
}

// EnqueueSTT enqueues an STT job for a given file ID, if it's an audio file.
func (s *Service) EnqueueSTT(fileID int64) {
	if s.EnqueueFunc == nil {
		return
	}
	payload := []byte(fmt.Sprintf(`{"file_id":%d}`, fileID))
	if _, err := s.EnqueueFunc("_media", "stt_file", payload); err != nil {
		log.Printf("media/stt: enqueue STT for file %d: %v", fileID, err)
	}
}
