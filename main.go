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
	"net"
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

// Editor configuration for the edit button feature.
var editorCommands = map[string]struct {
	Name    string
	Command string
}{
	"vscode":  {"VS Code", "code"},
	"cursor":  {"Cursor", "cursor"},
	"sublime": {"Sublime Text", "subl"},
	"zed":     {"Zed", "zed"},
	"idea":    {"IntelliJ IDEA", "idea"},
	"vim":     {"Vim (Terminal)", "vim"},
}

var editorSetupURLs = map[string]string{
	"vscode":  "https://code.visualstudio.com/docs/setup/mac#_launching-from-the-command-line",
	"cursor":  "https://docs.cursor.com/get-started/install#command-line",
	"sublime": "https://www.sublimetext.com/docs/command_line.html",
	"zed":     "https://zed.dev/docs/getting-started#command-palette",
	"idea":    "https://www.jetbrains.com/help/idea/working-with-the-ide-features-from-command-line.html",
}

// TreeEntry represents a single item in the file tree.
type TreeEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsDir     bool   `json:"isDir"`
	Extension string `json:"extension,omitempty"`
}

func main() {
	// Handle subcommands before flag parsing
	if len(os.Args) > 1 && os.Args[1] == "update" {
		runUpdate(os.Args[2:])
		return
	}

	host := flag.String("host", "127.0.0.1", "host/IP to bind to")
	port := flag.Int("port", 8080, "port to serve on")
	noBrowser := flag.Bool("no-browser", false, "don't open browser on startup")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	// Check if port was explicitly set by the user
	portExplicitlySet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			portExplicitlySet = true
		}
	})

	if *showVersion {
		fmt.Println("repoview v" + version)
		os.Exit(0)
	}

	// Check for updates in background (at most once per day)
	maybeCheckForUpdates()

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
	http.HandleFunc("/api/editors", handleEditors)
	http.HandleFunc("/api/open", handleOpen)
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

	// Find an available port (auto-increment if default port is taken)
	actualPort := *port
	if !portExplicitlySet {
		actualPort = findAvailablePort(*host, *port, 100)
	}

	addr := fmt.Sprintf("%s:%d", *host, actualPort)
	url := fmt.Sprintf("http://localhost:%d", actualPort)
	fmt.Printf("RepoView v%s · %s\n→ http://%s\n\nPress Ctrl+C to stop\n", version, rootDir, addr)

	if !*noBrowser {
		_ = browser.OpenURL(url)
	}

	log.Fatal(http.ListenAndServe(addr, nil))
}

// findAvailablePort tries to find an available port starting from startPort.
// It tries up to maxAttempts ports, incrementing by 1 each time.
// Returns the first available port, or startPort + maxAttempts - 1 if none found.
func findAvailablePort(host string, startPort, maxAttempts int) int {
	for i := 0; i < maxAttempts; i++ {
		port := startPort + i
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
		if err == nil {
			ln.Close()
			return port
		}
	}
	return startPort + maxAttempts - 1
}

// safePath resolves a request path within rootDir and rejects traversal.
func safePath(reqPath string) (string, error) {
	cleaned := filepath.Clean(reqPath)
	full := filepath.Join(rootDir, cleaned)
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	// Ensure path is exactly rootDir or inside rootDir (not a sibling with similar prefix)
	if abs != rootDir && !strings.HasPrefix(abs, rootDir+string(os.PathSeparator)) {
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
	RawCSV     string `json:"rawCSV,omitempty"`
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

	if isCSV {
		resp.RawCSV = string(data)
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
	fmt.Fprint(w, `<th class="csv-row-link-header"></th>`)
	fmt.Fprint(w, "</tr></thead><tbody>")
	for rowIdx, row := range records[1:] {
		fmt.Fprint(w, "<tr>")
		for _, cell := range row {
			fmt.Fprintf(w, "<td>%s</td>", html.EscapeString(cell))
		}
		fmt.Fprintf(w, `<td class="csv-row-link"><button class="csv-record-link-btn" data-row="%d" title="View record">→</button></td>`, rowIdx+1)
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

// EditorInfo is returned by the /api/editors endpoint.
type EditorInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// handleEditors returns a list of installed editors.
func handleEditors(w http.ResponseWriter, r *http.Request) {
	var editors []EditorInfo
	for id, info := range editorCommands {
		if _, err := exec.LookPath(info.Command); err == nil {
			editors = append(editors, EditorInfo{ID: id, Name: info.Name})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(editors)
}

// OpenResponse is returned by the /api/open endpoint.
type OpenResponse struct {
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
	SetupURL string `json:"setupUrl,omitempty"`
}

// handleOpen opens a file in the specified editor.
func handleOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	reqPath := r.URL.Query().Get("path")
	editorID := r.URL.Query().Get("editor")

	if reqPath == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(OpenResponse{Success: false, Error: "path required"})
		return
	}
	if editorID == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(OpenResponse{Success: false, Error: "editor required"})
		return
	}

	editor, ok := editorCommands[editorID]
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(OpenResponse{Success: false, Error: "unknown editor"})
		return
	}

	filePath, err := safePath(reqPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(OpenResponse{Success: false, Error: "invalid path"})
		return
	}

	// Verify file exists
	if _, err := os.Stat(filePath); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(OpenResponse{Success: false, Error: "file not found"})
		return
	}

	// Try to run the editor command
	cmd := exec.Command(editor.Command, filePath)
	if err := cmd.Start(); err != nil {
		setupURL := editorSetupURLs[editorID]
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(OpenResponse{
			Success:  false,
			Error:    fmt.Sprintf("Could not open in %s. Make sure the '%s' CLI command is set up.", editor.Name, editor.Command),
			SetupURL: setupURL,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(OpenResponse{Success: true})
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

// notifyFile sends a change notification to clients watching a given path
// or any of its parent directories.
func (h *hub) notifyFile(relPath string) {
	msg, _ := json.Marshal(map[string]string{"type": "change", "path": relPath})
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		c.mu.Lock()
		// Check if client is watching the exact path or any parent directory
		watching := c.watched[relPath]
		if !watching {
			// Check parent directories using filepath.Dir for cross-platform support
			p := relPath
			for {
				parent := filepath.Dir(p)
				if parent == p || parent == "." {
					// Reached root - check root directory (empty string or ".")
					if c.watched[""] || c.watched["."] {
						watching = true
					}
					break
				}
				p = parent
				if c.watched[p] {
					watching = true
					break
				}
			}
		}
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
