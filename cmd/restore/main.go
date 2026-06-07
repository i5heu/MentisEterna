// restore is a standalone CLI tool to download and decrypt a backup.
//
// Usage:
//
//	restore <s3-backup-key> <output.db>
//
// Environment:
//
//	BACKUP_ENCRYPTION_KEY   hex-encoded 64-character AES-256 key (required)
//	MEDIA_S3_ENDPOINTS      JSON array of S3 endpoint configs (required)
//
// Example:
//
//	restore backups/mentis-2026-05-12T03-00-00.bundle.enc mentis_restored.db
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/i5heu/MentisEterna/internal/backup"
	"github.com/i5heu/MentisEterna/internal/media"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: restore <s3-backup-key> <output.db>\n")
		fmt.Fprintf(os.Stderr, "\nEnvironment:\n")
		fmt.Fprintf(os.Stderr, "  BACKUP_ENCRYPTION_KEY   hex-encoded 64-char AES-256 key\n")
		fmt.Fprintf(os.Stderr, "  MEDIA_S3_ENDPOINTS      JSON array of S3 endpoint configs\n")
		os.Exit(1)
	}
	remoteKey := os.Args[1]
	outputPath := os.Args[2]

	// Load encryption key.
	hexKey := os.Getenv("BACKUP_ENCRYPTION_KEY")
	if hexKey == "" {
		fmt.Fprintf(os.Stderr, "Error: BACKUP_ENCRYPTION_KEY environment variable is not set\n")
		os.Exit(1)
	}
	key, err := backup.KeyFromHex(hexKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid BACKUP_ENCRYPTION_KEY: %v\n", err)
		os.Exit(1)
	}

	// Load S3 endpoint configuration.
	endpoints, err := media.LoadEndpointsFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: loading S3 config: %v\n", err)
		fmt.Fprintf(os.Stderr, "Set MEDIA_S3_ENDPOINTS to a JSON array of endpoint configs.\n")
		os.Exit(1)
	}

	store := media.NewS3Store()
	ctx := context.Background()

	// Try each configured endpoint until one succeeds.
	for _, ep := range endpoints {
		fmt.Printf("Trying endpoint %s...\n", ep.ID)

		rc, err := store.Get(ctx, ep, remoteKey)
		if err != nil {
			fmt.Printf("  %s: %v\n", ep.ID, err)
			continue
		}

		encrypted, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			fmt.Printf("  read error: %v\n", err)
			continue
		}
		fmt.Printf("  downloaded %d bytes\n", len(encrypted))

		// Decrypt.
		plaintext, err := backup.Decrypt(encrypted, key)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: decrypt failed: %v\n", err)
			fmt.Fprintf(os.Stderr, "The encryption key may be wrong or the backup may be corrupted.\n")
			os.Exit(1)
		}

		result, err := backup.RestorePayload(ctx, plaintext, outputPath, store, endpoints)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: restore failed: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Successfully restored %s (%d DB bytes, %d media file(s), %d media upload(s)) to %s\n",
			result.Format, result.DBBytes, result.MediaFiles, result.MediaCopies, outputPath)
		return
	}

	fmt.Fprintf(os.Stderr, "Error: failed to download backup from any configured S3 endpoint\n")
	os.Exit(1)
}
