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

// OCRResult holds the result of an OCR operation on a file.
type OCRResult struct {
	FileID  int64  `json:"file_id"`
	OCRText string `json:"ocr_text"`
	Model   string `json:"model"`
	Error   string `json:"error,omitempty"`
}

// imageMIMETypes is the set of MIME types for which OCR is attempted.
var imageMIMETypes = map[string]bool{
	"image/jpeg":    true,
	"image/png":     true,
	"image/gif":     true,
	"image/webp":    true,
	"image/bmp":     true,
	"image/tiff":    true,
	"image/svg+xml": true,
}

// IsOCRable returns true if the given MIME type is an image that can be OCR'd.
func IsOCRable(mimeType string) bool {
	return imageMIMETypes[mimeType]
}

// RunOCRForFile runs OCR on a file. It decrypts the file, sends the plaintext
// to the OCR model, and stores the result in files_ocr.
func (s *Service) RunOCRForFile(ctx context.Context, fileID int64, ocrClient llm.OCRer) (*OCRResult, error) {
	// Load file record to check MIME type and get keys.
	rec, err := s.loadFileRecord(fileID)
	if err != nil {
		return nil, fmt.Errorf("ocr: load file %d: %w", fileID, err)
	}

	model := "glm-ocr:latest"
	if oc, ok := ocrClient.(*llm.OCRClient); ok {
		model = oc.Model
	}

	// Check if this is an image type that can be OCR'd
	if !IsOCRable(rec.MimeType) {
		return &OCRResult{
			FileID:  fileID,
			OCRText: "",
			Model:   model,
			Error:   fmt.Sprintf("unsupported MIME type for OCR: %s", rec.MimeType),
		}, nil
	}

	// Decrypt file to memory
	var plainBuf bytes.Buffer
	ctReader, cacheErr := s.Cache.Open(fileID, rec.CiphertextSHA256)
	if cacheErr == nil {
		defer ctReader.Close()
		if err := DecryptToWriter(ctReader, &plainBuf, rec.AESKey, rec.AESNonce); err != nil {
			return s.saveOCRError(fileID, model, fmt.Errorf("ocr: decrypt cache: %w", err))
		}
	} else {
		// Cache miss: fetch from a replica
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
			// Cache for future use
			if cacheErr := s.Cache.Put(fileID, rec.CiphertextSHA256, bytes.NewReader(ctData)); cacheErr != nil {
				log.Printf("media/ocr: cache put file %d: %v", fileID, cacheErr)
			}
			fetched = true
			break
		}
		if !fetched {
			return s.saveOCRError(fileID, model, fmt.Errorf("ocr: file %d unavailable from any replica", fileID))
		}
	}

	// Send to OCR model
	ocrText, err := ocrClient.RunOCR(plainBuf.Bytes())
	if err != nil {
		return s.saveOCRError(fileID, model, fmt.Errorf("ocr: model error: %w", err))
	}

	// Store successful result
	return s.saveOCRResult(fileID, ocrText, model)
}

// saveOCRResult stores a successful OCR result in the database.
func (s *Service) saveOCRResult(fileID int64, ocrText, model string) (*OCRResult, error) {
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	// Use INSERT OR REPLACE to handle re-OCR scenarios
	_, err := s.DB.Exec(`
		INSERT INTO files_ocr (file_id, ocr_text, model, error, created_at, updated_at)
		VALUES (?, ?, ?, NULL, ?, ?)
		ON CONFLICT(file_id) DO UPDATE SET
			ocr_text = excluded.ocr_text,
			model = excluded.model,
			error = NULL,
			updated_at = excluded.updated_at
	`, fileID, ocrText, model, now, now)
	if err != nil {
		return nil, fmt.Errorf("ocr: save result: %w", err)
	}

	return &OCRResult{
		FileID:  fileID,
		OCRText: ocrText,
		Model:   model,
	}, nil
}

// saveOCRError stores an OCR error in the database and returns the result.
func (s *Service) saveOCRError(fileID int64, model string, err error) (*OCRResult, error) {
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	errStr := err.Error()
	_, dbErr := s.DB.Exec(`
		INSERT INTO files_ocr (file_id, ocr_text, model, error, created_at, updated_at)
		VALUES (?, '', ?, ?, ?, ?)
		ON CONFLICT(file_id) DO UPDATE SET
			ocr_text = '',
			model = excluded.model,
			error = excluded.error,
			updated_at = excluded.updated_at
	`, fileID, model, errStr, now, now)
	if dbErr != nil {
		log.Printf("ocr: failed to save error for file %d: %v", fileID, dbErr)
	}
	return &OCRResult{
		FileID: fileID,
		Model:  model,
		Error:  errStr,
	}, nil
}

// GetOCRResult retrieves the OCR result for a file from the database.
func (s *Service) GetOCRResult(fileID int64) (*OCRResult, error) {
	var result OCRResult
	var ocrText, model, errStr sql.NullString
	err := s.DB.QueryRow(`
		SELECT file_id, ocr_text, model, error FROM files_ocr WHERE file_id = ?
	`, fileID).Scan(&result.FileID, &ocrText, &model, &errStr)
	if err != nil {
		return nil, err
	}
	if ocrText.Valid {
		result.OCRText = ocrText.String
	}
	if model.Valid {
		result.Model = model.String
	}
	if errStr.Valid {
		result.Error = errStr.String
	}
	return &result, nil
}

// GetOCRTextForNoteFiles returns the concatenated OCR text for all files
// referenced by a note (both attachments and inline refs).
func (s *Service) GetOCRTextForNoteFiles(noteID int64) (string, error) {
	rows, err := s.DB.Query(`
		SELECT COALESCE(fo.ocr_text, '')
		FROM files_ocr fo
		JOIN files_refs fr ON fr.file_id = fo.file_id
		WHERE fr.note_id = ? AND fo.error IS NULL AND fo.ocr_text != ''
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

// FindNoteIDsByFileID returns all note IDs that reference the given file.
func (s *Service) FindNoteIDsByFileID(fileID int64) ([]int64, error) {
	rows, err := s.DB.Query(`
		SELECT note_id FROM files_refs WHERE file_id = ?
		UNION
		SELECT original_note_id FROM files WHERE id = ? AND original_note_id IS NOT NULL
	`, fileID, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// EnqueueOCR enqueues an OCR job for a given file ID, if it's an image.
func (s *Service) EnqueueOCR(fileID int64) {
	if s.EnqueueFunc == nil {
		return
	}
	payload := []byte(fmt.Sprintf(`{"file_id":%d}`, fileID))
	if _, err := s.EnqueueFunc("_media", "ocr_file", payload); err != nil {
		log.Printf("media/ocr: enqueue OCR for file %d: %v", fileID, err)
	}
}
