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
)

// Usage: go run ./cmd/backfill/ [--db mentis.db] [--batch 5] [--sleep 500ms]
func main() {
	dbPath := flag.String("db", "mentis.db", "path to the SQLite database")
	batchSize := flag.Int("batch", 5, "how many embeddings to generate before sleeping")
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

	// Find all notes that have no embedding yet.
	rows, err := database.Query(`
		SELECT n.id, n.title, COALESCE(u.body, '') AS body
		FROM notes n
		LEFT JOIN updates u ON u.id = (
			SELECT id FROM updates WHERE note_id = n.id ORDER BY id DESC LIMIT 1
		)
		WHERE n.id NOT IN (SELECT rowid FROM vss_notes)
		ORDER BY n.id ASC
	`)
	if err != nil {
		log.Fatalf("query notes: %v", err)
	}
	defer rows.Close()

	type pending struct {
		id    int64
		title string
		body  string
	}

	var all []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.title, &p.body); err != nil {
			log.Fatalf("scan note: %v", err)
		}
		all = append(all, p)
	}

	if len(all) == 0 {
		fmt.Println("All notes already have embeddings. Nothing to do.")
		return
	}

	fmt.Printf("Generating embeddings for %d notes (batch size %d, sleep %s)...\n",
		len(all), *batchSize, *sleepDur)

	for i, p := range all {
		text := llm.CombineTitleBody(p.title, p.body)
		text = llm.TruncateForEmbedding(text)
		vec, err := client.GenerateEmbedding(text)
		if err != nil {
			log.Printf("ERROR note %d: %v", p.id, err)
			continue
		}
		vecJSON := llm.EmbeddingToJSON(vec)
		_, err = database.Exec(
			`INSERT OR REPLACE INTO vss_notes(rowid, body_embedding) VALUES (?, ?)`,
			p.id, vecJSON,
		)
		if err != nil {
			log.Printf("ERROR upsert note %d: %v", p.id, err)
			continue
		}
		fmt.Printf("[%d/%d] Embedded note %d: %q\n", i+1, len(all), p.id, p.title)

		// Batch throttle
		if (i+1)%*batchSize == 0 && i+1 < len(all) {
			time.Sleep(*sleepDur)
		}
	}

	fmt.Println("Backfill complete.")
}
