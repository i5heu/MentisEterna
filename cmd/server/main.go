package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/internal/server"
)

func main() {
	database, err := db.Open(envOr("DB_PATH", "mentis.db"))
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer database.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	embeddingClient := llm.NewEmbeddingClient()
	if err := server.New(database, envOr("ADDR", ":8080"), embeddingClient).Start(ctx); err != nil {
		log.Fatalf("server: %v", err)
	}
	log.Println("server stopped, database closed")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
