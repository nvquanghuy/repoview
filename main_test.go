package main

import (
	"encoding/json"
	"io"
	"io/fs"
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

// newTestServer creates a test server with the same routing as production.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tree", handleTree)
	mux.HandleFunc("/api/file", handleFile)
	sub, _ := fs.Sub(staticFiles, "static")
	indexHTML, _ := fs.ReadFile(sub, "index.html")
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

	// Read index.html to compare against.
	sub, _ := fs.Sub(staticFiles, "static")
	indexData, _ := fs.ReadFile(sub, "index.html")
	indexBody := string(indexData)

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
		if string(body) != indexBody {
			t.Errorf("GET %s: response body does not match index.html", p)
		}
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

func TestSPARoute_EncodedPaths(t *testing.T) {
	setRoot(t, "testdata")
	srv := newTestServer(t)
	defer srv.Close()

	sub, _ := fs.Sub(staticFiles, "static")
	indexData, _ := fs.ReadFile(sub, "index.html")
	indexBody := string(indexData)

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
		if string(body) != indexBody {
			t.Errorf("GET %s: response body does not match index.html", p)
		}
	}
}
