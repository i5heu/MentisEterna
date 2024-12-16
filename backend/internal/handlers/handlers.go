package handlers

import (
	"net/http"
)

// HelloHandler ist ein Beispiel-Handler, der eine einfache Antwort zurückgibt.
func HelloHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Hello, World!"))
}

// Weitere Handler können hier hinzugefügt werden.
