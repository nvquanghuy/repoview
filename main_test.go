package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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
			if e.Path != "subdir/nested.md" {
				t.Errorf("unexpected path: %s", e.Path)
			}
		}
	}
	if !found {
		t.Error("expected nested.md in subdir listing")
	}
}

func TestHandleTree_HiddenFiles(t *testing.T) {
	// Create a hidden file in testdata, then verify it's included.
	hidden := filepath.Join("testdata", ".hidden")
	os.WriteFile(hidden, []byte("secret"), 0644)
	defer os.Remove(hidden)

	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/tree", nil)
	w := httptest.NewRecorder()
	handleTree(w, req)

	var entries []TreeEntry
	json.NewDecoder(w.Body).Decode(&entries)

	found := false
	for _, e := range entries {
		if e.Name == ".hidden" {
			found = true
		}
	}
	if !found {
		t.Error("expected dotfile .hidden to be included in tree")
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
	if !strings.Contains(resp.Content, "<h1") {
		t.Error("expected rendered HTML to contain <h1>")
	}
	// Headings should have id attributes for anchor linking.
	if !strings.Contains(resp.Content, `id="hello-world"`) {
		t.Error("expected heading to have id attribute for anchor linking")
	}
	if !strings.Contains(resp.Content, "<table>") {
		t.Error("expected rendered HTML to contain a table")
	}
}

func TestHandleFile_MarkdownHardWraps(t *testing.T) {
	// Create a temp file with newlines that should become <br> tags
	tmpDir := t.TempDir()
	content := "Line one\nLine two\nLine three"
	if err := os.WriteFile(filepath.Join(tmpDir, "test.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	setRoot(t, tmpDir)

	req := httptest.NewRequest("GET", "/api/file?path=test.md", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp FileResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Hard wraps should convert newlines to <br> tags
	if !strings.Contains(resp.Content, "<br") {
		t.Error("expected newlines to be rendered as <br> tags")
	}
}

func TestHandleFile_MarkdownFrontmatter(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/file?path=frontmatter.md", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp FileResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if !resp.IsMarkdown {
		t.Error("expected isMarkdown to be true")
	}
	// Should contain frontmatter table with keys and values.
	if !strings.Contains(resp.Content, "<table>") {
		t.Error("expected frontmatter table")
	}
	if !strings.Contains(resp.Content, "title") {
		t.Error("expected frontmatter key 'title'")
	}
	if !strings.Contains(resp.Content, "My Document") {
		t.Error("expected frontmatter value 'My Document'")
	}
	if !strings.Contains(resp.Content, "author") {
		t.Error("expected frontmatter key 'author'")
	}
	if !strings.Contains(resp.Content, "Jane Doe") {
		t.Error("expected frontmatter value 'Jane Doe'")
	}
	// Body should be rendered as markdown.
	if !strings.Contains(resp.Content, "<h1") {
		t.Error("expected rendered <h1> from markdown body")
	}
	if !strings.Contains(resp.Content, "Welcome") {
		t.Error("expected body content 'Welcome'")
	}
}

func TestParseFrontmatter_CRLF(t *testing.T) {
	data := []byte("---\r\ntitle: Test\r\nauthor: Alice\r\n---\r\n# Hello\r\n")
	pairs, body := parseFrontmatter(data)

	if len(pairs) != 2 {
		t.Fatalf("expected 2 frontmatter pairs, got %d", len(pairs))
	}
	if pairs[0][0] != "title" || pairs[0][1] != "Test" {
		t.Errorf("unexpected first pair: %v", pairs[0])
	}
	if pairs[1][0] != "author" || pairs[1][1] != "Alice" {
		t.Errorf("unexpected second pair: %v", pairs[1])
	}
	if !strings.Contains(string(body), "# Hello") {
		t.Error("expected body to contain '# Hello'")
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

func TestHandleFile_CSVRawCSV(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/file?path=data.csv", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp FileResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if !resp.IsCSV {
		t.Error("expected isCSV to be true")
	}
	if resp.RawCSV == "" {
		t.Error("expected non-empty rawCSV for CSV file")
	}
	// rawCSV should contain the original CSV data
	if !strings.Contains(resp.RawCSV, "name") {
		t.Error("expected CSV header 'name' in rawCSV")
	}
	if !strings.Contains(resp.RawCSV, "Alice") {
		t.Error("expected CSV data 'Alice' in rawCSV")
	}
}

func TestHandleFile_GoSyntax(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/file?path=sample.go", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp FileResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if !strings.Contains(resp.Content, "<span") {
		t.Error("expected syntax-highlighted <span> tokens in Go file")
	}
	if !strings.Contains(resp.Content, "Println") {
		t.Error("expected Go source content in response")
	}
}

func TestHandleFile_PySyntax(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/file?path=sample.py", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp FileResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if !strings.Contains(resp.Content, "<span") {
		t.Error("expected syntax-highlighted <span> tokens in Python file")
	}
	if !strings.Contains(resp.Content, "greet") {
		t.Error("expected Python source content in response")
	}
}

func TestHandleFile_JSON(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/file?path=data.json", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp FileResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Should be pretty-printed (contains newlines and indentation).
	if !strings.Contains(resp.Content, "Alice") {
		t.Error("expected JSON content with 'Alice'")
	}
	// Syntax-highlighted spans from Chroma.
	if !strings.Contains(resp.Content, "<span") {
		t.Error("expected syntax-highlighted <span> tokens in JSON file")
	}
	// Pretty-printed JSON should have indentation visible in the output.
	// The raw fixture is minified, so the presence of formatted key on its own line means it was pretty-printed.
	if !strings.Contains(resp.Content, "hobbies") {
		t.Error("expected 'hobbies' in pretty-printed JSON")
	}
}

func TestHandleFile_YAML(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/file?path=config.yaml", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp FileResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Should contain YAML keys.
	if !strings.Contains(resp.Content, "server") {
		t.Error("expected YAML content with 'server'")
	}
	if !strings.Contains(resp.Content, "database") {
		t.Error("expected YAML content with 'database'")
	}
	// Syntax-highlighted.
	if !strings.Contains(resp.Content, "<span") {
		t.Error("expected syntax-highlighted <span> tokens in YAML file")
	}
}

func TestHandleFile_MarkdownRawContent(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/file?path=hello.md", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp FileResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if !resp.IsMarkdown {
		t.Error("expected isMarkdown to be true")
	}
	if resp.RawContent == "" {
		t.Error("expected non-empty rawContent for markdown file")
	}
	// rawContent should be syntax-highlighted (contains Chroma <span> tokens)
	if !strings.Contains(resp.RawContent, "<span") {
		t.Error("expected syntax-highlighted <span> tokens in rawContent")
	}
	// rawContent should contain the original markdown source text
	if !strings.Contains(resp.RawContent, "Hello World") {
		t.Error("expected raw markdown source in rawContent")
	}
}

func TestHandleFile_NonMarkdownNoRawContent(t *testing.T) {
	setRoot(t, "testdata")

	cases := []string{"example.txt", "data.csv", "sample.go"}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/file?path="+url.QueryEscape(path), nil)
			w := httptest.NewRecorder()
			handleFile(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", w.Code)
			}

			var resp FileResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}

			if resp.RawContent != "" {
				t.Errorf("expected empty rawContent for %s, got non-empty", path)
			}
		})
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
	if !strings.Contains(resp.Content, "<pre") {
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

func TestSafePath_SiblingPrefix(t *testing.T) {
	// Ensure a sibling directory with a similar prefix cannot be accessed.
	// If rootDir is "/tmp/repo", a path resolving to "/tmp/repo2" should be rejected.
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "repo")
	sibling := filepath.Join(tmpDir, "repo2")
	os.MkdirAll(target, 0755)
	os.MkdirAll(sibling, 0755)
	os.WriteFile(filepath.Join(sibling, "secret.txt"), []byte("secret"), 0644)

	oldRoot := rootDir
	rootDir = target
	t.Cleanup(func() { rootDir = oldRoot })

	// Attempt to access sibling via prefix confusion
	_, err := safePath("../repo2/secret.txt")
	if err == nil {
		t.Error("expected error when accessing sibling directory with similar prefix, got nil")
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

func TestNotifyFile_ParentDirectoryWatch(t *testing.T) {
	h := newHub()

	// Create a mock client watching a directory
	client := &wsClient{
		hub:     h,
		send:    make(chan []byte, 64),
		watched: map[string]bool{"subdir": true},
	}
	h.register(client)
	defer h.unregister(client)

	// Notify about a file inside the watched directory (paths are forward-slash normalized)
	h.notifyFile("subdir/nested.md")

	// Should receive the notification
	select {
	case msg := <-client.send:
		var data map[string]string
		if err := json.Unmarshal(msg, &data); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if data["type"] != "change" {
			t.Errorf("expected type=change, got %s", data["type"])
		}
	default:
		t.Error("expected to receive notification for file in watched directory")
	}
}

func TestNotifyFile_RootWatch(t *testing.T) {
	h := newHub()

	// Create a mock client watching the root directory
	client := &wsClient{
		hub:     h,
		send:    make(chan []byte, 64),
		watched: map[string]bool{"": true},
	}
	h.register(client)
	defer h.unregister(client)

	// Notify about a file in the root
	h.notifyFile("newfile.txt")

	// Should receive the notification
	select {
	case msg := <-client.send:
		var data map[string]string
		if err := json.Unmarshal(msg, &data); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if data["type"] != "change" {
			t.Errorf("expected type=change, got %s", data["type"])
		}
	default:
		t.Error("expected to receive notification for file when watching root")
	}
}

func TestNotifyFile_NestedDirectoryWatch(t *testing.T) {
	h := newHub()

	// Create a mock client watching a parent directory
	client := &wsClient{
		hub:     h,
		send:    make(chan []byte, 64),
		watched: map[string]bool{"src": true},
	}
	h.register(client)
	defer h.unregister(client)

	// Notify about a deeply nested file (paths are forward-slash normalized)
	h.notifyFile("src/components/Button.tsx")

	// Should receive the notification
	select {
	case msg := <-client.send:
		var data map[string]string
		if err := json.Unmarshal(msg, &data); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if data["type"] != "change" {
			t.Errorf("expected type=change, got %s", data["type"])
		}
	default:
		t.Error("expected to receive notification for nested file in watched directory")
	}
}

func TestNotifyFile_NoMatchingWatch(t *testing.T) {
	h := newHub()

	// Create a mock client watching a different directory
	client := &wsClient{
		hub:     h,
		send:    make(chan []byte, 64),
		watched: map[string]bool{"other": true},
	}
	h.register(client)
	defer h.unregister(client)

	// Notify about a file in a different directory (paths are forward-slash normalized)
	h.notifyFile("src/main.go")

	// Should NOT receive the notification
	select {
	case <-client.send:
		t.Error("should not receive notification for unwatched directory")
	default:
		// Expected: no notification
	}
}

// ── Static file serving test ────────────────────────────────

// newTestServer creates a test server with the same routing as production.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tree", handleTree)
	mux.HandleFunc("/api/file", handleFile)
	mux.HandleFunc("/api/raw", handleRaw)
	sub, _ := fs.Sub(staticFiles, "static")
	rawHTML, _ := fs.ReadFile(sub, "index.html")
	indexHTML := prepareIndexHTML(rawHTML, filepath.Base(rootDir))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	return httptest.NewServer(mux)
}

func TestStaticFileServed(t *testing.T) {
	setRoot(t, "testdata")
	srv := newTestServer(t)
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

// ── SPA routing tests (/files/*) ────────────────────────────

func TestSPARoute_ServesIndexHTML(t *testing.T) {
	setRoot(t, "testdata")
	srv := newTestServer(t)
	defer srv.Close()

	paths := []string{
		"/hello.md",
		"/subdir/nested.md",
		"/subdir",
		"/",
	}

	sub, _ := fs.Sub(staticFiles, "static")
	rawHTML, _ := fs.ReadFile(sub, "index.html")
	expectedBody := string(prepareIndexHTML(rawHTML, filepath.Base(rootDir)))

	for _, p := range paths {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("failed to GET %s: %v", p, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: expected 200, got %d", p, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Errorf("GET %s: expected text/html, got %s", p, ct)
		}
		if string(body) != expectedBody {
			t.Errorf("GET %s: response body does not match prepared index.html", p)
		}
	}
}

func TestIndexHTML_ContainsRootDirName(t *testing.T) {
	setRoot(t, "testdata")
	srv := newTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("failed to GET /: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), "<title>testdata - RepoView</title>") {
		t.Error("expected page title to contain root dir name")
	}
	if !strings.Contains(string(body), `var rootDirName = "testdata"`) {
		t.Error("expected JS rootDirName variable with root dir name")
	}
}

func TestFilesRoute_DoesNotAffectAPI(t *testing.T) {
	setRoot(t, "testdata")
	srv := newTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/tree")
	if err != nil {
		t.Fatalf("failed to GET /api/tree: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected application/json, got %s", ct)
	}
}

// ── Special filename tests ──────────────────────────────────

func TestHandleFile_SpecialNames(t *testing.T) {
	setRoot(t, "testdata")

	cases := []struct {
		name     string
		apiPath  string
		wantCode int
	}{
		{"spaces", "file with spaces.txt", http.StatusOK},
		{"uppercase", "UPPERCASE.TXT", http.StatusOK},
		{"parens", "special (1).md", http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/file?path="+url.QueryEscape(tc.apiPath), nil)
			w := httptest.NewRecorder()
			handleFile(w, req)

			if w.Code != tc.wantCode {
				t.Errorf("expected %d, got %d", tc.wantCode, w.Code)
			}
			if tc.wantCode == http.StatusOK {
				var resp FileResponse
				if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				if resp.Content == "" {
					t.Error("expected non-empty content")
				}
			}
		})
	}
}

func TestHandleTree_SpecialNames(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/tree", nil)
	w := httptest.NewRecorder()
	handleTree(w, req)

	var entries []TreeEntry
	if err := json.NewDecoder(w.Body).Decode(&entries); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	wantNames := map[string]bool{
		"file with spaces.txt": false,
		"UPPERCASE.TXT":        false,
		"special (1).md":       false,
	}
	for _, e := range entries {
		if _, ok := wantNames[e.Name]; ok {
			wantNames[e.Name] = true
		}
	}
	for name, found := range wantNames {
		if !found {
			t.Errorf("expected %q in tree listing", name)
		}
	}
}

// ── Binary file tests ────────────────────────────────────────

func TestHandleFile_BinaryImage(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/file?path=pixel.png", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp FileResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if !resp.IsBinary {
		t.Error("expected isBinary to be true")
	}
	if resp.MimeType != "image/png" {
		t.Errorf("expected mimeType image/png, got %s", resp.MimeType)
	}
	if resp.Content != "" {
		t.Error("expected empty content for binary file")
	}
	if resp.Size == 0 {
		t.Error("expected non-zero size for binary file")
	}
}

