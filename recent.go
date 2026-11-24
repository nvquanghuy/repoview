package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	recentDefaultLimit = 20
	recentMaxLimit     = 100
)

// RecentFile is one entry of GET /api/recent.
type RecentFile struct {
	Path    string `json:"path"`
	ModTime int64  `json:"modTime"` // Unix milliseconds
	Size    int64  `json:"size"`
}

// handleRecent lists the most recently modified files in the request's
// scope, newest first. It lets people jump straight to files an agent (or
// anything else) just wrote, wherever they are in the tree.
func handleRecent(w http.ResponseWriter, r *http.Request) {
	limit := recentDefaultLimit
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		limit = min(l, recentMaxLimit)
	}

	if !acquireHeavy(w, r) {
		return
	}
	defer releaseHeavy()

	sc := scopeOf(r)
	files := parallelMap(r.Context(), sc.files(), func(rel string) (RecentFile, bool) {
		if inDotPath(rel) {
			return RecentFile{}, false
		}
		info, err := os.Stat(filepath.Join(sc.root, filepath.FromSlash(rel)))
		if err != nil || info.IsDir() {
			return RecentFile{}, false // deleted since listing, or a submodule
		}
		return RecentFile{Path: rel, ModTime: info.ModTime().UnixMilli(), Size: info.Size()}, true
	})
	files = newestFirst(files, limit)
	if files == nil {
		files = []RecentFile{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

// inDotPath reports whether any segment of a slash path starts with a dot.
// Dot-folders hold app state (.obsidian/workspace.json is rewritten on every
// click), which would crowd the files people actually write out of the list.
func inDotPath(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// newestFirst sorts by modification time (newest first, ties by path) and
// keeps at most limit entries.
func newestFirst(files []RecentFile, limit int) []RecentFile {
	sort.Slice(files, func(i, j int) bool {
		if files[i].ModTime != files[j].ModTime {
			return files[i].ModTime > files[j].ModTime
		}
		return files[i].Path < files[j].Path
	})
	if len(files) > limit {
		files = files[:limit]
	}
	return files
}
