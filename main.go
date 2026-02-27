package main

import (
	"bytes"
	"embed"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
	"github.com/pkg/browser"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

//go:embed static/index.html
var staticFiles embed.FS

var rootDir string

// TreeEntry represents a single item in the file tree.
type TreeEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsDir     bool   `json:"isDir"`
	Extension string `json:"extension,omitempty"`
}

func main() {
	port := flag.Int("port", 8080, "port to serve on")
	noBrowser := flag.Bool("no-browser", false, "don't open browser on startup")
	flag.Parse()

	if flag.NArg() > 0 {
		rootDir = flag.Arg(0)
	} else {
		var err error
		rootDir, err = os.Getwd()
		if err != nil {
			log.Fatal(err)
		}
	}

	var err error
	rootDir, err = filepath.Abs(rootDir)
	if err != nil {
		log.Fatal(err)
	}

	hub := newHub()
	go hub.run()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	go watchLoop(watcher, hub)

	http.HandleFunc("/api/tree", handleTree)
	http.HandleFunc("/api/file", handleFile)
	http.HandleFunc("/api/files", handleFiles)
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleWS(w, r, hub, watcher)
	})

	// Serve embedded static files at root.
	staticSub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}
	http.Handle("/", http.FileServer(http.FS(staticSub)))

	addr := fmt.Sprintf("0.0.0.0:%d", *port)
	url := fmt.Sprintf("http://%s", addr)
	fmt.Printf("repoview serving %s at %s\n", rootDir, url)

	if !*noBrowser {
		_ = browser.OpenURL(url)
	}

	log.Fatal(http.ListenAndServe(addr, nil))
}

// safePath resolves a request path within rootDir and rejects traversal.
func safePath(reqPath string) (string, error) {
	cleaned := filepath.Clean(reqPath)
	full := filepath.Join(rootDir, cleaned)
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(abs, rootDir) {
		return "", fmt.Errorf("path outside root")
	}
	return abs, nil
}

