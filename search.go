package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	searchMaxFileSize     = 2 << 20 // skip files larger than 2 MB
	searchDefaultLimit    = 50
	searchMaxLimit        = 200
	searchSnippetsPerFile = 3
	searchSnippetMaxRunes = 160
	searchMinQueryRunes   = 2
)

// SearchSnippet is one matching line within a file.
type SearchSnippet struct {
	Line int    `json:"line"` // 1-based line number
	Text string `json:"text"` // the line (possibly trimmed around the match)
}

// SearchResult is one file that matches every term in the query.
type SearchResult struct {
	Path      string          `json:"path"`
	NameMatch bool            `json:"nameMatch"` // all terms appear in the file name
	Count     int             `json:"count"`     // number of matching lines
	Snippets  []SearchSnippet `json:"snippets"`
}

// SearchResponse is the body of GET /api/search.
type SearchResponse struct {
	Query     string         `json:"query"`
	Terms     []string       `json:"terms"`
	Results   []SearchResult `json:"results"`
	Total     int            `json:"total"`     // matching files before the limit
	Truncated bool           `json:"truncated"` // Total > len(Results)
}

// parseSearchQuery splits a query into lower-cased terms. Double-quoted
// phrases are kept together ("weekly review" matches that exact phrase).
func parseSearchQuery(q string) []string {
	var terms []string
	seen := map[string]bool{}
	add := func(t string) {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && !seen[t] {
			seen[t] = true
			terms = append(terms, t)
		}
	}
	for {
		start := strings.IndexByte(q, '"')
		if start < 0 {
			break
		}
		end := strings.IndexByte(q[start+1:], '"')
		if end < 0 {
			break
		}
		for _, f := range strings.Fields(q[:start]) {
			add(f)
		}
		add(q[start+1 : start+1+end])
		q = q[start+1+end+1:]
	}
	for _, f := range strings.Fields(strings.ReplaceAll(q, `"`, " ")) {
		add(f)
	}
	return terms
}

// handleSearch performs a case-insensitive full-text search over text files.
// Every term must appear somewhere in the file's contents or path.
func handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit := searchDefaultLimit
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		limit = min(l, searchMaxLimit)
	}

	resp := SearchResponse{Query: q, Terms: parseSearchQuery(q), Results: []SearchResult{}}
	if utf8.RuneCountInString(strings.Join(resp.Terms, "")) >= searchMinQueryRunes {
		results := searchFiles(r.Context(), listFiles(), resp.Terms)
		resp.Total = len(results)
		if len(results) > limit {
			results = results[:limit]
			resp.Truncated = true
		}
		if results != nil {
			resp.Results = results
		}
	}
	if resp.Terms == nil {
		resp.Terms = []string{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// searchFiles scans files in parallel and returns ranked matches:
// file-name matches first, then by number of matching lines, then by path.
func searchFiles(ctx context.Context, files []string, terms []string) []SearchResult {
	if len(terms) == 0 {
		return nil
	}

	results := parallelMap(ctx, files, func(rel string) (SearchResult, bool) {
		return searchFile(rel, terms)
	})

	sort.Slice(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.NameMatch != b.NameMatch {
			return a.NameMatch
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Path < b.Path
	})
	return results
}

// searchFile checks a single file. ok is false when the file doesn't match,
// can't be read, is too large, or looks binary.
func searchFile(rel string, terms []string) (res SearchResult, ok bool) {
	abs := filepath.Join(rootDir, filepath.FromSlash(rel))
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() || info.Size() > searchMaxFileSize {
		return res, false
	}
	data, err := os.ReadFile(abs)
	if err != nil || isBinaryContent(data) {
		return res, false
	}

	lowerPath := strings.ToLower(rel)
	lowerName := path.Base(lowerPath)
	lowerData := bytes.ToLower(data)

	nameMatch := true
	for _, t := range terms {
		inPath := strings.Contains(lowerPath, t)
		if !inPath && !bytes.Contains(lowerData, []byte(t)) {
			return res, false
		}
		if !strings.Contains(lowerName, t) {
			nameMatch = false
		}
	}

	res = SearchResult{Path: rel, NameMatch: nameMatch, Snippets: []SearchSnippet{}}
	lineNo := 0
	for len(data) > 0 {
		lineNo++
		var line []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			line, data = data, nil
		}
		lowerLine := strings.ToLower(string(line))
		at := -1
		for _, t := range terms {
			if i := strings.Index(lowerLine, t); i >= 0 && (at < 0 || i < at) {
				at = i
			}
		}
		if at < 0 {
			continue
		}
		res.Count++
		if len(res.Snippets) < searchSnippetsPerFile {
			text := strings.TrimRight(string(line), "\r")
			// Byte offsets in the lower-cased line only map onto the original
			// when lower-casing didn't change the length.
			if len(lowerLine) != len(line) {
				at = 0
			}
			res.Snippets = append(res.Snippets, SearchSnippet{Line: lineNo, Text: snippetAround(text, at)})
		}
	}
	return res, true
}

// snippetAround trims a long line to searchSnippetMaxRunes, keeping the match
// at byte offset `at` visible with some leading context.
func snippetAround(line string, at int) string {
	trimmed := strings.TrimLeft(line, " \t")
	at -= len(line) - len(trimmed)
	line = strings.TrimRight(trimmed, " \t")
	runes := []rune(line)
	if len(runes) <= searchSnippetMaxRunes {
		return line
	}
	at = min(max(at, 0), len(line))
	for at > 0 && !utf8.RuneStart(line[at]) {
		at--
	}
	runeAt := utf8.RuneCountInString(line[:at])
	start := max(runeAt-40, 0)
	end := min(start+searchSnippetMaxRunes, len(runes))
	start = max(end-searchSnippetMaxRunes, 0)
	out := string(runes[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}

// parallelMap applies fn to every item using one worker per CPU and returns
// the results for which fn reported ok, in no particular order. It stops
// handing out work once ctx is cancelled (e.g. the client disconnected).
func parallelMap[T any](ctx context.Context, items []string, fn func(string) (T, bool)) []T {
	jobs := make(chan string)
	var mu sync.Mutex
	var out []T
	var wg sync.WaitGroup

	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				if v, ok := fn(item); ok {
					mu.Lock()
					out = append(out, v)
					mu.Unlock()
				}
			}
		}()
	}

feed:
	for _, item := range items {
		select {
		case jobs <- item:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	return out
}
