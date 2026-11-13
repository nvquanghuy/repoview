package main

import (
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPWAManifest(t *testing.T) {
	setRoot(t, "testdata")
	srv := newTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + pwaManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/manifest+json" {
		t.Errorf("unexpected Content-Type %q", ct)
	}

	var m webManifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if m.ShortName != "testdata" {
		t.Errorf("short_name = %q, want root folder name", m.ShortName)
	}
	if m.Name != "testdata - RepoView" {
		t.Errorf("name = %q", m.Name)
	}
	if m.Display != "standalone" || m.StartURL != "/" || m.Scope != "/" {
		t.Errorf("unexpected display/start_url/scope: %+v", m)
	}

	sizes := map[string]bool{}
	for _, ic := range m.Icons {
		sizes[ic.Sizes] = true
		// Every icon referenced by the manifest must actually be served.
		r, err := http.Get(srv.URL + ic.Src)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Errorf("icon %s: status %d", ic.Src, r.StatusCode)
		}
	}
	if !sizes["192x192"] || !sizes["512x512"] {
		t.Errorf("manifest must include 192 and 512 icons, got %v", sizes)
	}
}

func TestPWAManifest_EscapesRootName(t *testing.T) {
	var m webManifest
	if err := json.Unmarshal(buildManifest(`my "notes"`), &m); err != nil {
		t.Fatalf("manifest with quotes in name is invalid JSON: %v", err)
	}
	if m.ShortName != `my "notes"` {
		t.Errorf("short_name = %q", m.ShortName)
	}
}

func TestPWAIcons(t *testing.T) {
	setRoot(t, "testdata")
	srv := newTestServer(t)
	defer srv.Close()

	for _, size := range []int{180, 192, 512} {
		name := fmt.Sprintf("icon-%d.png", size)
		resp, err := http.Get(srv.URL + pwaIconPrefix + name)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", name, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
			t.Errorf("%s: Content-Type %q", name, ct)
		}
		cfg, err := png.DecodeConfig(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("%s: not a valid PNG: %v", name, err)
		}
		if cfg.Width != size || cfg.Height != size {
			t.Errorf("%s: got %dx%d", name, cfg.Width, cfg.Height)
		}
	}
}

func TestPWAIcons_RejectsNonPNGAndListing(t *testing.T) {
	setRoot(t, "testdata")
	srv := newTestServer(t)
	defer srv.Close()

	for _, p := range []string{pwaIconPrefix, pwaIconPrefix + "missing.png"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s: expected non-200, got 200 (%d bytes)", p, len(body))
		}
	}
}

func TestIndexHTML_HasPWAMetaTags(t *testing.T) {
	setRoot(t, "testdata")
	srv := newTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	for _, want := range []string{
		`<link rel="manifest" href="` + pwaManifestPath + `"`,
		`<link rel="apple-touch-icon" href="` + pwaIconPrefix + `icon-180.png"`,
		`<meta name="apple-mobile-web-app-capable" content="yes"`,
		`<meta name="apple-mobile-web-app-title" content="testdata"`,
		`<meta name="theme-color"`,
		`viewport-fit=cover`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing %q", want)
		}
	}
}
