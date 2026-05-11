package media

import "time"

// ReplicaState represents the state of a file replica on an S3 endpoint.
type ReplicaState string

const (
	ReplicaStateUploading    ReplicaState = "uploading"
	ReplicaStateUploaded     ReplicaState = "uploaded"
	ReplicaStateUploadFailed ReplicaState = "upload_failed"
	ReplicaStateDeleting     ReplicaState = "deleting"
	ReplicaStateDeleted      ReplicaState = "deleted"
	ReplicaStateDeleteFailed ReplicaState = "delete_failed"
)

// RefKind represents the kind of reference from a note to a file.
type RefKind string

const (
	RefKindAttachment RefKind = "attachment"
	RefKindInline     RefKind = "inline"
)

// FileRecord holds metadata for a stored file.
type FileRecord struct {
	ID                  int64      `json:"id"`
	OriginalNoteID      *int64     `json:"original_note_id,omitempty"`
	PendingInlineNoteID *int64     `json:"pending_inline_note_id,omitempty"`
	PendingInlineAt     *time.Time `json:"pending_inline_at,omitempty"`
	StorageKey          string     `json:"storage_key"`
	Filename            string     `json:"filename"`
	MimeType            string     `json:"mime_type"`
	SizeBytes           int64      `json:"size_bytes"`
	PlaintextSHA256     string     `json:"plaintext_sha256,omitempty"`
	CiphertextSHA256    string     `json:"ciphertext_sha256"`
	AESKey              []byte     `json:"-"`
	AESNonce            []byte     `json:"-"`
	CreatedAt           time.Time  `json:"created_at"`
	DeletedAt           *time.Time `json:"deleted_at,omitempty"`
}

// NoteFile is a lightweight view of a file for note JSON responses.
type NoteFile struct {
	ID        int64  `json:"id"`
	Filename  string `json:"filename"`
	MimeType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	URL       string `json:"url"`
	IsImage   bool   `json:"is_image"`
}

// S3ReplicaRecord holds the state of a single S3 replica.
type S3ReplicaRecord struct {
	FileID         int64        `json:"file_id"`
	EndpointID     string       `json:"endpoint_id"`
	State          ReplicaState `json:"state"`
	RemoteKey      string       `json:"remote_key"`
	ETag           string       `json:"etag,omitempty"`
	CiphertextSize int64        `json:"ciphertext_size,omitempty"`
	LastError      string       `json:"last_error,omitempty"`
	RetryCount     int          `json:"retry_count"`
	LastAttemptAt  *time.Time   `json:"last_attempt_at,omitempty"`
	LastSuccessAt  *time.Time   `json:"last_success_at,omitempty"`
	NextRetryAt    *time.Time   `json:"next_retry_at,omitempty"`
}

// ReplicaResult is returned from a single replica upload/delete operation.
type ReplicaResult struct {
	EndpointID string
	State      ReplicaState
	ETag       string
	Error      string
}

// IsImage checks if the MIME type represents an image.
func IsImage(mimeType string) bool {
	switch mimeType {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/svg+xml", "image/bmp", "image/tiff":
		return true
	default:
		return false
	}
}
