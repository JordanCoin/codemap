package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codemap/skills"
	"codemap/watch"
)

// RunServe starts a lightweight HTTP server exposing codemap's intelligence.
func RunServe(args []string, root string) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := fs.Int("port", 9471, "Port to listen on")
	host := fs.String("host", "127.0.0.1", "Host to bind to (default: localhost only)")
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if fs.NArg() > 0 {
		root = fs.Arg(0)
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()

	// GET /api/context — full context envelope
	mux.HandleFunc("/api/context", func(w http.ResponseWriter, r *http.Request) {
		prompt := r.URL.Query().Get("intent")
		compact := r.URL.Query().Get("compact") == "true"
		envelope := buildContextEnvelope(absRoot, prompt, compact)
		writeJSON(w, envelope)
	})

	// GET /api/skills — list available skills
	mux.HandleFunc("/api/skills", func(w http.ResponseWriter, r *http.Request) {
		idx, err := skills.LoadSkills(absRoot)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Optional filters
		lang := r.URL.Query().Get("language")
		category := r.URL.Query().Get("category")

		if category != "" || lang != "" {
			var langs []string
			if lang != "" {
				langs = []string{lang}
			}
			matches := idx.MatchSkills(category, nil, langs, 10)
			writeJSON(w, matches)
			return
		}

		// Return all skill metadata (no bodies)
		type skillMeta struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Source      string   `json:"source"`
			Keywords    []string `json:"keywords,omitempty"`
			Languages   []string `json:"languages,omitempty"`
			Priority    int      `json:"priority,omitempty"`
		}
		var meta []skillMeta
		for _, s := range idx.Skills {
			meta = append(meta, skillMeta{
				Name:        s.Meta.Name,
				Description: s.Meta.Description,
				Source:      s.Source,
				Keywords:    s.Meta.Keywords,
				Languages:   s.Meta.Languages,
				Priority:    s.Meta.Priority,
			})
		}
		writeJSON(w, meta)
	})

	// GET /api/skills/:name — get full skill
	mux.HandleFunc("/api/skills/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/api/skills/")
		if name == "" {
			writeError(w, http.StatusBadRequest, "skill name required")
			return
		}

		idx, err := skills.LoadSkills(absRoot)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		skill, ok := idx.ByName[name]
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("skill %q not found", name))
			return
		}
		writeJSON(w, skill)
	})

	// GET /api/working-set — current session working set
	mux.HandleFunc("/api/working-set", func(w http.ResponseWriter, r *http.Request) {
		state := watch.ReadState(absRoot)
		if state == nil || state.WorkingSet == nil {
			writeJSON(w, map[string]string{"status": "no working set available"})
			return
		}
		writeJSON(w, state.WorkingSet)
	})

	// GET /api/health — simple health check
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})

	addr := fmt.Sprintf("%s:%d", *host, *port)
	fmt.Printf("codemap serve — listening on http://%s\n", addr)
	fmt.Printf("  GET /api/context?intent=refactor+auth&compact=true\n")
	fmt.Printf("  GET /api/skills?language=go&category=refactor\n")
	fmt.Printf("  GET /api/skills/<name>\n")
	fmt.Printf("  GET /api/working-set\n")
	fmt.Printf("  GET /api/health\n")
	fmt.Println()

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