func TestHandleFile_BinaryExecutable(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/file?path=tiny.bin", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp FileResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if !resp.IsBinary {
		t.Error("expected isBinary to be true")
	}
	if resp.Content != "" {
		t.Error("expected empty content for binary file")
	}
}

func TestHandleFile_PDF(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/file?path=test.pdf", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp FileResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if !resp.IsPDF {
		t.Error("expected isPDF to be true")
	}
	if resp.IsBinary {
		t.Error("expected isBinary to be false for PDF")
	}
	if resp.MimeType != "application/pdf" {
		t.Errorf("expected mimeType application/pdf, got %s", resp.MimeType)
	}
	if resp.Content != "" {
		t.Error("expected empty content for PDF file")
	}
	if resp.Size == 0 {
		t.Error("expected non-zero size for PDF file")
	}
}

func TestHandleFile_SVG(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/file?path=icon.svg", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp FileResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if !resp.IsSVG {
		t.Error("expected isSVG to be true")
	}
	if resp.IsBinary {
		t.Error("expected isBinary to be false for SVG")
	}
	// Content should be syntax-highlighted code (same as default rendering)
	if !strings.Contains(resp.Content, "<span") {
		t.Error("expected syntax-highlighted <span> tokens in SVG content")
	}
	// RawContent should also be present (for code view toggle)
	if resp.RawContent == "" {
		t.Error("expected non-empty rawContent for SVG file")
	}
	if !strings.Contains(resp.Content, "circle") {
		t.Error("expected SVG source content in response")
	}
	// RawSVG should contain the original SVG markup for inline rendering
	if resp.RawSVG == "" {
		t.Error("expected non-empty rawSVG for SVG file")
	}
	if !strings.Contains(resp.RawSVG, "<svg") {
		t.Error("expected raw SVG markup in rawSVG field")
	}
}

