package main

import (
	"backend/internal/handlers"
	"backend/internal/routes"
	"backend/internal/setup"
	"backend/pkg/config"
	"backend/pkg/server"
	"log"
	"path/filepath"
)

func main() {

	absPath, err := filepath.Abs("../TestData/")
	if err != nil {
		log.Fatalf("Error creating absolute path: %v", err)
	}

	cfg, err := config.LoadConfig("../config.yaml")
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}

	// Start the database
	db, err := setup.StartDB(absPath)
	if err != nil {
		log.Fatalf("Error starting the database: %v", err)
	}
	defer db.Close()

	srv := server.NewServer(cfg.Server.Address)

	// Create handler with db
	handler := &handlers.Handler{DB: db}

	// Setup routes with handler
	router := routes.SetupRoutes(handler)
	srv.SetHandler(router)

	if err := srv.Start(); err != nil {
		log.Fatalf("Error starting the server: %v", err)
	}
}
