package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const backlinkSnippetsPerFile = 3

// BacklinkSnippet is one line in the source note that links to the target.
type BacklinkSnippet struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

// Backlink is one note that links to the target file.
type Backlink struct {
	Path     string            `json:"path"`
	Count    int               `json:"count"` // number of links to the target
	Modified time.Time         `json:"modified"`
	Snippets []BacklinkSnippet `json:"snippets"`
}

// BacklinksResponse is the body of GET /api/backlinks.
type BacklinksResponse struct {
	Path      string     `json:"path"`
	Backlinks []Backlink `json:"backlinks"`
}

var (
	// [text](dest) or ![alt](dest), with dest optionally in <angle brackets>.
	mdLinkRe     = regexp.MustCompile(`\]\(\s*(<[^>]*>|[^)\s]+)`)
	inlineCodeRe = regexp.MustCompile("`[^`]*`")
	urlSchemeRe  = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
)

// wikiResolver resolves Obsidian wiki-link targets to file paths using the
// same rules as the frontend's resolveWikiTarget, so backlinks agree with
// where links actually go when clicked.
type wikiResolver struct {
	files  map[string]bool
	byName map[string][]string // lower-cased basename and basename-without-.md
}

func newWikiResolver(files []string) *wikiResolver {
	r := &wikiResolver{files: map[string]bool{}, byName: map[string][]string{}}
	for _, f := range files {
		r.files[f] = true
		base := strings.ToLower(path.Base(f))
		noExt := base
		if strings.HasSuffix(noExt, ".md") {
			noExt = strings.TrimSuffix(noExt, ".md")
		}
		r.byName[noExt] = append(r.byName[noExt], f)
		if base != noExt {
			r.byName[base] = append(r.byName[base], f)
		}
	}
	return r
}

func (r *wikiResolver) resolve(target, sourceDir string) string {
	t := strings.TrimSpace(target)
	if t == "" {
		return ""
	}
	if r.files[t] {
		return t
	}
	if r.files[t+".md"] {
		return t + ".md"
	}
	base := strings.ToLower(path.Base(t))
	candidates := r.byName[base]
	if len(candidates) == 0 {
		candidates = r.byName[strings.TrimSuffix(base, ".md")]
	}
	switch len(candidates) {
	case 0:
		return ""
	case 1:
		return candidates[0]
	}
	for _, c := range candidates {
		if dirOf(c) == sourceDir {
			return c
		}
	}
	best := candidates[0]
	for _, c := range candidates[1:] {
		if len(c) < len(best) || (len(c) == len(best) && c < best) {
			best = c
		}
	}
	return best
}

// dirOf returns the slash-separated directory of a relative path ("" for root).
func dirOf(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}

// resolveMarkdownLink resolves a standard markdown link destination found in
// sourceDir to a root-relative path. ok is false for external URLs, pure
// fragments, and paths escaping the root.
func resolveMarkdownLink(dest, sourceDir string) (string, bool) {
	dest = strings.TrimSuffix(strings.TrimPrefix(dest, "<"), ">")
	if dest == "" || strings.HasPrefix(dest, "#") || strings.HasPrefix(dest, "//") || urlSchemeRe.MatchString(dest) {
		return "", false
	}
	if i := strings.IndexAny(dest, "#?"); i >= 0 {
		dest = dest[:i]
	}
	if unescaped, err := url.PathUnescape(dest); err == nil {
		dest = unescaped
	}
	var p string
	if strings.HasPrefix(dest, "/") {
		p = path.Clean(strings.TrimLeft(dest, "/"))
	} else {
		p = path.Join(sourceDir, dest)
	}
	if p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return "", false
	}
	return p, true
}

func isMarkdownPath(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".md" || ext == ".markdown"
}

// findBacklinks returns the links to target found in one source note.
// Links inside fenced code blocks and inline code spans are ignored.
func findBacklinks(source string, data []byte, target string, resolver *wikiResolver, wikiLinks bool) (count int, snippets []BacklinkSnippet) {
	sourceDir := dirOf(source)
	inFence := false
	fenceMarker := ""
	for i, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimLeft(line, " ")
		if len(line)-len(trimmed) <= 3 && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
			marker := trimmed[:3]
			if !inFence {
				inFence, fenceMarker = true, marker
			} else if marker == fenceMarker {
				inFence = false
			}
			continue
		}
		if inFence {
			continue
		}
		// Blank out inline code (same length, so offsets still line up).
		scan := inlineCodeRe.ReplaceAllStringFunc(line, func(s string) string { return strings.Repeat(" ", len(s)) })

		hits, firstAt := 0, -1
		note := func(at int) {
			hits++
			if firstAt < 0 || at < firstAt {
				firstAt = at
			}
		}
		if wikiLinks {
			for _, m := range wikiLinkRe.FindAllStringSubmatchIndex(scan, -1) {
				inner := strings.ReplaceAll(scan[m[2]:m[3]], `\|`, `|`)
				t, _, _ := strings.Cut(inner, "|")
				t, _, _ = strings.Cut(t, "#")
				if resolver.resolve(t, sourceDir) == target {
					note(m[0])
				}
			}
		}
		for _, m := range mdLinkRe.FindAllStringSubmatchIndex(scan, -1) {
			if p, ok := resolveMarkdownLink(scan[m[2]:m[3]], sourceDir); ok && p == target {
				note(m[0])
			}
		}
		if hits == 0 {
			continue
		}
		count += hits
		if len(snippets) < backlinkSnippetsPerFile {
			snippets = append(snippets, BacklinkSnippet{Line: i + 1, Text: snippetAround(line, firstAt)})
		}
	}
	return count, snippets
}

// handleBacklinks lists markdown notes that link to ?path=, most recently
// modified first.
func handleBacklinks(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimPrefix(path.Clean("/"+r.URL.Query().Get("path")), "/")
	if target == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	if _, err := safePath(target); err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	files := listFiles()
	resolver := newWikiResolver(files)
	wikiLinks := isObsidianVault

	var sources []string
	for _, f := range files {
		if f != target && isMarkdownPath(f) {
			sources = append(sources, f)
		}
	}

	backlinks := parallelMap(r.Context(), sources, func(src string) (Backlink, bool) {
		abs := filepath.Join(rootDir, filepath.FromSlash(src))
		info, err := os.Stat(abs)
		if err != nil || !info.Mode().IsRegular() || info.Size() > searchMaxFileSize {
			return Backlink{}, false
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return Backlink{}, false
		}
		count, snippets := findBacklinks(src, data, target, resolver, wikiLinks)
		if count == 0 {
			return Backlink{}, false
		}
		return Backlink{Path: src, Count: count, Modified: info.ModTime(), Snippets: snippets}, true
	})

	sort.Slice(backlinks, func(i, j int) bool {
		a, b := backlinks[i], backlinks[j]
		if !a.Modified.Equal(b.Modified) {
			return a.Modified.After(b.Modified)
		}
		return a.Path < b.Path
	})
	if backlinks == nil {
		backlinks = []Backlink{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(BacklinksResponse{Path: target, Backlinks: backlinks})
}
