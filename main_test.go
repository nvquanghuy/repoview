package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
)

// setRoot sets rootDir to the given absolute path for tests.
func setRoot(t *testing.T, dir string) {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	rootDir = abs
}

// ── Tree tests ──────────────────────────────────────────────

func TestHandleTree_Root(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/tree", nil)
	w := httptest.NewRecorder()
	handleTree(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var entries []TreeEntry
	if err := json.NewDecoder(w.Body).Decode(&entries); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected non-empty tree entries")
	}

	// Should contain known test files.
	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name] = true
	}
	for _, want := range []string{"hello.md", "data.csv", "example.txt", "subdir"} {
		if !names[want] {
			t.Errorf("expected entry %q in tree", want)
		}
	}
}

func TestHandleTree_Subdir(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/tree?path=subdir", nil)
	w := httptest.NewRecorder()
	handleTree(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var entries []TreeEntry
	if err := json.NewDecoder(w.Body).Decode(&entries); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	found := false
	for _, e := range entries {
		if e.Name == "nested.md" {
			found = true
			if e.Path != filepath.Join("subdir", "nested.md") {
				t.Errorf("unexpected path: %s", e.Path)
			}
		}
	}
	if !found {
		t.Error("expected nested.md in subdir listing")
	}
}

func TestHandleTree_HiddenFiles(t *testing.T) {
	// Create a hidden file in testdata, then verify it's excluded.
	hidden := filepath.Join("testdata", ".hidden")
	os.WriteFile(hidden, []byte("secret"), 0644)
	defer os.Remove(hidden)

	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/tree", nil)
	w := httptest.NewRecorder()
	handleTree(w, req)

	var entries []TreeEntry
	json.NewDecoder(w.Body).Decode(&entries)

	for _, e := range entries {
		if strings.HasPrefix(e.Name, ".") {
			t.Errorf("hidden file %q should be excluded", e.Name)
		}
	}
}

func TestHandleTree_InvalidPath(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/tree?path=../../etc", nil)
	w := httptest.NewRecorder()
	handleTree(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for path traversal, got %d", w.Code)
	}
}

// ── File tests ──────────────────────────────────────────────

func TestHandleFile_Markdown(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/file?path=hello.md", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}

	var resp FileResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if !resp.IsMarkdown {
		t.Error("expected isMarkdown to be true")
	}
	if resp.IsCSV {
		t.Error("expected isCSV to be false")
	}
	if resp.Name != "hello.md" {
		t.Errorf("expected name hello.md, got %s", resp.Name)
	}
	if resp.Path != "hello.md" {
		t.Errorf("expected path hello.md, got %s", resp.Path)
	}
	// Goldmark should produce an <h1> tag from the "# Hello World" heading.
	if !strings.Contains(resp.Content, "<h1>") {
		t.Error("expected rendered HTML to contain <h1>")
	}
	if !strings.Contains(resp.Content, "<table>") {
		t.Error("expected rendered HTML to contain a table")
	}
}

func TestHandleFile_CSV(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/file?path=data.csv", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp FileResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if !resp.IsCSV {
		t.Error("expected isCSV to be true")
	}
	if resp.IsMarkdown {
		t.Error("expected isMarkdown to be false")
	}
	if !strings.Contains(resp.Content, "<table>") {
		t.Error("expected HTML table in content")
	}
	if !strings.Contains(resp.Content, "Alice") {
		t.Error("expected CSV data in rendered table")
	}
}

func TestHandleFile_PlainText(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/file?path=example.txt", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp FileResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.IsMarkdown || resp.IsCSV {
		t.Error("expected plain text, not markdown or CSV")
	}
	if !strings.Contains(resp.Content, "<pre>") {
		t.Error("expected <pre> wrapper for plain text")
	}
	if !strings.Contains(resp.Content, "plain text file") {
		t.Error("expected file content in response")
	}
}

func TestHandleFile_NotFound(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/file?path=nonexistent.txt", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleFile_Directory(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/file?path=subdir", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for directory, got %d", w.Code)
	}
}

// ── Files (flat list) test ──────────────────────────────────

func TestHandleFiles(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/files", nil)
	w := httptest.NewRecorder()
	handleFiles(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var files []string
	if err := json.NewDecoder(w.Body).Decode(&files); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected non-empty file list")
	}

	// Should find nested file.
	found := false
	for _, f := range files {
		if strings.Contains(f, "nested.md") {
			found = true
		}
	}
	if !found {
		t.Error("expected nested.md in file list")
	}
}

// ── safePath tests ──────────────────────────────────────────

func TestSafePath_Traversal(t *testing.T) {
	setRoot(t, "testdata")

	cases := []string{
		"../../../etc/passwd",
		"subdir/../../etc/passwd",
	}
	for _, c := range cases {
		_, err := safePath(c)
		if err == nil {
			t.Errorf("expected error for path %q, got nil", c)
		}
	}
}

func TestSafePath_Valid(t *testing.T) {
	setRoot(t, "testdata")

	cases := []string{
		"hello.md",
		"subdir/nested.md",
		"data.csv",
	}
	for _, c := range cases {
		p, err := safePath(c)
		if err != nil {
			t.Errorf("expected valid path for %q, got error: %v", c, err)
		}
		if !strings.HasPrefix(p, rootDir) {
			t.Errorf("resolved path %q not under root %q", p, rootDir)
		}
	}
}

// ── WebSocket integration test ──────────────────────────────

func TestWebSocket(t *testing.T) {
	setRoot(t, "testdata")

	h := newHub()
	go h.run()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer watcher.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleWS(w, r, h, watcher)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Connect via WebSocket.
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	// Send a watch message — should not error.
	msg := `{"action":"watch","path":"hello.md"}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatalf("failed to send message: %v", err)
	}
}

// ── Static file serving test ────────────────────────────────

func TestStaticFileServed(t *testing.T) {
	setRoot(t, "testdata")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/tree", handleTree)
	mux.HandleFunc("/api/file", handleFile)
	// Serve embedded static files.
	sub, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("failed to GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("expected text/html, got %s", ct)
	}
}
