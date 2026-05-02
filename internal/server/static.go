package server

import (
	"net/http"
	"os"
	"path/filepath"
)

type spaHandler struct {
	root string
	fs   http.Handler
}

func newSPAHandler(root string) http.Handler {
	return &spaHandler{
		root: root,
		fs:   http.FileServer(http.Dir(root)),
	}
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(h.root, filepath.Clean("/"+r.URL.Path))
	if _, err := os.Stat(path); os.IsNotExist(err) {
		http.ServeFile(w, r, filepath.Join(h.root, "index.html"))
		return
	}
	h.fs.ServeHTTP(w, r)
}
