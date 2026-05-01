package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"vigil/internal/config"
)

func NewHandler(cfg config.Config) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{
			"status":   "ok",
			"app":      "vigil",
			"data_dir": cfg.DataDir,
		})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		distIndex := filepath.Join("web", "dist", "index.html")
		if _, err := os.Stat(distIndex); err == nil {
			http.FileServer(http.Dir("web/dist")).ServeHTTP(w, r)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{
			"message": "Vigil backend is running. Build the UI with `make ui-build` to serve the frontend.",
		})
	})

	return mux
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
