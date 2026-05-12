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

	// Register note type plugins via their init() functions.
	_ "github.com/i5heu/MentisEterna/pkg/notetype/example"
	_ "github.com/i5heu/MentisEterna/pkg/notetype/index"
	_ "github.com/i5heu/MentisEterna/pkg/notetype/recipe"
	_ "github.com/i5heu/MentisEterna/pkg/notetype/recipeoverview"
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
	chatClient := llm.NewChatClient()
	ocrClient := llm.NewOCRClient()
	sttClient := llm.NewSTTClient()
	if err := server.New(database, envOr("ADDR", ":8080"), embeddingClient, chatClient, ocrClient, sttClient).Start(ctx); err != nil {
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
