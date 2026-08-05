// restore is a standalone CLI tool to download and decrypt a backup.
//
// Usage:
//
//	restore [-config config.toml] <s3-backup-key> <output.db>
//
// Configuration:
//
//	config.toml                  media.endpoints (endpoint definitions)
//	BACKUP_ENCRYPTION_KEY        hex-encoded 64-character AES-256 key (required)
//	MEDIA_S3_<ID>_ACCESS_KEY_ID  per-endpoint access key (required)
//	MEDIA_S3_<ID>_SECRET_ACCESS_KEY  per-endpoint secret key (required)
//
// Example:
//
//	restore backups/mentis-2026-05-12T03-00-00.bundle.enc mentis_restored.db
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/i5heu/MentisEterna/internal/backup"
	"github.com/i5heu/MentisEterna/internal/config"
	"github.com/i5heu/MentisEterna/internal/media"
)

func main() {
	configPath := flag.String("config", "config.toml", "path to TOML config file; if absent, defaults are used")
	flag.Parse()
	if flag.NArg() < 2 {
		fmt.Fprintf(os.Stderr, "Usage: restore [-config config.toml] <s3-backup-key> <output.db>\n")
		fmt.Fprintf(os.Stderr, "\nConfiguration:\n")
		fmt.Fprintf(os.Stderr, "  config.toml                    media.endpoints (endpoint definitions)\n")
		fmt.Fprintf(os.Stderr, "  BACKUP_ENCRYPTION_KEY          hex-encoded 64-char AES-256 key\n")
		fmt.Fprintf(os.Stderr, "  MEDIA_S3_<ID>_ACCESS_KEY_ID    per-endpoint access key\n")
		fmt.Fprintf(os.Stderr, "  MEDIA_S3_<ID>_SECRET_ACCESS_KEY per-endpoint secret key\n")
		os.Exit(1)
	}
	remoteKey := flag.Arg(0)
	outputPath := flag.Arg(1)

	if fi, err := os.Stat(*configPath); err == nil && fi != nil {
		if err := config.Load(*configPath); err != nil {
			log.Fatalf("config: %v", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Fatalf("config: stat %s: %v", *configPath, err)
	} else {
		log.Printf("config: %s not found; using defaults (copy config.default.toml to customize)", *configPath)
	}

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

	// Load S3 endpoint configuration (definitions from config.toml, keys from env).
	endpoints, err := media.BuildEndpoints()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: loading S3 config: %v\n", err)
		fmt.Fprintf(os.Stderr, "Set media.endpoints in config.toml and the matching MEDIA_S3_<ID>_* key env vars.\n")
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
