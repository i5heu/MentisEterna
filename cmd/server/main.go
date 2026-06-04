package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/internal/server"

	// Register note type plugins via their init() functions.
	_ "github.com/i5heu/MentisEterna/pkg/notetype/builtins"
)

func main() {
	createDB := flag.Bool("create-db", false, "create the SQLite database if it does not exist")
	flag.Parse()

	dbPath := envOr("DB_PATH", "mentis.db")
	if err := requireExistingDBUnlessCreate(dbPath, *createDB); err != nil {
		log.Fatal(err)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer database.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	embeddingClient := llm.NewEmbeddingClient()
	chatClient := llm.NewChatClient()
	ocrClient := llm.NewOCRClient()
	sttClient := llm.NewSTTClient()
	if err := server.New(database, envOr("ADDR", ":8080"), embeddingClient, chatClient, ocrClient, sttClient).Start(ctx); err != nil {
		log.Fatalf("server: %v", err)
	}
	log.Println("server stopped, database closed")
}

func requireExistingDBUnlessCreate(dbPath string, createDB bool) error {
	if createDB || isInMemoryDBPath(dbPath) {
		return nil
	}

	info, err := os.Stat(dbPath)
	if err == nil {
		if info.IsDir() {
			return fmt.Errorf("database path %q is a directory", dbPath)
		}
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("database %q does not exist; start the server with --create-db to create it", dbPath)
	}
	return fmt.Errorf("stat db %q: %w", dbPath, err)
}

func isInMemoryDBPath(dbPath string) bool {
	trimmed := strings.TrimSpace(dbPath)
	if trimmed == ":memory:" {
		return true
	}
	return strings.HasPrefix(trimmed, "file:") && strings.Contains(trimmed, "mode=memory")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
