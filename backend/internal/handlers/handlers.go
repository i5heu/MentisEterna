package handlers

import (
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/i5heu/ouroboros-db"
	"github.com/i5heu/ouroboros-db/pkg/storage"
	"github.com/i5heu/ouroboros-db/pkg/types"
)

type Handler struct {
	DB *ouroboros.OuroborosDB
}

// addCORS adds the necessary CORS headers to the response
func addCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "http://localhost:9000")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func (h *Handler) StoreNoteHandler(w http.ResponseWriter, r *http.Request) {
	addCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Read note from request body
	note, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Unable to read note", http.StatusBadRequest)
		return
	}
	// Create a root event titled "Notes" if it doesn't exist
	rootEvents, err := h.DB.DB.GetRootEventsWithTitle("Notes")
	var rootEvent types.Event
	if err != nil || len(rootEvents) == 0 {
		rootEvent, err = h.DB.DB.CreateRootEvent("Notes")
		if err != nil {
			http.Error(w, "Failed to create root event", http.StatusInternalServerError)
			return
		}
	} else {
		rootEvent = rootEvents[0]
	}

	fmt.Println("note:", string(note), rootEvent.EventHash)

	// Store the note
	_, err = h.DB.DB.StoreFile(storage.StoreFileOptions{
		EventToAppendTo: rootEvent,
		FastMeta:        nil,
		Metadata:        note,
		File:            note,
	})
	if err != nil {
		http.Error(w, "Failed to store note", http.StatusInternalServerError)
		return
	}

	h.DB.Index.RebuildIndex()
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) GetNoteHandler(w http.ResponseWriter, r *http.Request) {
	addCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Retrieve the "Notes" root event
	rootEvents, err := h.DB.DB.GetRootEventsWithTitle("Notes")
	if err != nil || len(rootEvents) == 0 {
		http.Error(w, "No notes found", http.StatusNotFound)
		return
	}
	rootEvent := rootEvents[0]
	// Get all child events under "Notes"
	childEvents, err := h.DB.Index.GetDirectChildrenOfEvent(rootEvent.EventHash)
	if err != nil || len(childEvents) == 0 {
		http.Error(w, "No notes found", http.StatusNotFound)
		return
	}
	// Retrieve notes from child events
	for _, event := range childEvents {
		note, err := h.DB.DB.GetFile(event)
		if err != nil {
			continue
		}
		w.Write(note)
		w.Write([]byte("\n"))
	}
}

func HelloHandler(w http.ResponseWriter, r *http.Request) {
	addCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Hello, World!"))
}
