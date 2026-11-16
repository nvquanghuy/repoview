package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ── F1: a share never reaches outside the served root ──────

func TestShare_RootTurnedIntoOutsideSymlinkIsRefused(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)

	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "id_rsa"), []byte("PRIVATE"), 0o600)

	pub := filepath.Join(e.root, "pub")
	os.MkdirAll(pub, 0o755)
	os.WriteFile(filepath.Join(pub, "a.md"), []byte("# a"), 0o644)
	sh := e.createShare(owner, "pub", "", 0)

	// Later (a pull, a sync) the shared folder becomes a symlink elsewhere.
	os.RemoveAll(pub)
	if err := os.Symlink(outside, pub); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	anon := e.client()
	for _, p := range []string{"/_raw/id_rsa", "/api/file?path=id_rsa", "/api/tree", "/"} {
		if r := e.get(anon, sh.URL+p); r.code == http.StatusOK || strings.Contains(r.body, "PRIVATE") {
			t.Errorf("%s: %d %q", p, r.code, r.body)
		}
	}
}

func TestShare_CannotCreateShareOfOutsideSymlink(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.md"), []byte("x"), 0o644)
	if err := os.Symlink(outside, filepath.Join(e.root, "out-dir")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(e.root, "out-file.md"))
	for _, p := range []string{"out-dir", "out-file.md"} {
		body := fmt.Sprintf(`{"path":%q}`, p)
		r := e.do(owner, "POST", "/api/shares", strings.NewReader(body), map[string]string{"Origin": e.srv.URL, "Content-Type": "application/json"})
		if r.code != http.StatusBadRequest {
			t.Errorf("share of %s: %d %s", p, r.code, r.body)
		}
	}
}

// ── F2: listing files never runs repository-configured commands ──

func TestListFiles_DoesNotRunGitFsmonitor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "PWNED")
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("a"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "b.md"), []byte("b"), 0o644)
	run("add", ".")
	run("config", "core.fsmonitor", "touch "+marker+"; false")

	listFilesIn(dir)
	scope{root: filepath.Join(dir, "sub"), restricted: true}.files()
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("git ran the repository's core.fsmonitor command")
	}
}

// Share visitors never cause git to run at all.
func TestShareScopeFiles_DoesNotUseGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	exec.Command("git", "-C", dir, "init", "-q").Run()
	os.WriteFile(filepath.Join(dir, "tracked.md"), []byte("a"), 0o644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	os.WriteFile(filepath.Join(dir, "untracked.md"), []byte("b"), 0o644)
	got := scope{root: dir, restricted: true}.files()
	have := map[string]bool{}
	for _, f := range got {
		have[f] = true
	}
	// A filesystem walk lists untracked files too; git ls-files would not.
	if !have["untracked.md"] || !have["tracked.md"] {
		t.Fatalf("restricted scope should walk the filesystem, got %v", got)
	}
}

// ── F3: big files are not rendered ──────────────────────────

func TestHandleFile_LargeFileReturnsMetadataOnly(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("package main\nfunc f() {}\n", (maxRenderSize/24)+10)
	os.WriteFile(filepath.Join(dir, "big.go"), []byte(big), 0o644)
	r := httptest.NewRequest("GET", "/api/file?path=big.go", nil).WithContext(withScope(context.Background(), scope{root: dir}))
	w := httptest.NewRecorder()
	handleFile(w, r)
	var resp FileResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.IsTooLarge || resp.Content != "" || resp.Size != int64(len(big)) {
		t.Fatalf("large file: tooLarge=%v contentLen=%d size=%d", resp.IsTooLarge, len(resp.Content), resp.Size)
	}
}

func TestHandleFile_LargeImagePreviewed(t *testing.T) {
	dir := t.TempDir()
	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F', 0}, make([]byte, maxRenderSize+1)...)
	os.WriteFile(filepath.Join(dir, "photo.jpeg"), jpeg, 0o644)
	r := httptest.NewRequest("GET", "/api/file?path=photo.jpeg", nil).WithContext(withScope(context.Background(), scope{root: dir}))
	w := httptest.NewRecorder()
	handleFile(w, r)
	var resp FileResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.IsTooLarge || !resp.IsBinary || resp.MimeType != "image/jpeg" || resp.Size != int64(len(jpeg)) {
		t.Fatalf("large image: tooLarge=%v binary=%v mime=%q size=%d", resp.IsTooLarge, resp.IsBinary, resp.MimeType, resp.Size)
	}
}

// ── F4: websocket input is bounded ──────────────────────────

func TestWebsocket_InputBounded(t *testing.T) {
	setRoot(t, "testdata")
	app := newServer(serverConfig{root: rootDir})
	srv := httptest.NewServer(app.handler())
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	// Oversized messages close the connection.
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.WriteMessage(websocket.TextMessage, []byte(`{"action":"watch","path":"`+strings.Repeat("a", 1<<20)+`"}`))
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("oversized message must close the connection")
	}
	conn.Close()

	// Watches of nonexistent paths aren't stored; the watch list is capped.
	conn, _, err = websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for i := 0; i < maxWatchesPerClient+50; i++ {
		conn.WriteJSON(map[string]string{"action": "watch", "path": fmt.Sprintf("nope-%d.md", i)})
	}
	conn.WriteJSON(map[string]string{"action": "watch", "path": "hello.md"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n := app.hub.maxWatched(); n == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := app.hub.maxWatched(); n != 1 {
		t.Fatalf("watched entries = %d, want only the existing path", n)
	}
}

func TestWebsocket_ConnectionCap(t *testing.T) {
	setRoot(t, "testdata")
	app := newServer(serverConfig{root: rootDir})
	srv := httptest.NewServer(app.handler())
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	var conns []*websocket.Conn
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	for i := 0; i < maxWSClients; i++ {
		c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("conn %d: %v", i, err)
		}
		conns = append(conns, c)
	}
	if _, resp, err := websocket.DefaultDialer.Dial(wsURL, nil); err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("connection over the cap: err=%v resp=%v", err, resp)
	}
}
