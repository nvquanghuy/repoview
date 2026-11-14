package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func withObsidian(t *testing.T, on bool) {
	t.Helper()
	old := isObsidianVault
	isObsidianVault = on
	t.Cleanup(func() { isObsidianVault = old })
}

func getBacklinks(t *testing.T, srv, p string) BacklinksResponse {
	t.Helper()
	resp, err := http.Get(srv + "/api/backlinks?path=" + url.QueryEscape(p))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out BacklinksResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func backlinkPaths(r BacklinksResponse) []string {
	var p []string
	for _, b := range r.Backlinks {
		p = append(p, b.Path)
	}
	return p
}

// touch sets distinct modification times so ordering is deterministic.
func touch(t *testing.T, rel string, age time.Duration) {
	t.Helper()
	ts := time.Now().Add(-age)
	if err := os.Chtimes(filepath.Join(rootDir, filepath.FromSlash(rel)), ts, ts); err != nil {
		t.Fatal(err)
	}
}

func TestBacklinks_WikiAndMarkdownLinks(t *testing.T) {
	withObsidian(t, true)
	writeVault(t, map[string]string{
		"projects/alpha.md": "# Alpha\n",
		"daily/2026-11-01.md": "Worked on [[alpha]] today.\n" +
			"Also [[alpha|the alpha project]] and [[projects/alpha#Goals]].\n",
		"daily/2026-11-02.md": "See [notes](../projects/alpha.md) and [root link](/projects/alpha.md#x).\n",
		"index.md":            "- [Alpha](projects/alpha.md)\n- [Encoded](projects%2Falpha.md)\n- ![[alpha]]\n",
		"unrelated.md":        "Mentions alpha but has no link. [[beta]] [x](https://example.com/alpha.md)\n",
		"projects/alpha2.md":  "[[alpha2]] is not alpha\n",
	})
	touch(t, "daily/2026-11-01.md", 3*time.Hour)
	touch(t, "daily/2026-11-02.md", 1*time.Hour)
	touch(t, "index.md", 2*time.Hour)

	srv := newTestServer(t)
	defer srv.Close()

	got := getBacklinks(t, srv.URL, "projects/alpha.md")
	if got.Path != "projects/alpha.md" {
		t.Errorf("path = %q", got.Path)
	}
	// Most recently modified first.
	want := []string{"daily/2026-11-02.md", "index.md", "daily/2026-11-01.md"}
	if !reflect.DeepEqual(backlinkPaths(got), want) {
		t.Fatalf("paths = %v, want %v", backlinkPaths(got), want)
	}
	counts := map[string]int{}
	for _, b := range got.Backlinks {
		counts[b.Path] = b.Count
	}
	if counts["daily/2026-11-01.md"] != 3 || counts["daily/2026-11-02.md"] != 2 || counts["index.md"] != 3 {
		t.Errorf("counts = %v", counts)
	}
	daily := got.Backlinks[2]
	wantSnips := []BacklinkSnippet{
		{Line: 1, Text: "Worked on [[alpha]] today."},
		{Line: 2, Text: "Also [[alpha|the alpha project]] and [[projects/alpha#Goals]]."},
	}
	if !reflect.DeepEqual(daily.Snippets, wantSnips) {
		t.Errorf("snippets = %#v", daily.Snippets)
	}
}

func TestBacklinks_IgnoresCodeAndSelfLinks(t *testing.T) {
	withObsidian(t, true)
	writeVault(t, map[string]string{
		"target.md": "Self link [[target]] should not count.\n",
		"code.md": "```\n[[target]]\n```\n" +
			"~~~md\n[x](target.md)\n```\nstill fenced [[target]]\n~~~\n" +
			"Inline `[[target]]` code.\n" +
			"Real [[target]] link.\n",
	})
	srv := newTestServer(t)
	defer srv.Close()

	got := getBacklinks(t, srv.URL, "target.md")
	if !reflect.DeepEqual(backlinkPaths(got), []string{"code.md"}) {
		t.Fatalf("paths = %v", backlinkPaths(got))
	}
	b := got.Backlinks[0]
	if b.Count != 1 || len(b.Snippets) != 1 || b.Snippets[0].Line != 10 {
		t.Errorf("got %+v", b)
	}
}

func TestBacklinks_WikiLinksOnlyInObsidianVault(t *testing.T) {
	withObsidian(t, false)
	writeVault(t, map[string]string{
		"a.md": "# A\n",
		"b.md": "[[a]]\n",
		"c.md": "[a](a.md)\n",
	})
	srv := newTestServer(t)
	defer srv.Close()

	if got := backlinkPaths(getBacklinks(t, srv.URL, "a.md")); !reflect.DeepEqual(got, []string{"c.md"}) {
		t.Errorf("paths = %v (wiki links must not count outside a vault)", got)
	}
}

func TestBacklinks_WikiResolutionPrefersSameDirThenShortest(t *testing.T) {
	withObsidian(t, true)
	writeVault(t, map[string]string{
		"notes/todo.md":       "x\n",
		"archive/old/todo.md": "x\n",
		"notes/today.md":      "[[todo]]\n", // same dir -> notes/todo.md
		"elsewhere.md":        "[[todo]]\n", // shortest -> notes/todo.md
		"archive/old/log.md":  "[[todo]]\n", // same dir -> archive/old/todo.md
		"explicit.md":         "[[archive/old/todo]]\n",
	})
	srv := newTestServer(t)
	defer srv.Close()

	got := backlinkPaths(getBacklinks(t, srv.URL, "notes/todo.md"))
	if !sameSet(got, []string{"notes/today.md", "elsewhere.md"}) {
		t.Errorf("notes/todo.md backlinks = %v", got)
	}
	got = backlinkPaths(getBacklinks(t, srv.URL, "archive/old/todo.md"))
	if !sameSet(got, []string{"archive/old/log.md", "explicit.md"}) {
		t.Errorf("archive/old/todo.md backlinks = %v", got)
	}
}

func TestBacklinks_NonMarkdownTarget(t *testing.T) {
	withObsidian(t, true)
	writeVault(t, map[string]string{
		"img/diagram.png": "\x89PNG",
		"note.md":         "![[diagram.png]]\n![d](img/diagram.png)\n",
	})
	srv := newTestServer(t)
	defer srv.Close()

	got := getBacklinks(t, srv.URL, "img/diagram.png")
	if len(got.Backlinks) != 1 || got.Backlinks[0].Count != 2 {
		t.Errorf("got %+v", got)
	}
}

func TestBacklinks_NoneReturnsEmptyArray(t *testing.T) {
	writeVault(t, map[string]string{"lonely.md": "nobody links here\n"})
	srv := newTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/backlinks?path=lonely.md")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw map[string]json.RawMessage
	json.NewDecoder(resp.Body).Decode(&raw)
	if string(raw["backlinks"]) != "[]" {
		t.Errorf("backlinks = %s, want []", raw["backlinks"])
	}
}

func TestBacklinks_BadRequests(t *testing.T) {
	writeVault(t, map[string]string{"a.md": ""})
	srv := newTestServer(t)
	defer srv.Close()

	for _, p := range []string{"", "/", "."} {
		resp, err := http.Get(srv.URL + "/api/backlinks?path=" + url.QueryEscape(p))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("path=%q: status %d, want 400", p, resp.StatusCode)
		}
	}
	// Traversal is normalised into the root rather than escaping it.
	if got := getBacklinks(t, srv.URL, "../../etc/passwd"); got.Path != "etc/passwd" {
		t.Errorf("traversal path = %q", got.Path)
	}
}

func TestResolveMarkdownLink(t *testing.T) {
	cases := []struct {
		dest, dir, want string
		ok              bool
	}{
		{"b.md", "notes", "notes/b.md", true},
		{"../b.md", "notes/sub", "notes/b.md", true},
		{"./b.md#heading", "", "b.md", true},
		{"/x/y.md?raw=1", "notes", "x/y.md", true},
		{"<my note.md>", "", "my note.md", true},
		{"my%20note.md", "", "my note.md", true},
		{"../../outside.md", "notes", "", false},
		{"https://example.com/a.md", "", "", false},
		{"mailto:me@example.com", "", "", false},
		{"//cdn.example.com/a.md", "", "", false},
		{"#local", "", "", false},
	}
	for _, c := range cases {
		got, ok := resolveMarkdownLink(c.dest, c.dir)
		if got != c.want || ok != c.ok {
			t.Errorf("resolveMarkdownLink(%q, %q) = %q, %v; want %q, %v", c.dest, c.dir, got, ok, c.want, c.ok)
		}
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		if m[x] == 0 {
			return false
		}
		m[x]--
	}
	return true
}
