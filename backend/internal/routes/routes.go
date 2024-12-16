package routes

import (
	"backend/internal/handlers"
	"net/http"

	"github.com/gorilla/mux"
)

func SetupRoutes() *mux.Router {
	router := mux.NewRouter()

	router.HandleFunc("/hello", handlers.HelloHandler).Methods(http.MethodGet)

	return router
}
