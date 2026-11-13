package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
)

//go:embed web
var webRoot embed.FS

func main() {
	store, err := Open("hold.db")
	if err != nil {
		log.Fatal(err)
	}
	live := NewLive(store)
	go live.Watch(context.Background())
	log.Print("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", routes(store, live)))
}

func routes(s *Store, live *Live) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /api/live", live)
	mux.HandleFunc("GET /api/catalog", func(w http.ResponseWriter, r *http.Request) {
		cat, err := s.Catalog()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, cat)
	})
	mux.HandleFunc("POST /api/holds", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ProductID string `json:"product_id"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ProductID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
			return
		}
		h, err := s.Hold(body.ProductID)
		if err != nil {
			writeErr(w, err)
			return
		}
		live.Touch()
		writeJSON(w, http.StatusCreated, h)
	})
	mux.HandleFunc("GET /api/holds/{id}", func(w http.ResponseWriter, r *http.Request) {
		h, err := s.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h)
	})
	mux.HandleFunc("POST /api/holds/{id}/confirm", func(w http.ResponseWriter, r *http.Request) {
		h, err := s.Confirm(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		live.Touch()
		writeJSON(w, http.StatusOK, h)
	})
	mux.HandleFunc("POST /api/holds/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		h, err := s.Cancel(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		live.Touch()
		writeJSON(w, http.StatusOK, h)
	})
	mux.HandleFunc("POST /api/race", func(w http.ResponseWriter, r *http.Request) {
		h, err := s.Race()
		if err != nil {
			writeErr(w, err)
			return
		}
		live.Announce("Two hands reached for Ash bottles. One was too late.")
		writeJSON(w, http.StatusCreated, map[string]any{"winner": h, "beaten": 1})
	})
	mux.HandleFunc("POST /api/reset", func(w http.ResponseWriter, r *http.Request) {
		cat, err := s.Reset()
		if err != nil {
			writeErr(w, err)
			return
		}
		live.Touch()
		writeJSON(w, http.StatusOK, cat)
	})
	sub, err := fs.Sub(webRoot, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	case errors.Is(err, ErrGone):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "gone"})
	default:
		log.Print(err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "error"})
	}
}
