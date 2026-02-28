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

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
	"github.com/pkg/browser"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"gopkg.in/yaml.v3"
)

//go:embed static/index.html
var staticFiles embed.FS

var version = "dev"

var rootDir string

// TreeEntry represents a single item in the file tree.
type TreeEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsDir     bool   `json:"isDir"`
	Extension string `json:"extension,omitempty"`
}

func main() {
	host := flag.String("host", "127.0.0.1", "host/IP to bind to")
	port := flag.Int("port", 8080, "port to serve on")
	noBrowser := flag.Bool("no-browser", false, "don't open browser on startup")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("repoview v" + version)
		os.Exit(0)
	}

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
	http.HandleFunc("/api/raw", handleRaw)
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleWS(w, r, hub, watcher)
	})

	// Serve index.html for all non-API paths (SPA catch-all).
	staticSub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}
	indexHTML, err := fs.ReadFile(staticSub, "index.html")
	if err != nil {
		log.Fatal(err)
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})

	addr := fmt.Sprintf("%s:%d", *host, *port)
	url := fmt.Sprintf("http://localhost:%d", *port)
	fmt.Printf("repoview v%s serving %s at http://%s\n", version, rootDir, addr)

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
	RawContent string `json:"rawContent,omitempty"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	IsMarkdown bool   `json:"isMarkdown"`
	IsCSV      bool   `json:"isCSV"`
	IsSVG      bool   `json:"isSVG,omitempty"`
	RawSVG     string `json:"rawSVG,omitempty"`
	IsBinary   bool   `json:"isBinary,omitempty"`
	MimeType   string `json:"mimeType,omitempty"`
	Size       int64  `json:"size,omitempty"`
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

	// Detect binary files and return metadata instead of content.
	if isBinaryContent(data) {
		resp := FileResponse{
			Name:     filepath.Base(filePath),
			Path:     reqPath,
			IsBinary: true,
			MimeType: http.DetectContentType(data),
			Size:     info.Size(),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	isMarkdown := ext == ".md" || ext == ".markdown"
	isCSV := ext == ".csv"
	isSVG := ext == ".svg"

	var buf bytes.Buffer
	switch {
	case isMarkdown:
		renderMarkdown(&buf, data)
	case isCSV:
		renderCSV(&buf, data)
	default:
		data = prettyPrint(data, ext)
		renderCode(&buf, data, filepath.Base(filePath))
	}

	resp := FileResponse{
		Content:    buf.String(),
		Name:       filepath.Base(filePath),
		Path:       reqPath,
		IsMarkdown: isMarkdown,
		IsCSV:      isCSV,
		IsSVG:      isSVG,
	}

	if isMarkdown || isSVG {
		var rawBuf bytes.Buffer
		renderCode(&rawBuf, data, filepath.Base(filePath))
		resp.RawContent = rawBuf.String()
	}

	if isSVG {
		resp.RawSVG = string(data)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// parseFrontmatter extracts YAML frontmatter key-value pairs from markdown data.
// It returns the pairs and the remaining body. If no valid frontmatter is found,
// it returns nil and the original data unchanged.
func parseFrontmatter(data []byte) ([][2]string, []byte) {
	s := string(data)
	if !strings.HasPrefix(s, "---\n") {
		return nil, data
	}
	end := strings.Index(s[4:], "\n---\n")
	if end < 0 {
		return nil, data
	}
	block := s[4 : 4+end]
	body := []byte(s[4+end+5:])

	var pairs [][2]string
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if key != "" {
			pairs = append(pairs, [2]string{key, val})
		}
	}
	if len(pairs) == 0 {
		return nil, data
	}
	return pairs, body
}

// renderMarkdown converts markdown bytes to HTML using goldmark with GFM.
func renderMarkdown(w io.Writer, data []byte) {
	pairs, body := parseFrontmatter(data)

	if len(pairs) > 0 {
		fmt.Fprint(w, "<table><thead><tr><th>Key</th><th>Value</th></tr></thead><tbody>")
		for _, p := range pairs {
			fmt.Fprintf(w, "<tr><td>%s</td><td>%s</td></tr>",
				html.EscapeString(p[0]), html.EscapeString(p[1]))
		}
		fmt.Fprint(w, "</tbody></table>")
	}

	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
	)
	var buf bytes.Buffer
	if err := md.Convert(body, &buf); err != nil {
		fmt.Fprintf(w, "<pre>%s</pre>", html.EscapeString(string(body)))
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

// renderCode syntax-highlights code using chroma and writes HTML to w.
// Falls back to plain <pre> on any error or when no lexer matches.
func renderCode(w io.Writer, data []byte, filename string) {
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Analyse(string(data))
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	style := styles.Get("github")
	formatter := chromahtml.New(chromahtml.WithLineNumbers(true))

	iterator, err := lexer.Tokenise(nil, string(data))
	if err != nil {
		fmt.Fprintf(w, "<pre>%s</pre>", html.EscapeString(string(data)))
		return
	}
	if err := formatter.Format(w, style, iterator); err != nil {
		fmt.Fprintf(w, "<pre>%s</pre>", html.EscapeString(string(data)))
		return
	}
}

// prettyPrint re-formats JSON and YAML content with proper indentation.
// If parsing fails, the original data is returned unchanged.
func prettyPrint(data []byte, ext string) []byte {
	switch ext {
	case ".json":
		var v any
		if err := json.Unmarshal(data, &v); err == nil {
			if pretty, err := json.MarshalIndent(v, "", "  "); err == nil {
				return pretty
			}
		}
	case ".yaml", ".yml":
		var v any
		if err := yaml.Unmarshal(data, &v); err == nil {
			if pretty, err := yaml.Marshal(v); err == nil {
				return pretty
			}
		}
	}
	return data
}

// isBinaryContent returns true if the data appears to be binary (non-text).
func isBinaryContent(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	sample := data
	if len(sample) > 512 {
		sample = sample[:512]
	}
	mime := http.DetectContentType(sample)
	if strings.HasPrefix(mime, "text/") {
		return false
	}
	for _, t := range []string{"application/json", "application/xml", "application/javascript"} {
		if strings.HasPrefix(mime, t) {
			return false
		}
	}
	return true
}

// handleRaw serves raw file bytes with proper Content-Type for inline display.
func handleRaw(w http.ResponseWriter, r *http.Request) {
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

	http.ServeFile(w, r, filePath)
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
