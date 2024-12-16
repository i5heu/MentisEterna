package main

import (
	"backend/internal/setup"
	"backend/pkg/config"
	"backend/pkg/server"
	"log"
)

func main() {
	cfg, err := config.LoadConfig("../config.yaml")
	if err != nil {
		log.Fatalf("Fehler beim Laden der Konfiguration: %v", err)
	}

	// Start the database
	db, err := setup.StartDB()
	if err != nil {
		log.Fatalf("Fehler beim Starten der Datenbank: %v", err)
	}
	defer db.Close()

	srv := server.NewServer(cfg.Server.Address)
	if err := srv.Start(); err != nil {
		log.Fatalf("Fehler beim Starten des Servers: %v", err)
	}
}
