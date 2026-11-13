package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeVault creates a temporary notes folder (not a git repo, so listFiles
// walks it) and points rootDir at it.
func writeVault(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setRoot(t, dir)
}

func doSearch(t *testing.T, srv string, q string, extra string) SearchResponse {
	t.Helper()
	resp, err := http.Get(srv + "/api/search?q=" + url.QueryEscape(q) + extra)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type %q", ct)
	}
	var out SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func resultPaths(r SearchResponse) []string {
	var paths []string
	for _, res := range r.Results {
		paths = append(paths, res.Path)
	}
	return paths
}

func TestParseSearchQuery(t *testing.T) {
	cases := map[string][]string{
		"":                         nil,
		"   ":                      nil,
		"Hello":                    {"hello"},
		"foo  BAR foo":             {"foo", "bar"},
		`"weekly review" tasks`:    {"weekly review", "tasks"},
		`a "b c" d "e f"`:          {"a", "b c", "d", "e f"},
		`unterminated "quote here`: {"unterminated", "quote", "here"},
		`"  spaced  " ""`:          {"spaced"},
	}
	for in, want := range cases {
		if got := parseSearchQuery(in); !reflect.DeepEqual(got, want) {
			t.Errorf("parseSearchQuery(%q) = %#v, want %#v", in, got, want)
		}
	}
}

func TestSearch_AllTermsMustMatch(t *testing.T) {
	writeVault(t, map[string]string{
		"a.md":           "# Groceries\napples and bananas\n",
		"b.md":           "only apples here\n",
		"sub/c.md":       "Bananas.\n\nAPPLES too\n",
		".obsidian/x.md": "apples bananas in a hidden dir\n",
	})
	srv := newTestServer(t)
	defer srv.Close()

	got := doSearch(t, srv.URL, "apples bananas", "")
	paths := resultPaths(got)
	want := []string{"sub/c.md", "a.md"} // c.md has 2 matching lines, a.md has 1
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	if got.Total != 2 || got.Truncated {
		t.Errorf("total=%d truncated=%v", got.Total, got.Truncated)
	}
	if !reflect.DeepEqual(got.Terms, []string{"apples", "bananas"}) {
		t.Errorf("terms = %v", got.Terms)
	}
}

func TestSearch_SnippetsAndLineNumbers(t *testing.T) {
	writeVault(t, map[string]string{
		"note.md": "line one\n    indented Needle here\nnothing\nneedle again\r\nthird needle\nfourth needle\n",
	})
	srv := newTestServer(t)
	defer srv.Close()

	got := doSearch(t, srv.URL, "needle", "")
	if len(got.Results) != 1 {
		t.Fatalf("results = %v", resultPaths(got))
	}
	r := got.Results[0]
	if r.Count != 4 {
		t.Errorf("count = %d, want 4", r.Count)
	}
	want := []SearchSnippet{
		{Line: 2, Text: "indented Needle here"},
		{Line: 4, Text: "needle again"},
		{Line: 5, Text: "third needle"},
	}
	if !reflect.DeepEqual(r.Snippets, want) {
		t.Errorf("snippets = %#v", r.Snippets)
	}
}

func TestSearch_PhraseAndFileNameRanking(t *testing.T) {
	writeVault(t, map[string]string{
		"journal/weekly review.md": "nothing relevant inside\n",
		"notes.md":                 "My weekly review checklist\nweekly review again\n",
		"other.md":                 "weekly stuff and a review\n",
	})
	srv := newTestServer(t)
	defer srv.Close()

	got := doSearch(t, srv.URL, `"weekly review"`, "")
	paths := resultPaths(got)
	want := []string{"journal/weekly review.md", "notes.md"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	if !got.Results[0].NameMatch || got.Results[1].NameMatch {
		t.Errorf("nameMatch flags wrong: %+v", got.Results)
	}

	// Without quotes, other.md matches too (both words, not adjacent).
	if n := len(doSearch(t, srv.URL, "weekly review", "").Results); n != 3 {
		t.Errorf("unquoted: %d results, want 3", n)
	}
}

func TestSearch_MatchesFolderInPath(t *testing.T) {
	writeVault(t, map[string]string{
		"projects/alpha.md": "kickoff notes\n",
		"misc.md":           "kickoff notes\n",
	})
	srv := newTestServer(t)
	defer srv.Close()

	got := doSearch(t, srv.URL, "projects kickoff", "")
	if paths := resultPaths(got); !reflect.DeepEqual(paths, []string{"projects/alpha.md"}) {
		t.Fatalf("paths = %v", paths)
	}
}

func TestSearch_SkipsBinaryAndLargeFiles(t *testing.T) {
	big := strings.Repeat("filler line\n", searchMaxFileSize/12+10) + "zebra\n"
	writeVault(t, map[string]string{
		"bin.dat":  "\x00\x01\x02zebra\x00\x00",
		"big.txt":  big,
		"small.md": "a zebra\n",
	})
	srv := newTestServer(t)
	defer srv.Close()

	if paths := resultPaths(doSearch(t, srv.URL, "zebra", "")); !reflect.DeepEqual(paths, []string{"small.md"}) {
		t.Fatalf("paths = %v", paths)
	}
}

func TestSearch_ShortEmptyOrUnmatchedQueryReturnsEmptyArrays(t *testing.T) {
	writeVault(t, map[string]string{"a.md": "a b c\n"})
	srv := newTestServer(t)
	defer srv.Close()

	for _, q := range []string{"", "  ", "a", "nomatchanywhere"} {
		got := doSearch(t, srv.URL, q, "")
		if len(got.Results) != 0 || got.Results == nil || got.Terms == nil {
			t.Errorf("q=%q: want empty non-null arrays, got %+v", q, got)
		}
	}
}

func TestSearch_Limit(t *testing.T) {
	files := map[string]string{}
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		files[n+".md"] = "common word\n"
	}
	writeVault(t, files)
	srv := newTestServer(t)
	defer srv.Close()

	got := doSearch(t, srv.URL, "common", "&limit=2")
	if !reflect.DeepEqual(resultPaths(got), []string{"a.md", "b.md"}) || got.Total != 5 || !got.Truncated {
		t.Errorf("got %+v", got)
	}
}

func TestSearch_GitRepoUsesTrackedFiles(t *testing.T) {
	setRoot(t, "testdata")
	srv := newTestServer(t)
	defer srv.Close()

	got := doSearch(t, srv.URL, "hello world", "")
	found := false
	for _, r := range got.Results {
		if r.Path == "hello.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("hello.md not found in %v", resultPaths(got))
	}
}

func TestSearch_CancelledContextStopsEarly(t *testing.T) {
	writeVault(t, map[string]string{"a.md": "term\n", "b.md": "term\n"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Must return promptly (not deadlock) and never return more than exist.
	if res := searchFiles(ctx, listFiles(), []string{"term"}); len(res) > 2 {
		t.Errorf("unexpected results %v", res)
	}
}

func TestSnippetAround(t *testing.T) {
	long := strings.Repeat("x", 300) + "TARGET" + strings.Repeat("y", 300)
	got := snippetAround(long, 300)
	if !strings.Contains(got, "TARGET") {
		t.Errorf("snippet lost the match: %q", got)
	}
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipses on both ends: %q", got)
	}
	if n := len([]rune(got)); n > searchSnippetMaxRunes+2 {
		t.Errorf("snippet too long: %d runes", n)
	}

	// Leading indentation is trimmed and the offset adjusted with it.
	indented := "\t\t" + strings.Repeat("é", 300) + "MATCH" + strings.Repeat("é", 50)
	if got := snippetAround(indented, len("\t\t"+strings.Repeat("é", 300))); !strings.Contains(got, "MATCH") {
		t.Errorf("multibyte/indented snippet lost the match: %q", got)
	}

	if got := snippetAround("  short line  ", 2); got != "short line" {
		t.Errorf("short line = %q", got)
	}
}
