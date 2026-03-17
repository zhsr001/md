package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed assets
var assets embed.FS

var dataDir string

func main() {
	dataDir = os.Getenv("MD_DATA_DIR")
	if dataDir == "" {
		dataDir = "/data"
	}

	// Ensure data directories exist
	for _, sub := range []string{"storage", "upload"} {
		dir := filepath.Join(dataDir, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("Failed to create directory %s: %v", dir, err)
		}
	}

	mux := http.NewServeMux()

	// Storage API
	mux.HandleFunc("/api/storage/keys", handleStorageKeys)
	mux.HandleFunc("/api/storage/", handleStorage)
	mux.HandleFunc("/api/storage", handleStorageBulk)

	// Upload API
	mux.HandleFunc("/api/upload", handleUpload)

	// Serve uploaded files
	uploadDir := filepath.Join(dataDir, "upload")
	mux.Handle("/upload/", http.StripPrefix("/upload/", http.FileServer(http.Dir(uploadDir))))

	// Health check — also used by frontend to detect server mode
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Serve frontend assets
	md, _ := fs.Sub(assets, "assets")
	mux.Handle("/", http.FileServer(http.FS(md)))

	log.Printf("Server starting on :80, data dir: %s", dataDir)
	if err := http.ListenAndServe(":80", mux); err != nil {
		log.Fatal(err)
	}
}

// --- Storage Handlers ---

func storageDir() string {
	return filepath.Join(dataDir, "storage")
}

func keyToFile(key string) string {
	safe := strings.NewReplacer(
		"/", "__SLASH__",
		"\\", "__BSLASH__",
		":", "__COLON__",
		"*", "__STAR__",
		"?", "__QMARK__",
		"\"", "__DQUOTE__",
		"<", "__LT__",
		">", "__GT__",
		"|", "__PIPE__",
	).Replace(key)
	return filepath.Join(storageDir(), safe+".json")
}

func handleStorageKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	entries, err := os.ReadDir(storageDir())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string][]string{"keys": {}})
		return
	}

	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		key := strings.NewReplacer(
			"__SLASH__", "/",
			"__BSLASH__", "\\",
			"__COLON__", ":",
			"__STAR__", "*",
			"__QMARK__", "?",
			"__DQUOTE__", "\"",
			"__LT__", "<",
			"__GT__", ">",
			"__PIPE__", "|",
		).Replace(name)
		keys = append(keys, key)
	}

	writeJSON(w, http.StatusOK, map[string][]string{"keys": keys})
}

func handleStorage(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/api/storage/")
	if key == "" {
		http.Error(w, "Key required", http.StatusBadRequest)
		return
	}

	filePath := keyToFile(key)

	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, http.StatusOK, map[string]any{"value": nil})
				return
			}
			http.Error(w, "Read error", http.StatusInternalServerError)
			return
		}
		var stored map[string]any
		if err := json.Unmarshal(data, &stored); err != nil {
			http.Error(w, "Parse error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, stored)

	case http.MethodPut:
		body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
		if err != nil {
			http.Error(w, "Read body error", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if err := os.WriteFile(filePath, body, 0o644); err != nil {
			http.Error(w, "Write error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})

	case http.MethodDelete:
		os.Remove(filePath)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})

	case http.MethodHead:
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleStorageBulk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	entries, err := os.ReadDir(storageDir())
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				os.Remove(filepath.Join(storageDir(), e.Name()))
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- Upload Handler ---

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "File too large or invalid form", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "No file provided", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ext := filepath.Ext(header.Filename)
	timestamp := time.Now().Format("20060102150405")
	randBytes := make([]byte, 8)
	rand.Read(randBytes)
	filename := fmt.Sprintf("%s-%s%s", timestamp, hex.EncodeToString(randBytes), ext)

	uploadPath := filepath.Join(dataDir, "upload")
	destPath := filepath.Join(uploadPath, filename)

	// Path traversal protection
	absUpload, _ := filepath.Abs(uploadPath)
	absDest, _ := filepath.Abs(destPath)
	if !strings.HasPrefix(absDest, absUpload+string(filepath.Separator)) {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}

	dest, err := os.Create(destPath)
	if err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}
	defer dest.Close()

	if _, err := io.Copy(dest, file); err != nil {
		http.Error(w, "Failed to write file", http.StatusInternalServerError)
		return
	}

	url := fmt.Sprintf("/upload/%s", filename)
	writeJSON(w, http.StatusOK, map[string]string{
		"url":      url,
		"filename": filename,
	})
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
