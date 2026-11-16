package main

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

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
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"gopkg.in/yaml.v3"
)

//go:embed static/index.html static/icons/*.png
var staticFiles embed.FS

var version = "dev"

var rootDir string

var isObsidianVault bool

// Editor configuration for the edit button feature.
var editorCommands = map[string]struct {
	Name    string
	Command string
}{
	"vscode":  {"VS Code", "code"},
	"cursor":  {"Cursor", "cursor"},
	"sublime": {"Sublime Text", "subl"},
	"zed":     {"Zed", "zed"},
}

var editorSetupURLs = map[string]string{
	"vscode":  "https://code.visualstudio.com/docs/editor/command-line",
	"cursor":  "https://cursor.com/docs/cli/installation",
	"sublime": "https://www.sublimetext.com/docs/command_line.html",
	"zed":     "https://zed.dev/features#cli",
}

// TreeEntry represents a single item in the file tree.
type TreeEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsDir     bool   `json:"isDir"`
	Extension string `json:"extension,omitempty"`
	Size      int64  `json:"size"`
	ModTime   int64  `json:"modTime"`
}

func main() {
	// Handle subcommands before flag parsing
	if len(os.Args) > 1 && os.Args[1] == "update" {
		runUpdate(os.Args[2:])
		return
	}

	host := flag.String("host", "127.0.0.1", "host/IP to bind to")
	port := flag.Int("port", 7777, "port to serve on")
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

	// Check if this is an Obsidian vault
	if _, err := os.Stat(filepath.Join(rootDir, ".obsidian")); err == nil {
		isObsidianVault = true
		log.Println("Obsidian vault detected, enabling wiki links")
	}

	cfg, err := serverConfigFromEnv(rootDir)
	if err != nil {
		log.Fatal(err)
	}
	app := newServer(cfg)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()
	app.watch(watcher)

	// Find an available port (auto-increment if default port is taken)
	actualPort := *port
	if !portExplicitlySet {
		actualPort = findAvailablePort(*host, *port, 100)
	}

	addr := fmt.Sprintf("%s:%d", *host, actualPort)
	url := fmt.Sprintf("http://localhost:%d", actualPort)
	fmt.Printf("RepoView v%s · %s\n→ http://%s\n", version, rootDir, addr)
	if app.authEnabled() {
		fmt.Println("Password login on; share links enabled")
	} else if !isLoopbackHost(*host) {
		fmt.Println("WARNING: listening beyond localhost without a password; set REPOVIEW_PASSWORD")
	}
	fmt.Print("\nPress Ctrl+C to stop\n")

	if !*noBrowser {
		_ = browser.OpenURL(url)
	}

	log.Fatal(newHTTPServer(addr, app.handler()).ListenAndServe())
}

// rawPrefix is the path-based route for raw file bytes: /_raw/<relative/path>.
// HTML previews load from it so relative URLs inside a page (img, link,
// script, CSS url(), <a href>) resolve against the file's own folder. The
// leading underscore keeps it from shadowing a real top-level "raw/" folder,
// whose files are reachable at the SPA's clean URL /raw/<file>.
const rawPrefix = "/_raw/"

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

func prepareIndexHTML(raw []byte, rootName string) []byte {
	return renderIndex(raw, rootName, pageConfig{Mode: "local"})
}

// safePath resolves a request path within rootDir and rejects traversal.
func safePath(reqPath string) (string, error) {
	return scope{root: rootDir}.resolve(reqPath)
}