// handleTree returns immediate children of a directory as JSON.
func handleTree(w http.ResponseWriter, r *http.Request) {
	reqPath := r.URL.Query().Get("path")
	if reqPath == "" {
		reqPath = "."
	}

	dirPath, err := safePath(reqPath)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		http.Error(w, "cannot read directory", http.StatusNotFound)
		return
	}

	result := make([]TreeEntry, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		// Skip hidden files/dirs.
		if strings.HasPrefix(name, ".") {
			continue
		}
		relPath, _ := filepath.Rel(rootDir, filepath.Join(dirPath, name))
		ext := ""
		if !e.IsDir() {
			ext = strings.TrimPrefix(filepath.Ext(name), ".")
		}
		result = append(result, TreeEntry{
			Name:      name,
			Path:      relPath,
			IsDir:     e.IsDir(),
			Extension: ext,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// FileResponse is the JSON envelope returned by the /api/file endpoint.
type FileResponse struct {
	Content    string `json:"content"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	IsMarkdown bool   `json:"isMarkdown"`
	IsCSV      bool   `json:"isCSV"`
}

// handleFile returns rendered file content as JSON.
func handleFile(w http.ResponseWriter, r *http.Request) {
	reqPath := r.URL.Query().Get("path")
	if reqPath == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}

	filePath, err := safePath(reqPath)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		http.Error(w, "cannot read file", http.StatusInternalServerError)
		return
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	isMarkdown := ext == ".md" || ext == ".markdown"
	isCSV := ext == ".csv"

	var buf bytes.Buffer
	switch {
	case isMarkdown:
		renderMarkdown(&buf, data)
	case isCSV:
		renderCSV(&buf, data)
	default:
		fmt.Fprintf(&buf, "<pre>%s</pre>", html.EscapeString(string(data)))
	}

	resp := FileResponse{
		Content:    buf.String(),
		Name:       filepath.Base(filePath),
		Path:       reqPath,
		IsMarkdown: isMarkdown,
		IsCSV:      isCSV,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// renderMarkdown converts markdown bytes to HTML using goldmark with GFM.
func renderMarkdown(w io.Writer, data []byte) {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
		),
	)
	var buf bytes.Buffer
	if err := md.Convert(data, &buf); err != nil {
		fmt.Fprintf(w, "<pre>%s</pre>", html.EscapeString(string(data)))
		return
	}
	buf.WriteTo(w)
}

// renderCSV parses CSV data and writes a GitHub-style HTML table.
func renderCSV(w io.Writer, data []byte) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1 // allow variable fields
	records, err := reader.ReadAll()
	if err != nil {
		fmt.Fprintf(w, "<pre>%s</pre>", html.EscapeString(string(data)))
		return
	}
	if len(records) == 0 {
		return
	}

	fmt.Fprint(w, "<table><thead><tr>")
	for _, cell := range records[0] {
		fmt.Fprintf(w, "<th>%s</th>", html.EscapeString(cell))
	}
	fmt.Fprint(w, "</tr></thead><tbody>")
	for _, row := range records[1:] {
		fmt.Fprint(w, "<tr>")
		for _, cell := range row {
			fmt.Fprintf(w, "<td>%s</td>", html.EscapeString(cell))
		}
		fmt.Fprint(w, "</tr>")
	}
	fmt.Fprint(w, "</tbody></table>")
}

// handleFiles returns a flat list of all file paths for fuzzy search.
func handleFiles(w http.ResponseWriter, r *http.Request) {
	var files []string

	// Try git ls-files first.
	cmd := exec.Command("git", "-C", rootDir, "ls-files")
	out, err := cmd.Output()
	if err == nil && len(out) > 0 {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" {
				files = append(files, line)
			}
		}
	} else {
		// Walk the directory tree.
		filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") && path != rootDir {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				rel, _ := filepath.Rel(rootDir, path)
				files = append(files, rel)
			}
			return nil
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

// --- WebSocket hub and client ---

type hub struct {
	mu      sync.Mutex
	clients map[*wsClient]bool
}

type wsClient struct {
	conn    *websocket.Conn
	hub     *hub
	send    chan []byte
	watched map[string]bool // paths this client wants watched
	mu      sync.Mutex
}

func newHub() *hub {
	return &hub{clients: make(map[*wsClient]bool)}
}

func (h *hub) run() {
	// Hub doesn't need a goroutine loop; broadcast is called directly.
}

func (h *hub) register(c *wsClient) {
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
}

func (h *hub) unregister(c *wsClient) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	close(c.send)
}

func (h *hub) broadcast(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
		}
	}
}

// notifyFile sends a change notification only to clients watching a given path.
func (h *hub) notifyFile(relPath string) {
	msg, _ := json.Marshal(map[string]string{"type": "change", "path": relPath})
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		c.mu.Lock()
		watching := c.watched[relPath]
		c.mu.Unlock()
		if watching {
			select {
			case c.send <- msg:
			default:
			}
		}
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func handleWS(w http.ResponseWriter, r *http.Request, h *hub, watcher *fsnotify.Watcher) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &wsClient{
		conn:    conn,
		hub:     h,
		send:    make(chan []byte, 64),
		watched: make(map[string]bool),
	}
	h.register(client)

	go client.writePump()
	go client.readPump(watcher)
}

func (c *wsClient) readPump(watcher *fsnotify.Watcher) {
	defer func() {
		c.hub.unregister(c)
		c.conn.Close()
	}()
	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		var req struct {
			Action string `json:"action"` // "watch" or "unwatch"
			Path   string `json:"path"`
		}
		if json.Unmarshal(msg, &req) != nil {
			continue
		}

		absPath, err := safePath(req.Path)
		if err != nil {
			continue
		}

		c.mu.Lock()
		switch req.Action {
		case "watch":
			c.watched[req.Path] = true
			_ = watcher.Add(absPath)
		case "unwatch":
			delete(c.watched, req.Path)
			// Don't remove from fsnotify—other clients may watch it.
		}
		c.mu.Unlock()
	}
}

func (c *wsClient) writePump() {
	for msg := range c.send {
		if c.conn.WriteMessage(websocket.TextMessage, msg) != nil {
			break
		}
	}
}

// watchLoop processes fsnotify events and notifies relevant clients.
func watchLoop(watcher *fsnotify.Watcher, h *hub) {
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
				relPath, err := filepath.Rel(rootDir, event.Name)
				if err != nil {
					continue
				}
				h.notifyFile(relPath)
			}
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
		}
	}
}
