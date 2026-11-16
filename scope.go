package main

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// scope is the slice of the filesystem a request may see. The owner gets
// the whole root; a share gets a sub-folder (or a single file) with stricter
// rules. Every API handler resolves paths through the request's scope, so
// the boundary is enforced in one place.
type scope struct {
	root       string // absolute directory request paths are relative to
	only       string // when set, the single file (slash path) visible in this scope
	restricted bool   // shares: hide dot-paths and refuse symlinks that escape root
}

var errOutsideScope = errors.New("path outside scope")

// checkRel normalises a slash-separated request path and decides whether it
// is visible in a scope. It touches no filesystem. "." means the scope root.
func checkRel(rel, only string, restricted bool) (string, bool) {
	if restricted || os.PathSeparator == '\\' {
		rel = strings.ReplaceAll(rel, `\`, "/")
	}
	// A request that climbs above its root is refused outright rather than
	// clamped, so "../x" can't quietly turn into "x".
	if escapes(rel) {
		return "", false
	}
	cleaned := strings.TrimPrefix(path.Clean("/"+rel), "/")
	if cleaned == "" {
		cleaned = "."
	}
	if restricted && cleaned != "." {
		for _, seg := range strings.Split(cleaned, "/") {
			if strings.HasPrefix(seg, ".") {
				return "", false
			}
		}
	}
	if only != "" && cleaned != "." && cleaned != only {
		return "", false
	}
	return cleaned, true
}

// escapes reports whether a relative path climbs above its starting point at
// any step (e.g. "../x" or "a/../../x").
func escapes(rel string) bool {
	depth := 0
	for _, seg := range strings.Split(rel, "/") {
		switch seg {
		case "", ".":
		case "..":
			depth--
			if depth < 0 {
				return true
			}
		default:
			depth++
		}
	}
	return false
}

// resolve maps a request path to an absolute filesystem path inside the scope.
func (s scope) resolve(rel string) (string, error) {
	cleaned, ok := checkRel(rel, s.only, s.restricted)
	if !ok {
		return "", errOutsideScope
	}
	abs := filepath.Join(s.root, filepath.FromSlash(cleaned))
	if s.restricted && !s.realPathInside(abs) {
		return "", errOutsideScope
	}
	return abs, nil
}

// realPathInside reports whether abs, after following symlinks, still lies
// within the scope root. Paths that don't exist yet are judged by their
// lexical location (they can't leak anything).
func (s scope) realPathInside(abs string) bool {
	realRoot, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return false
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return true
		}
		return false
	}
	return real == realRoot || strings.HasPrefix(real, realRoot+string(os.PathSeparator))
}

// visible reports whether a slash path relative to the scope root may be
// shown. Cheap check for listings; resolve() is the authoritative one.
func (s scope) visible(rel string) bool {
	_, err := s.resolve(rel)
	return err == nil
}

// files returns every visible file path under the scope root (slash-separated,
// relative). It uses `git ls-files` when the root is inside a git repo,
// otherwise walks the tree skipping dotfiles and dot-directories.
func (s scope) files() []string {
	if s.only != "" {
		if s.visible(s.only) {
			return []string{s.only}
		}
		return nil
	}
	all := listFilesIn(s.root)
	if !s.restricted {
		return all
	}
	out := all[:0]
	for _, f := range all {
		if s.visible(f) {
			out = append(out, f)
		}
	}
	return out
}

func listFilesIn(root string) []string {
	var files []string

	// Try git ls-files first. -z gives raw NUL-separated paths; without it git
	// C-quotes non-ASCII names (e.g. "T\341\273\255.md") and they can't be opened.
	// In a sub-directory of a repo it lists only that directory, relative to it.
	cmd := exec.Command("git", "-C", root, "ls-files", "-z")
	out, err := cmd.Output()
	if err == nil && len(out) > 0 {
		for _, name := range strings.Split(string(out), "\x00") {
			if name != "" {
				files = append(files, name)
			}
		}
		return files
	}

	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") && p != root {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	return files
}

type scopeKey struct{}

func withScope(ctx context.Context, s scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, s)
}

// scopeOf returns the request's scope, defaulting to the whole root.
func scopeOf(r *http.Request) scope {
	if s, ok := r.Context().Value(scopeKey{}).(scope); ok {
		return s
	}
	return scope{root: rootDir}
}

// apiHandler serves the read-only API that both the owner and share
// visitors use. Each handler reads the scope from the request context.
func apiHandler() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tree", handleTree)
	mux.HandleFunc("/api/file", handleFile)
	mux.HandleFunc("/api/files", handleFiles)
	mux.HandleFunc("/api/search", handleSearch)
	mux.HandleFunc("/api/backlinks", handleBacklinks)
	mux.HandleFunc("/api/raw", handleRaw)
	mux.HandleFunc("/api/jsonl", handleJSONL)
	mux.HandleFunc(rawPrefix, handleRawPath)
	return mux
}