// handleTree returns immediate children of a directory as JSON.
func handleTree(w http.ResponseWriter, r *http.Request) {
	reqPath := r.URL.Query().Get("path")
	if reqPath == "" {
		reqPath = "."
	}

	sc := scopeOf(r)
	dirPath, err := sc.resolve(reqPath)
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
		relPath, _ := filepath.Rel(sc.root, filepath.Join(dirPath, name))
		relPath = filepath.ToSlash(relPath)
		if (sc.restricted || sc.only != "") && !sc.visible(relPath) {
			continue
		}
		ext := ""
		if !e.IsDir() {
			ext = strings.TrimPrefix(filepath.Ext(name), ".")
		}
		var size int64
		var modTime int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
			modTime = info.ModTime().UnixMilli()
		}
		result = append(result, TreeEntry{
			Name:      name,
			Path:      relPath,
			IsDir:     e.IsDir(),
			Extension: ext,
			Size:      size,
			ModTime:   modTime,
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
	IsHTML     bool   `json:"isHTML,omitempty"`
	PreviewURL string `json:"previewURL,omitempty"` // HTML: where the sandboxed preview loads from
	IsPDF      bool   `json:"isPDF,omitempty"`
	IsBinary   bool   `json:"isBinary,omitempty"`
	IsJSONL    bool   `json:"isJSONL,omitempty"`
	IsWord     bool   `json:"isWord,omitempty"`
	IsTooLarge bool   `json:"isTooLarge,omitempty"`
	MimeType   string `json:"mimeType,omitempty"`
	Size       int64  `json:"size,omitempty"`
}

// JSONLResponse represents paginated JSONL data.
type JSONLResponse struct {
	Records     []string `json:"records"`
	TotalLines  int64    `json:"totalLines"`
	Page        int      `json:"page"`
	PageSize    int      `json:"pageSize"`
	HasMore     bool     `json:"hasMore"`
	ParseErrors []string `json:"parseErrors,omitempty"`
}

// handleFile returns rendered file content as JSON.
func handleFile(w http.ResponseWriter, r *http.Request) {
	reqPath := r.URL.Query().Get("path")
	if reqPath == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}

	filePath, err := scopeOf(r).resolve(reqPath)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	// Get file extension early to avoid reading large files unnecessarily.
	ext := strings.ToLower(filepath.Ext(filePath))

	// Detect PDF files and return metadata for frontend rendering.
	if ext == ".pdf" {
		resp := FileResponse{
			Name:     filepath.Base(filePath),
			Path:     reqPath,
			IsPDF:    true,
			MimeType: "application/pdf",
			Size:     info.Size(),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	// Detect Word (.docx) documents; the frontend renders them from /api/raw.
	if ext == ".docx" {
		resp := FileResponse{
			Name:     filepath.Base(filePath),
			Path:     reqPath,
			IsWord:   true,
			MimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			Size:     info.Size(),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	// Detect JSONL files and return metadata for frontend pagination.
	if ext == ".jsonl" {
		resp := FileResponse{
			Name:     filepath.Base(filePath),
			Path:     reqPath,
			IsJSONL:  true,
			MimeType: "application/x-ndjson",
			Size:     info.Size(),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	if info.Size() > maxRenderSize {
		resp := FileResponse{
			Name:       filepath.Base(filePath),
			Path:       reqPath,
			IsTooLarge: true,
			Size:       info.Size(),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
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

	isMarkdown := ext == ".md" || ext == ".markdown"
	isCSV := ext == ".csv"
	isSVG := ext == ".svg"
	isHTML := ext == ".html" || ext == ".htm"

	var buf bytes.Buffer
	switch {
	case isMarkdown:
		renderMarkdown(&buf, data)
	case isCSV:
		renderCSV(&buf, data)
	case isHTML:
		// HTML files render via /api/raw in an iframe; no server-side rendering needed.
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
		IsHTML:     isHTML,
	}

	if isCSV {
		resp.RawCSV = string(data)
	}

	if isMarkdown || isSVG || isHTML {
		var rawBuf bytes.Buffer
		renderCode(&rawBuf, data, filepath.Base(filePath))
		resp.RawContent = rawBuf.String()
	}

	if isSVG {
		resp.RawSVG = string(data)
	}

	if isHTML {
		// Path relative to the scope, so relative URLs inside the page
		// resolve within it.
		if rel, err := filepath.Rel(scopeOf(r).root, filePath); err == nil {
			resp.PreviewURL = previewURLFor(r, filepath.ToSlash(rel))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// parseFrontmatter extracts YAML frontmatter key-value pairs from markdown data.
// It returns the pairs and the remaining body. If no valid frontmatter is found,
// it returns nil and the original data unchanged.
func parseFrontmatter(data []byte) ([][2]string, []byte) {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
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

// wikiLinkRe matches Obsidian wiki links: [[target]], [[target|display]], [[target#heading]], [[target#heading|display]]
// The inner content may contain \| (an escaped pipe used inside markdown tables); splitting on the
// alias separator and unescaping is done in preprocessWikiLinks rather than the regex itself.
var wikiLinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

var wikiImageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".svg": true, ".bmp": true, ".tiff": true, ".tif": true, ".avif": true,
}

// preprocessWikiLinks converts Obsidian wiki link syntax to HTML placeholders for frontend resolution.
func preprocessWikiLinks(data []byte) []byte {
	return wikiLinkRe.ReplaceAllFunc(data, func(match []byte) []byte {
		// Inside a markdown table, the alias separator is written as \| to avoid colliding with
		// the column separator. Treat \| identically to | by unescaping before splitting.
		inner := strings.ReplaceAll(string(match[2:len(match)-2]), `\|`, `|`)

		targetPart, alias, hasAlias := strings.Cut(inner, "|")

		heading := ""
		target := targetPart
		if idx := strings.IndexByte(targetPart, '#'); idx >= 0 {
			target = targetPart[:idx]
			heading = targetPart[idx+1:]
		}

		// Obsidian renders image wiki-links as <img> elements.
		if wikiImageExts[strings.ToLower(filepath.Ext(target))] {
			altText := strings.TrimSuffix(filepath.Base(target), filepath.Ext(target))
			result := fmt.Sprintf(`<img class="wiki-image" data-wiki-target="%s"`, html.EscapeString(target))
			if hasAlias && alias != "" {
				// Alias may be a width ("200"), WxH ("200x300"), or alt text.
				if w, err := strconv.Atoi(alias); err == nil {
					result += fmt.Sprintf(` width="%d"`, w)
				} else if parts := strings.SplitN(alias, "x", 2); len(parts) == 2 {
					if w, err1 := strconv.Atoi(parts[0]); err1 == nil {
						if h, err2 := strconv.Atoi(parts[1]); err2 == nil {
							result += fmt.Sprintf(` width="%d" height="%d"`, w, h)
						} else {
							altText = alias
						}
					} else {
						altText = alias
					}
				} else {
					altText = alias
				}
			}
			result += fmt.Sprintf(` alt="%s">`, html.EscapeString(altText))
			return []byte(result)
		}

		display := target
		if hasAlias {
			display = alias
		}

		// Build the placeholder anchor tag with href="#" so it's clickable
		result := fmt.Sprintf(`<a class="wiki-link" href="#" data-wiki-target="%s"`,
			html.EscapeString(target))
		if heading != "" {
			result += fmt.Sprintf(` data-wiki-heading="%s"`, html.EscapeString(heading))
		}
		result += fmt.Sprintf(`>%s</a>`, html.EscapeString(display))
		return []byte(result)
	})
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

	rendererOpts := []renderer.Option{gmhtml.WithHardWraps()}
	if isObsidianVault {
		body = preprocessWikiLinks(body)
		rendererOpts = append(rendererOpts, gmhtml.WithUnsafe())
	}
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			safeAutoLinks{},
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			rendererOpts...,
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
// Kept for backwards compatibility; the frontend uses /_raw/<path>.
func handleRaw(w http.ResponseWriter, r *http.Request) {
	reqPath := r.URL.Query().Get("path")
	if reqPath == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}
	serveRawFile(w, r, reqPath)
}

// handleRawPath serves raw file bytes at /_raw/<relative/path>.
func handleRawPath(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, rawPrefix)
	if rel == "" {
		http.NotFound(w, r)
		return
	}
	serveRawFile(w, r, rel)
}

// serveRawFile serves the file at rel, relative to the request's scope.
// Traversal is rejected with 400; missing files, directories and symlinks
// resolving outside the scope root are 404.
func serveRawFile(w http.ResponseWriter, r *http.Request, rel string) {
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == ".." {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
	}
	sc := scopeOf(r)
	filePath, err := sc.resolve(rel)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	real, err := realPathInRoot(sc.root, filePath)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	f, err := os.Open(real)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	// ServeContent, not ServeFile: ServeFile redirects ".../index.html" to
	// the directory, which would break previews of index.html files. The
	// sandboxWriter sandboxes anything whose final type can run script.
	http.ServeContent(newSandboxWriter(w, r), r, info.Name(), info.ModTime(), f)
}

// realPathInRoot resolves symlinks in p and checks the result is still inside
// the (symlink-resolved) root directory.
func realPathInRoot(rootPath, p string) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return "", err
	}
	if real != root && !strings.HasPrefix(real, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path outside root")
	}
	return real, nil
}

// handleFiles returns a flat list of all file paths for fuzzy search.
func handleFiles(w http.ResponseWriter, r *http.Request) {
	files := scopeOf(r).files()
	if files == nil {
		files = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

// listFiles returns all file paths under rootDir (slash-separated, relative).
func listFiles() []string {
	return listFilesIn(rootDir)
}

// EditorInfo is returned by the /api/editors endpoint.
type EditorInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
	SetupURL  string `json:"setupUrl,omitempty"`
}

// handleEditors returns a list of all editors with availability status.
func handleEditors(w http.ResponseWriter, r *http.Request) {
	var editors []EditorInfo
	for id, info := range editorCommands {
		_, err := exec.LookPath(info.Command)
		editor := EditorInfo{
			ID:        id,
			Name:      info.Name,
			Available: err == nil,
		}
		if err != nil {
			editor.SetupURL = editorSetupURLs[id]
		}
		editors = append(editors, editor)
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
	scope   scope       // what this client may watch; zero value = whole root
	alive   func() bool // nil, or false once the client's share is gone
}

func (c *wsClient) scopeOrRoot() scope {
	if c.scope.root == "" {
		return scope{root: rootDir}
	}
	return c.scope
}

func newHub() *hub {
	return &hub{clients: make(map[*wsClient]bool)}
}

func (h *hub) run() {
	// Hub doesn't need a goroutine loop; broadcast is called directly.
}

func (h *hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// maxWatched is the largest watch list of any client (for tests).
func (h *hub) maxWatched() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for c := range h.clients {
		c.mu.Lock()
		n = max(n, len(c.watched))
		c.mu.Unlock()
	}
	return n
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

// sweep disconnects clients whose share was revoked or expired.
func (h *hub) sweep() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.alive != nil && !c.alive() {
			c.conn.Close() // readPump then unregisters
		}
	}
}

// notifyFile sends a change notification for a path relative to rootDir.
func (h *hub) notifyFile(relPath string) {
	h.notifyAbs(filepath.Join(rootDir, filepath.FromSlash(relPath)))
}

// notifyAbs sends a change notification to clients watching a path or any
// of its parent directories. Each client gets the path relative to its own
// scope; clients whose scope doesn't include it hear nothing. Clients whose
// share was revoked or expired are disconnected.
func (h *hub) notifyAbs(absPath string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.alive != nil && !c.alive() {
			c.conn.Close() // readPump then unregisters
			continue
		}
		sc := c.scopeOrRoot()
		rel, err := filepath.Rel(sc.root, absPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		relPath := filepath.ToSlash(rel)
		if (sc.restricted || sc.only != "") && relPath != "." && !sc.visible(relPath) {
			continue
		}
		msg, _ := json.Marshal(map[string]string{"type": "change", "path": relPath})
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

// Browsers attach cookies to cross-site websocket handshakes, so only accept
// connections from our own pages.
var upgrader = websocket.Upgrader{
	CheckOrigin: sameOrigin,
}

func handleWS(w http.ResponseWriter, r *http.Request, h *hub, watcher *fsnotify.Watcher) {
	handleWSScoped(w, r, h, watcher, nil)
}

// Websocket limits: clients only send small watch/unwatch messages.
const (
	maxWSClients        = 256
	maxWatchesPerClient = 256
	maxWSMessage        = 4096
)

// handleWSScoped upgrades the connection; the client may only watch paths
// inside the request's scope.
func handleWSScoped(w http.ResponseWriter, r *http.Request, h *hub, watcher *fsnotify.Watcher, alive func() bool) {
	if h.count() >= maxWSClients {
		http.Error(w, "too many live-reload connections", http.StatusServiceUnavailable)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &wsClient{
		conn:    conn,
		hub:     h,
		send:    make(chan []byte, 64),
		watched: make(map[string]bool),
		scope:   scopeOf(r),
		alive:   alive,
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
	c.conn.SetReadLimit(maxWSMessage)
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

		absPath, err := c.scopeOrRoot().resolve(req.Path)
		if err != nil {
			continue
		}

		c.mu.Lock()
		switch req.Action {
		case "watch":
			// Only existing paths, and a bounded number of them.
			if _, err := os.Stat(absPath); err != nil || (!c.watched[req.Path] && len(c.watched) >= maxWatchesPerClient) {
				break
			}
			c.watched[req.Path] = true
			if watcher != nil {
				_ = watcher.Add(absPath)
			}
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
				h.notifyAbs(event.Name)
			}
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

// handleJSONL serves paginated JSONL data.
func handleJSONL(w http.ResponseWriter, r *http.Request) {
	reqPath := r.URL.Query().Get("path")
	if reqPath == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}

	// Parse pagination parameters.
	page := 1
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	pageSize := 100 // Default page size
	if sizeStr := r.URL.Query().Get("pageSize"); sizeStr != "" {
		if s, err := strconv.Atoi(sizeStr); err == nil && s > 0 && s <= 500 {
			pageSize = s
		}
	}

	filePath, err := scopeOf(r).resolve(reqPath)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	// Paging reads the file from the start each time; bound concurrent reads.
	if !acquireHeavy(w, r) {
		return
	}
	defer releaseHeavy()

	// Count total lines first.
	totalLines, err := countJSONLLines(filePath)
	if err != nil {
		http.Error(w, "cannot read file", http.StatusInternalServerError)
		return
	}

	// Read paginated records.
	records, parseErrors, err := readJSONLPage(filePath, page, pageSize)
	if err != nil {
		http.Error(w, "cannot read file", http.StatusInternalServerError)
		return
	}

	resp := JSONLResponse{
		Records:     records,
		TotalLines:  totalLines,
		Page:        page,
		PageSize:    pageSize,
		HasMore:     int64(page*pageSize) < totalLines,
		ParseErrors: parseErrors,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// countJSONLLines efficiently counts lines in a file.
func countJSONLLines(filePath string) (int64, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	var count int64
	scanner := bufio.NewScanner(file)
	// Use larger buffer for better performance with large lines.
	buf := make([]byte, 1024*1024)    // 1MB buffer
	scanner.Buffer(buf, 10*1024*1024) // Max 10MB per line

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && line != "{}" { // Skip empty lines and empty objects
			count++
		}
	}

	if err := scanner.Err(); err != nil {
		return 0, err
	}

	return count, nil
}

// readJSONLPage reads a specific page of JSONL records.
func readJSONLPage(filePath string, page, pageSize int) ([]string, []string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var records []string
	var parseErrors []string

	startIdx := (page - 1) * pageSize
	endIdx := page * pageSize
	currentIdx := 0
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		if line == "" || line == "{}" {
			continue
		}

		if currentIdx >= startIdx && currentIdx < endIdx {
			// Validate JSON.
			var v interface{}
			if err := json.Unmarshal([]byte(line), &v); err != nil {
				parseErrors = append(parseErrors, fmt.Sprintf("Line %d: %s", lineNum, err.Error()))
				// Include the malformed line with error marker.
				records = append(records, line)
			} else {
				// Pretty-print the JSON for better display.
				prettyJSON, err := json.MarshalIndent(v, "", "  ")
				if err != nil {
					records = append(records, line)
				} else {
					records = append(records, string(prettyJSON))
				}
			}
		}

		currentIdx++

		if currentIdx >= endIdx {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}

	return records, parseErrors, nil
}

// maxRenderSize: larger files are not read or rendered by /api/file (a 20 MB
// source file took ~3.5 GB to highlight); the frontend offers a download.
const maxRenderSize = 2 << 20

// newHTTPServer bounds slow or oversized requests. No write timeout: raw
// downloads and websockets are long-lived by design.
func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
}
