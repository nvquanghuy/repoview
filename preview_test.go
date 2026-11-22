package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// previewURLOf asks /api/file (as client c, under base) for an HTML file's
// preview URL.
func (e *authEnv) previewURLOf(c *http.Client, base, path string) string {
	e.t.Helper()
	r := e.get(c, base+"/api/file?path="+path)
	var resp FileResponse
	if err := json.Unmarshal([]byte(r.body), &resp); err != nil || r.code != http.StatusOK {
		e.t.Fatalf("api/file %s: %d %s", path, r.code, r.body)
	}
	if resp.PreviewURL == "" {
		e.t.Fatalf("no previewURL for %s: %s", path, r.body)
	}
	return resp.PreviewURL
}

// cookieless fetches like an opaque-origin (sandboxed) document: no cookies.
func (e *authEnv) cookieless(path string) resp {
	return e.get(&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, path)
}

func TestPreview_OwnerRelativeAssetsLoadWithoutCookies(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)

	u := e.previewURLOf(owner, "", "site/index.html")
	if !strings.HasPrefix(u, "/_preview/") || !strings.HasSuffix(u, "/site/index.html") {
		t.Fatalf("preview URL %q", u)
	}
	page := e.cookieless(u)
	if page.code != http.StatusOK || !strings.Contains(page.body, "Site with relative assets") {
		t.Fatalf("preview page: %d", page.code)
	}
	csp := page.header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox allow-scripts") || !strings.Contains(csp, "connect-src 'none'") {
		t.Fatalf("preview CSP %q", csp)
	}
	dir := strings.TrimSuffix(u, "index.html")
	for _, asset := range []string{"img/dot.png", "css/style.css"} {
		if r := e.cookieless(dir + asset); r.code != http.StatusOK {
			t.Errorf("relative asset %s: %d", asset, r.code)
		}
	}
}

func TestPreview_ShareRelativeAssetsLoadWithoutCookies(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	sh := e.createShare(owner, "site", "pw", 0)
	bob := e.client()
	e.unlockShare(bob, sh, "pw")

	u := e.previewURLOf(bob, sh.URL, "index.html")
	if !strings.HasPrefix(u, sh.URL+"/_preview/") {
		t.Fatalf("share preview URL %q", u)
	}
	if r := e.cookieless(u); r.code != http.StatusOK {
		t.Fatalf("share preview page: %d", r.code)
	}
	if r := e.cookieless(strings.TrimSuffix(u, "index.html") + "img/dot.png"); r.code != http.StatusOK {
		t.Fatalf("share relative image: %d", r.code)
	}
}

func TestPreview_TokensAreScopedAndRevocable(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	os.WriteFile(filepath.Join(e.root, "secret.md"), []byte("TOP SECRET"), 0o644)
	a := e.createShare(owner, "site", "pw", 0)
	b := e.createShare(owner, "subdir", "pw", 0)
	bob := e.client()
	e.unlockShare(bob, a, "pw")
	ua := e.previewURLOf(bob, a.URL, "index.html")
	tok := strings.SplitN(strings.TrimPrefix(ua, a.URL+"/_preview/"), "/", 2)[0]

	for name, p := range map[string]string{
		"escape the share":         a.URL + "/_preview/" + tok + "/../secret.md",
		"escape encoded":           a.URL + "/_preview/" + tok + "/..%2Fsecret.md",
		"other share":              b.URL + "/_preview/" + tok + "/nested.md",
		"owner area":               "/_preview/" + tok + "/secret.md",
		"tampered":                 a.URL + "/_preview/" + tok + "x/index.html",
		"session token as preview": a.URL + "/_preview/" + "garbage.sig/index.html",
	} {
		if r := e.cookieless(p); r.code == http.StatusOK || strings.Contains(r.body, "TOP SECRET") {
			t.Errorf("%s: %d", name, r.code)
		}
	}
	// A preview token is not a session.
	if r := e.cookieless(a.URL + "/api/tree?token=" + tok); r.code != http.StatusUnauthorized {
		t.Errorf("token used as session: %d", r.code)
	}

	// Owner tokens die with a password change: other credential, same secret.
	uo := e.previewURLOf(owner, "", "site/index.html")
	changed := mustAuthServer(t, e.root, e.cfgDir, "new password", e.now)
	if code := serveOnce(t, changed, uo); code == http.StatusOK {
		t.Errorf("owner preview token survived a password change")
	}

	// Revoking the share kills its tokens; so does expiry.
	e.do(owner, "DELETE", "/api/shares?id="+a.ID, nil, map[string]string{"Origin": e.srv.URL})
	if r := e.cookieless(ua); r.code == http.StatusOK {
		t.Errorf("revoked share preview: %d", r.code)
	}
	e.advance(previewMaxAge + time.Minute)
	if r := e.cookieless(uo); r.code == http.StatusOK {
		t.Errorf("expired owner preview: %d", r.code)
	}
}

func serveOnce(t *testing.T, app *server, path string) int {
	t.Helper()
	w := httptest.NewRecorder()
	r, _ := http.NewRequest("GET", path, nil)
	r.RemoteAddr = "127.0.0.1:1"
	app.handler().ServeHTTP(w, r)
	return w.Code
}

// Without a password there is nothing to authenticate: previews use /_raw/.
func TestPreview_LocalModeUsesRaw(t *testing.T) {
	setRoot(t, "testdata")
	srv := newTestServer(t)
	defer srv.Close()
	r, err := http.Get(srv.URL + "/api/file?path=site/index.html")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var resp FileResponse
	json.NewDecoder(r.Body).Decode(&resp)
	if resp.PreviewURL != "/_raw/site/index.html" {
		t.Fatalf("local preview URL %q", resp.PreviewURL)
	}
}

// With a password, a raw file opened top-level with cookies gets no scripts
// (anyone could link a planted page that draws a fake login form), but a
// preview capability URL, which only a signed-in viewer can obtain, may run
// full-tab ("Open original").
func TestPreview_TopLevelScriptsOnlyThroughPreviewURL(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	doc := map[string]string{"Sec-Fetch-Dest": "document"}

	raw := e.do(owner, "GET", "/_raw/site/index.html", nil, doc)
	if raw.code != http.StatusOK || raw.header.Get("Content-Security-Policy") != "sandbox" {
		t.Fatalf("raw top-level: %d CSP %q, want plain sandbox", raw.code, raw.header.Get("Content-Security-Policy"))
	}

	u := e.previewURLOf(owner, "", "site/index.html")
	cookieless := &http.Client{}
	page := e.do(cookieless, "GET", u, nil, doc)
	if page.code != http.StatusOK || page.header.Get("Content-Security-Policy") != "sandbox allow-scripts; connect-src 'none'" {
		t.Fatalf("preview top-level: %d CSP %q", page.code, page.header.Get("Content-Security-Policy"))
	}

	// A forged or expired token still gets nothing.
	tok := strings.SplitN(strings.TrimPrefix(u, "/_preview/"), "/", 2)[0]
	if r := e.do(cookieless, "GET", "/_preview/"+tok+"x/site/index.html", nil, doc); r.code != http.StatusUnauthorized {
		t.Fatalf("tampered token: %d", r.code)
	}
	e.advance(previewMaxAge + time.Minute)
	if r := e.do(cookieless, "GET", u, nil, doc); r.code != http.StatusUnauthorized {
		t.Fatalf("expired token: %d", r.code)
	}
}
