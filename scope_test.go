package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// ── checkRel (pure) ─────────────────────────────────────────

func TestCheckRel(t *testing.T) {
	cases := []struct {
		name       string
		rel        string
		only       string
		restricted bool
		want       string
		ok         bool
	}{
		{"root", ".", "", false, ".", true},
		{"empty is root", "", "", false, ".", true},
		{"plain file", "a/b.md", "", false, "a/b.md", true},
		{"leading slash stripped", "/a/b.md", "", false, "a/b.md", true},
		{"cleans dots", "a/./c/../b.md", "", false, "a/b.md", true},
		{"traversal", "../x", "", false, "", false},
		{"deep traversal", "a/../../x", "", false, "", false},
		{"backslash traversal", `..\x`, "", true, "", false},
		{"dotfile allowed for owner", ".env", "", false, ".env", true},
		{"dotfile hidden in share", ".env", "", true, "", false},
		{"dot dir hidden in share", "sub/.git/config", "", true, "", false},
		{"root ok in share", ".", "", true, ".", true},
		{"only: the file", "doc.md", "doc.md", true, "doc.md", true},
		{"only: root listing", ".", "doc.md", true, ".", true},
		{"only: other file", "other.md", "doc.md", true, "", false},
		{"only: traversal to file", "x/../doc.md", "doc.md", true, "doc.md", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := checkRel(c.rel, c.only, c.restricted)
			if ok != c.ok || got != c.want {
				t.Fatalf("checkRel(%q, %q, %v) = %q, %v; want %q, %v", c.rel, c.only, c.restricted, got, ok, c.want, c.ok)
			}
		})
	}
}

// ── scope.resolve (filesystem) ──────────────────────────────

