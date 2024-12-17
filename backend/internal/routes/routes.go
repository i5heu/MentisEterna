package routes

import (
	"backend/internal/handlers"

	"github.com/gorilla/mux"
)

func SetupRoutes(h *handlers.Handler) *mux.Router {
	router := mux.NewRouter()
	router.HandleFunc("/note", h.StoreNoteHandler).Methods("POST")
	router.HandleFunc("/note", h.GetNoteHandler).Methods("GET")
	return router
}
