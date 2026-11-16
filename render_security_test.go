package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Autolinks (<scheme:...>) bypass goldmark's dangerous-URL filter, which
// only covers [text](url) links and images.
func TestRenderMarkdown_AutolinkScriptURLsDropped(t *testing.T) {
	old := isObsidianVault
	t.Cleanup(func() { isObsidianVault = old })
	for _, vault := range []bool{false, true} {
		isObsidianVault = vault
		for _, src := range []string{
			"<javascript:alert(1)>",
			"<JaVaScRiPt:alert(1)>",
			"<vbscript:msgbox(1)>",
			"<data:text/html,<script>alert(1)</script>>",
		} {
			var buf bytes.Buffer
			renderMarkdown(&buf, []byte(src))
			out := strings.ToLower(buf.String())
			for _, bad := range []string{`href="javascript:`, `href="vbscript:`, `href="data:`} {
				if strings.Contains(out, bad) {
					t.Errorf("vault=%v %q rendered a script URL: %s", vault, src, buf.String())
				}
			}
		}
	}
	// Ordinary autolinks keep working.
	var buf bytes.Buffer
	isObsidianVault = false
	renderMarkdown(&buf, []byte("<https://example.com/a?b=c> and <me@example.com>"))
	out := buf.String()
	if !strings.Contains(out, `href="https://example.com/a?b=c"`) || !strings.Contains(out, `href="mailto:me@example.com"`) {
		t.Fatalf("safe autolinks broken: %s", out)
	}
}

// Opened as a top-level page, a script-bearing file gets no script at all
// (it could otherwise draw a fake login form on our domain); embedded in the
// preview iframe it keeps scripts, still without our origin.
func TestRaw_TopLevelDocumentsGetNoScripts(t *testing.T) {
	setRoot(t, "testdata")
	srv := newTestServer(t)
	defer srv.Close()
	get := func(dest string) string {
		req, _ := http.NewRequest("GET", srv.URL+"/_raw/sample.html", nil)
		if dest != "" {
			req.Header.Set("Sec-Fetch-Dest", dest)
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.Header.Get("Content-Security-Policy")
	}
	if csp := get("document"); csp != "sandbox" {
		t.Errorf("top-level document: CSP %q, want plain sandbox", csp)
	}
	for _, dest := range []string{"iframe", ""} {
		if csp := get(dest); csp != "sandbox allow-scripts; connect-src 'none'" {
			t.Errorf("dest %q: CSP %q", dest, csp)
		}
	}
}

func TestLocalMode_FrameAndSniffProtection(t *testing.T) {
	setRoot(t, "testdata")
	srv := newTestServer(t)
	defer srv.Close()
	r, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.Header.Get("X-Frame-Options") != "SAMEORIGIN" || r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("local mode headers: %v", r.Header)
	}
}

// Placeholder values must never be rescanned: a file named after a later
// placeholder used to break out of the injected JSON config.
func TestRenderIndex_PlaceholdersNotRescanned(t *testing.T) {
	raw := []byte(`<title>__ROOT_DIR_NAME__</title><script>var n = __ROOT_DIR_NAME_JS__; var RV = __RV_CONFIG_JS__;</script>`)
	evil := `,a:console.log('x'),b:__ROOT_DIR_NAME_JS__,c:__ROOT_DIR_NAME__`
	out := string(renderIndex(raw, `name"\`, pageConfig{Base: "/s/x", Mode: "share", File: evil}))
	start := strings.Index(out, "var RV = ")
	end := strings.Index(out[start:], ";</script>")
	var cfg struct{ File string }
	if err := json.Unmarshal([]byte(out[start+len("var RV = "):start+end]), &cfg); err != nil || cfg.File != evil {
		t.Fatalf("config must be intact JSON with the file name verbatim; got %q (%v) in %s", cfg.File, err, out)
	}
	if !strings.Contains(out, `var n = "name\"\\";`) {
		t.Fatalf("root name not encoded correctly: %s", out)
	}
}