// shareFixture builds:
//
//	tmp/root/shared/a.md
//	tmp/root/shared/.env
//	tmp/root/shared/in-link -> a.md         (symlink inside the share)
//	tmp/root/shared/out-link -> ../secret.md (symlink escaping the share)
//	tmp/root/secret.md
func shareFixture(t *testing.T) (root string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "root")
	shared := filepath.Join(root, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	for p, s := range map[string]string{
		filepath.Join(shared, "a.md"):    "# A",
		filepath.Join(shared, ".env"):    "SECRET=1",
		filepath.Join(root, "secret.md"): "top secret",
	} {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("a.md", filepath.Join(shared, "in-link")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	if err := os.Symlink(filepath.Join("..", "secret.md"), filepath.Join(shared, "out-link")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestScopeResolve_Restricted(t *testing.T) {
	root := shareFixture(t)
	sc := scope{root: filepath.Join(root, "shared"), restricted: true}

	if p, err := sc.resolve("a.md"); err != nil || p != filepath.Join(root, "shared", "a.md") {
		t.Fatalf("resolve(a.md) = %q, %v", p, err)
	}
	if _, err := sc.resolve("in-link"); err != nil {
		t.Fatalf("symlink inside share should resolve: %v", err)
	}
	for _, bad := range []string{"out-link", ".env", "../secret.md"} {
		if _, err := sc.resolve(bad); err == nil {
			t.Errorf("resolve(%q) should be rejected", bad)
		}
	}
}

func TestScopeResolve_OwnerFollowsSymlinks(t *testing.T) {
	root := shareFixture(t)
	sc := scope{root: root}
	// The owner sees the whole root, symlinks included (unchanged behaviour).
	if _, err := sc.resolve("shared/out-link"); err != nil {
		t.Fatalf("owner should follow symlinks: %v", err)
	}
	if _, err := sc.resolve("shared/.env"); err != nil {
		t.Fatalf("owner should see dotfiles: %v", err)
	}
}

func TestScopeResolve_Only(t *testing.T) {
	root := shareFixture(t)
	sc := scope{root: filepath.Join(root, "shared"), only: "a.md", restricted: true}
	if _, err := sc.resolve("a.md"); err != nil {
		t.Fatalf("the shared file must resolve: %v", err)
	}
	if _, err := sc.resolve("in-link"); err == nil {
		t.Fatal("other files in a single-file share must be rejected")
	}
}

// ── scope.files ─────────────────────────────────────────────

func TestScopeFiles_RestrictedHidesDotfilesAndEscapes(t *testing.T) {
	root := shareFixture(t)
	got := scope{root: filepath.Join(root, "shared"), restricted: true}.files()
	sort.Strings(got)
	want := []string{"a.md", "in-link"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("files() = %v, want %v", got, want)
	}
}

func TestScopeFiles_Only(t *testing.T) {
	root := shareFixture(t)
	got := scope{root: filepath.Join(root, "shared"), only: "a.md", restricted: true}.files()
	if !reflect.DeepEqual(got, []string{"a.md"}) {
		t.Fatalf("files() = %v, want [a.md]", got)
	}
}

// ── request context ─────────────────────────────────────────

func TestScopeOf_DefaultsToRoot(t *testing.T) {
	setRoot(t, "testdata")
	r := httptest.NewRequest("GET", "/api/tree", nil)
	if sc := scopeOf(r); sc.root != rootDir || sc.restricted || sc.only != "" {
		t.Fatalf("default scope = %+v", sc)
	}
	want := scope{root: "/x", only: "y", restricted: true}
	r = r.WithContext(withScope(context.Background(), want))
	if sc := scopeOf(r); sc != want {
		t.Fatalf("scopeOf = %+v, want %+v", sc, want)
	}
}

// ── handlers honour the scope ───────────────────────────────

func TestHandlers_RespectRestrictedScope(t *testing.T) {
	root := shareFixture(t)
	sc := scope{root: filepath.Join(root, "shared"), restricted: true}
	for _, tc := range []struct {
		url  string
		want int
	}{
		{"/api/file?path=a.md", 200},
		{"/api/raw?path=a.md", 200},
		{"/api/file?path=.env", 400},
		{"/api/raw?path=out-link", 400},
		{"/api/raw?path=../secret.md", 400},
		{"/api/jsonl?path=../secret.md", 400},
		{"/api/backlinks?path=../secret.md", 400},
		{"/api/tree?path=..", 400},
		{"/api/editors", 404},
		{"/api/open?path=a.md&editor=vscode", 404},
	} {
		r := httptest.NewRequest("GET", tc.url, nil).WithContext(withScope(context.Background(), sc))
		w := httptest.NewRecorder()
		apiHandler().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s: got %d, want %d (%s)", tc.url, w.Code, tc.want, w.Body.String())
		}
	}
}

func TestHandleTree_RestrictedScopeHidesDotfiles(t *testing.T) {
	root := shareFixture(t)
	sc := scope{root: filepath.Join(root, "shared"), restricted: true}
	r := httptest.NewRequest("GET", "/api/tree?path=.", nil).WithContext(withScope(context.Background(), sc))
	w := httptest.NewRecorder()
	handleTree(w, r)
	names := treeNames(t, w)
	if names[".env"] || names["out-link"] {
		t.Fatalf("restricted tree leaked hidden entries: %v", names)
	}
	if !names["a.md"] {
		t.Fatalf("restricted tree missing a.md: %v", names)
	}
}

func TestHandleTree_OnlyScopeShowsSingleFile(t *testing.T) {
	root := shareFixture(t)
	sc := scope{root: filepath.Join(root, "shared"), only: "a.md", restricted: true}
	r := httptest.NewRequest("GET", "/api/tree?path=.", nil).WithContext(withScope(context.Background(), sc))
	w := httptest.NewRecorder()
	handleTree(w, r)
	names := treeNames(t, w)
	if len(names) != 1 || !names["a.md"] {
		t.Fatalf("single-file tree = %v, want only a.md", names)
	}
}

func TestSearch_RestrictedScopeStaysInside(t *testing.T) {
	root := shareFixture(t)
	sc := scope{root: filepath.Join(root, "shared"), restricted: true}
	r := httptest.NewRequest("GET", "/api/search?q=secret", nil).WithContext(withScope(context.Background(), sc))
	w := httptest.NewRecorder()
	handleSearch(w, r)
	if body := w.Body.String(); contains(body, "top secret") || contains(body, ".env") {
		t.Fatalf("search leaked content outside the share: %s", body)
	}
}

// ── websocket notifications honour the client's scope ──────

func TestHub_NotifyAbsRespectsClientScope(t *testing.T) {
	root := shareFixture(t)
	h := newHub()
	shared := &wsClient{
		hub: h, send: make(chan []byte, 8),
		watched: map[string]bool{".": true},
		scope:   scope{root: filepath.Join(root, "shared"), restricted: true},
	}
	h.register(shared)

	h.notifyAbs(filepath.Join(root, "secret.md"))      // outside the share
	h.notifyAbs(filepath.Join(root, "shared", ".env")) // hidden in shares
	h.notifyAbs(filepath.Join(root, "shared", "a.md")) // visible
	select {
	case msg := <-shared.send:
		if string(msg) != `{"path":"a.md","type":"change"}` {
			t.Fatalf("got %s", msg)
		}
	default:
		t.Fatal("expected a notification for a.md")
	}
	select {
	case msg := <-shared.send:
		t.Fatalf("unexpected extra notification %s", msg)
	default:
	}
}

func treeNames(t *testing.T, w *httptest.ResponseRecorder) map[string]bool {
	t.Helper()
	var entries []TreeEntry
	if err := json.NewDecoder(w.Body).Decode(&entries); err != nil {
		t.Fatalf("invalid JSON (%d): %v", w.Code, err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	return names
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