func TestHandleRaw_SVG(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/raw?path=icon.svg", nil)
	w := httptest.NewRecorder()
	handleRaw(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "svg") && !strings.Contains(ct, "xml") {
		t.Errorf("expected Content-Type containing svg or xml, got %s", ct)
	}
	if w.Body.Len() == 0 {
		t.Error("expected non-empty body for raw SVG file")
	}
}

func TestHandleRaw(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/raw?path=pixel.png", nil)
	w := httptest.NewRecorder()
	handleRaw(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "image/png") {
		t.Errorf("expected Content-Type containing image/png, got %s", ct)
	}
	if w.Body.Len() == 0 {
		t.Error("expected non-empty body for raw file")
	}
}

func TestSPARoute_EncodedPaths(t *testing.T) {
	setRoot(t, "testdata")
	srv := newTestServer(t)
	defer srv.Close()

	sub, _ := fs.Sub(staticFiles, "static")
	rawHTML, _ := fs.ReadFile(sub, "index.html")
	expectedBody := string(prepareIndexHTML(rawHTML, filepath.Base(rootDir)))

	// URL-encoded paths that the browser would produce
	paths := []string{
		"/file%20with%20spaces.txt",
		"/UPPERCASE.TXT",
		"/special%20(1).md",
	}

	for _, p := range paths {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("failed to GET %s: %v", p, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: expected 200, got %d", p, resp.StatusCode)
		}
		if string(body) != expectedBody {
			t.Errorf("GET %s: response body does not match prepared index.html", p)
		}
	}
}


