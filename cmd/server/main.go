package main

import (
	"log"
	"os"

	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/internal/server"
)

func main() {
	database, err := db.Open(envOr("DB_PATH", "mentis.db"))
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer database.Close()

	if err := server.New(database, envOr("ADDR", ":8080")).Start(); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
