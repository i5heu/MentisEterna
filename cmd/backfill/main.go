package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/internal/searchindex"
)

// Usage: go run ./cmd/backfill/ [--db mentis.db] [--batch 5] [--sleep 500ms]
func main() {
	dbPath := flag.String("db", "mentis.db", "path to the SQLite database")
	batchSize := flag.Int("batch", 5, "how many notes to index before sleeping")
	sleepDur := flag.Duration("sleep", 500*time.Millisecond, "sleep duration between batches")
	flag.Parse()

	database, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer database.Close()

	if !database.VSSAvailable() {
		fmt.Println("sqlite-vec is not available, so vector indexing cannot run.")
		os.Exit(1)
	}

	client := llm.NewEmbeddingClient()

	rows, err := database.Query(`
		SELECT n.id
		FROM notes n
		WHERE NOT EXISTS (
			SELECT 1 FROM note_search_chunks c WHERE c.note_id = n.id
		)
		ORDER BY n.id ASC
	`)
	if err != nil {
		log.Fatalf("query notes: %v", err)
	}
	defer rows.Close()

	var noteIDs []int64
	for rows.Next() {
		var noteID int64
		if err := rows.Scan(&noteID); err != nil {
			log.Fatalf("scan note id: %v", err)
		}
		noteIDs = append(noteIDs, noteID)
	}
	if err := rows.Err(); err != nil {
		log.Fatalf("iterate note ids: %v", err)
	}

	if len(noteIDs) == 0 {
		fmt.Println("All notes already have search embeddings. Nothing to do.")
		return
	}

	fmt.Printf("Generating search embeddings for %d notes (batch size %d, sleep %s)...\n",
		len(noteIDs), *batchSize, *sleepDur)

	for i, noteID := range noteIDs {
		doc, err := searchindex.LoadDocument(database.DB, noteID)
		if err != nil {
			log.Printf("ERROR load note %d: %v", noteID, err)
			continue
		}
		chunkCount, err := searchindex.ReplaceNoteIndex(database.DB, client, doc)
		if err != nil {
			log.Printf("ERROR reindex note %d: %v", noteID, err)
			continue
		}
		_, _ = database.Exec(`DELETE FROM vss_notes WHERE rowid = ?`, noteID)
		fmt.Printf("[%d/%d] Indexed note %d: %q (%d search chunks)\n", i+1, len(noteIDs), noteID, doc.Title, chunkCount)

		if (i+1)%*batchSize == 0 && i+1 < len(noteIDs) {
			time.Sleep(*sleepDur)
		}
	}

	fmt.Println("Backfill complete.")
}