// ── Editor endpoints tests ─────────────────────────────────────

func TestHandleEditors(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/editors", nil)
	w := httptest.NewRecorder()
	handleEditors(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var editors []EditorInfo
	if err := json.NewDecoder(w.Body).Decode(&editors); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Should return an array (may be empty if no editors installed)
	if editors == nil {
		t.Error("expected non-nil array")
	}
}

func TestHandleOpen_InvalidEditor(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("POST", "/api/open?path=hello.md&editor=nonexistent", nil)
	w := httptest.NewRecorder()
	handleOpen(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp OpenResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if resp.Success {
		t.Error("expected success=false for unknown editor")
	}
	if resp.Error != "unknown editor" {
		t.Errorf("expected 'unknown editor' error, got %q", resp.Error)
	}
}

func TestHandleOpen_MissingPath(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/open?editor=vscode", nil)
	w := httptest.NewRecorder()
	handleOpen(w, req)

	var resp OpenResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Success {
		t.Error("expected success=false for missing path")
	}
	if resp.Error != "path required" {
		t.Errorf("expected 'path required' error, got %q", resp.Error)
	}
}

func TestHandleOpen_MissingEditor(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/open?path=hello.md", nil)
	w := httptest.NewRecorder()
	handleOpen(w, req)

	var resp OpenResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Success {
		t.Error("expected success=false for missing editor")
	}
	if resp.Error != "editor required" {
		t.Errorf("expected 'editor required' error, got %q", resp.Error)
	}
}

func TestHandleOpen_PathTraversal(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("POST", "/api/open?path=../../../etc/passwd&editor=vscode", nil)
	w := httptest.NewRecorder()
	handleOpen(w, req)

	var resp OpenResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Success {
		t.Error("expected success=false for path traversal")
	}
	if resp.Error != "invalid path" {
		t.Errorf("expected 'invalid path' error, got %q", resp.Error)
	}
}

func TestHandleOpen_FileNotFound(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("POST", "/api/open?path=nonexistent.txt&editor=vscode", nil)
	w := httptest.NewRecorder()
	handleOpen(w, req)

	var resp OpenResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Success {
		t.Error("expected success=false for nonexistent file")
	}
	if resp.Error != "file not found" {
		t.Errorf("expected 'file not found' error, got %q", resp.Error)
	}
}

func TestHandleOpen_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/open?path=hello.md&editor=vscode", nil)
	w := httptest.NewRecorder()
	handleOpen(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ── Port finding tests ─────────────────────────────────────────

func TestFindAvailablePort_FirstPortAvailable(t *testing.T) {
	// When the starting port is available, it should return that port
	port := findAvailablePort("127.0.0.1", 19000, 10)
	if port != 19000 {
		t.Errorf("expected port 19000, got %d", port)
	}
}

func TestFindAvailablePort_SkipsOccupiedPorts(t *testing.T) {
	// Occupy a port
	ln, err := net.Listen("tcp", "127.0.0.1:19100")
	if err != nil {
		t.Fatalf("failed to occupy port: %v", err)
	}
	defer ln.Close()

	// Should skip 19100 and return 19101
	port := findAvailablePort("127.0.0.1", 19100, 10)
	if port != 19101 {
		t.Errorf("expected port 19101, got %d", port)
	}
}

func TestFindAvailablePort_SkipsMultipleOccupiedPorts(t *testing.T) {
	// Occupy multiple consecutive ports
	var listeners []net.Listener
	for i := 0; i < 3; i++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", 19200+i))
		if err != nil {
			t.Fatalf("failed to occupy port %d: %v", 19200+i, err)
		}
		listeners = append(listeners, ln)
	}
	defer func() {
		for _, ln := range listeners {
			ln.Close()
		}
	}()

	// Should skip 19200, 19201, 19202 and return 19203
	port := findAvailablePort("127.0.0.1", 19200, 10)
	if port != 19203 {
		t.Errorf("expected port 19203, got %d", port)
	}
}

func TestPreprocessWikiLinks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantFrag string
	}{
		{
			name:     "simple link",
			input:    "[[My Note]]",
			wantFrag: `<a class="wiki-link" href="#" data-wiki-target="My Note">My Note</a>`,
		},
		{
			name:     "link with display text",
			input:    "[[My Note|Display Text]]",
			wantFrag: `<a class="wiki-link" href="#" data-wiki-target="My Note">Display Text</a>`,
		},
		{
			name:     "link with heading",
			input:    "[[My Note#Section]]",
			wantFrag: `<a class="wiki-link" href="#" data-wiki-target="My Note" data-wiki-heading="Section">My Note</a>`,
		},
		{
			name:     "link with heading and display text",
			input:    "[[My Note#Section|Display]]",
			wantFrag: `<a class="wiki-link" href="#" data-wiki-target="My Note" data-wiki-heading="Section">Display</a>`,
		},
		{
			name:     "no wiki link unchanged",
			input:    "plain text without links",
			wantFrag: "plain text without links",
		},
		{
			name:     "html special chars escaped",
			input:    `[[A & B|<Display>]]`,
			wantFrag: `data-wiki-target="A &amp; B"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := string(preprocessWikiLinks([]byte(tc.input)))
			if !strings.Contains(got, tc.wantFrag) {
				t.Errorf("preprocessWikiLinks(%q) = %q, want fragment %q", tc.input, got, tc.wantFrag)
			}
		})
	}
}

func TestRenderMarkdown_WikiLinks(t *testing.T) {
	// Enable Obsidian mode for this test.
	old := isObsidianVault
	isObsidianVault = true
	defer func() { isObsidianVault = old }()

	input := []byte("See [[My Note]] for details.")
	var buf bytes.Buffer
	renderMarkdown(&buf, input)
	out := buf.String()

	if !strings.Contains(out, `class="wiki-link"`) {
		t.Errorf("expected wiki-link class in output, got: %s", out)
	}
	if !strings.Contains(out, `data-wiki-target="My Note"`) {
		t.Errorf("expected data-wiki-target attribute in output, got: %s", out)
	}
}

// ── JSONL tests ──────────────────────────────────────────────

func TestHandleJSONL_Pagination(t *testing.T) {
	setRoot(t, "testdata")

	// Test first page
	req := httptest.NewRequest("GET", "/api/jsonl?path=small.jsonl&page=1&pageSize=5", nil)
	w := httptest.NewRecorder()
	handleJSONL(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp JSONLResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if resp.TotalLines != 10 {
		t.Errorf("expected 10 total lines, got %d", resp.TotalLines)
	}

	if resp.Page != 1 {
		t.Errorf("expected page 1, got %d", resp.Page)
	}

	if resp.PageSize != 5 {
		t.Errorf("expected page size 5, got %d", resp.PageSize)
	}

	if len(resp.Records) != 5 {
		t.Errorf("expected 5 records, got %d", len(resp.Records))
	}

	if !resp.HasMore {
		t.Error("expected HasMore to be true")
	}

	// Test second page
	req2 := httptest.NewRequest("GET", "/api/jsonl?path=small.jsonl&page=2&pageSize=5", nil)
	w2 := httptest.NewRecorder()
	handleJSONL(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}

	var resp2 JSONLResponse
	if err := json.NewDecoder(w2.Body).Decode(&resp2); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(resp2.Records) != 5 {
		t.Errorf("expected 5 records on page 2, got %d", len(resp2.Records))
	}

	if resp2.HasMore {
		t.Error("expected HasMore to be false on last page")
	}
}

func TestCountJSONLLines(t *testing.T) {
	setRoot(t, "testdata")

	tests := []struct {
		file     string
		expected int64
	}{
		{"small.jsonl", 10},
		{"single-line.jsonl", 1},
		{"empty.jsonl", 0},
		{"nested.jsonl", 3},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			path := filepath.Join(rootDir, tt.file)
			count, err := countJSONLLines(path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if count != tt.expected {
				t.Errorf("expected %d lines, got %d", tt.expected, count)
			}
		})
	}
}

func TestReadJSONLPage(t *testing.T) {
	setRoot(t, "testdata")

	path := filepath.Join(rootDir, "small.jsonl")

	// Test first page
	records, parseErrors, err := readJSONLPage(path, 1, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(records) != 5 {
		t.Errorf("expected 5 records, got %d", len(records))
	}

	if len(parseErrors) != 0 {
		t.Errorf("expected no parse errors, got %d", len(parseErrors))
	}

	// Verify first record contains expected data
	if !strings.Contains(records[0], "Alice") {
		t.Errorf("expected first record to contain 'Alice', got: %s", records[0])
	}

	// Test second page
	records2, _, err := readJSONLPage(path, 2, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(records2) != 5 {
		t.Errorf("expected 5 records on page 2, got %d", len(records2))
	}

	// Verify we got different records
	if records[0] == records2[0] {
		t.Error("expected different records on different pages")
	}
}

func TestHandleJSONL_MalformedJSON(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/jsonl?path=malformed.jsonl&page=1&pageSize=10", nil)
	w := httptest.NewRecorder()
	handleJSONL(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp JSONLResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Should count all non-empty lines (valid + malformed)
	if resp.TotalLines != 6 {
		t.Errorf("expected 6 total lines (valid + malformed counted), got %d", resp.TotalLines)
	}

	// Should have some parse errors
	if len(resp.ParseErrors) == 0 {
		t.Error("expected parse errors for malformed JSON")
	}
}

func TestHandleJSONL_EmptyFile(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/jsonl?path=empty.jsonl&page=1&pageSize=100", nil)
	w := httptest.NewRecorder()
	handleJSONL(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp JSONLResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if resp.TotalLines != 0 {
		t.Errorf("expected 0 total lines, got %d", resp.TotalLines)
	}

	if len(resp.Records) != 0 {
		t.Errorf("expected 0 records, got %d", len(resp.Records))
	}

	if resp.HasMore {
		t.Error("expected HasMore to be false for empty file")
	}
}

func TestHandleJSONL_SingleLine(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/jsonl?path=single-line.jsonl&page=1&pageSize=100", nil)
	w := httptest.NewRecorder()
	handleJSONL(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp JSONLResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if resp.TotalLines != 1 {
		t.Errorf("expected 1 total line, got %d", resp.TotalLines)
	}

	if len(resp.Records) != 1 {
		t.Errorf("expected 1 record, got %d", len(resp.Records))
	}

	if resp.HasMore {
		t.Error("expected HasMore to be false for single line file")
	}
}

func TestHandleJSONL_LargeFile(t *testing.T) {
	setRoot(t, "testdata")

	// Test that large file pagination works
	req := httptest.NewRequest("GET", "/api/jsonl?path=large.jsonl&page=1&pageSize=100", nil)
	w := httptest.NewRecorder()
	handleJSONL(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp JSONLResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if resp.TotalLines != 10000 {
		t.Errorf("expected 10000 total lines, got %d", resp.TotalLines)
	}

	if len(resp.Records) != 100 {
		t.Errorf("expected 100 records, got %d", len(resp.Records))
	}

	if !resp.HasMore {
		t.Error("expected HasMore to be true")
	}

	// Test a later page
	req2 := httptest.NewRequest("GET", "/api/jsonl?path=large.jsonl&page=50&pageSize=100", nil)
	w2 := httptest.NewRecorder()
	handleJSONL(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}

	var resp2 JSONLResponse
	if err := json.NewDecoder(w2.Body).Decode(&resp2); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(resp2.Records) != 100 {
		t.Errorf("expected 100 records on page 50, got %d", len(resp2.Records))
	}
}

func TestHandleFile_JSONL(t *testing.T) {
	setRoot(t, "testdata")

	req := httptest.NewRequest("GET", "/api/file?path=small.jsonl", nil)
	w := httptest.NewRecorder()
	handleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp FileResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if !resp.IsJSONL {
		t.Error("expected IsJSONL to be true")
	}

	if resp.MimeType != "application/x-ndjson" {
		t.Errorf("expected MIME type application/x-ndjson, got %s", resp.MimeType)
	}

	if resp.Size == 0 {
		t.Error("expected size to be non-zero")
	}
}
