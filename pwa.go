package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
)

// PWA assets live under /api/ so they can never shadow a user file served via
// the SPA catch-all (e.g. a root-level "manifest.webmanifest" in the repo).
const (
	pwaManifestPath = "/api/manifest.webmanifest"
	pwaIconPrefix   = "/api/icons/"
)

type webManifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose,omitempty"`
}

type webManifest struct {
	Name            string            `json:"name"`
	ShortName       string            `json:"short_name"`
	StartURL        string            `json:"start_url"`
	Scope           string            `json:"scope"`
	Display         string            `json:"display"`
	BackgroundColor string            `json:"background_color"`
	ThemeColor      string            `json:"theme_color"`
	Icons           []webManifestIcon `json:"icons"`
}

func buildManifest(rootName string) []byte {
	m := webManifest{
		Name:            rootName + " - RepoView",
		ShortName:       rootName,
		StartURL:        "/",
		Scope:           "/",
		Display:         "standalone",
		BackgroundColor: "#ffffff",
		ThemeColor:      "#ffffff",
		Icons: []webManifestIcon{
			{Src: pwaIconPrefix + "icon-192.png", Sizes: "192x192", Type: "image/png", Purpose: "any maskable"},
			{Src: pwaIconPrefix + "icon-512.png", Sizes: "512x512", Type: "image/png", Purpose: "any maskable"},
		},
	}
	out, _ := json.MarshalIndent(m, "", "  ")
	return out
}

// registerPWARoutes serves the web app manifest and home-screen icons so the
// UI can be installed ("Add to Home Screen") and launched standalone.
func registerPWARoutes(mux *http.ServeMux, rootName string) {
	manifest := buildManifest(rootName)
	mux.HandleFunc(pwaManifestPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Write(manifest)
	})

	icons, err := fs.Sub(staticFiles, "static/icons")
	if err != nil {
		panic(err)
	}
	iconServer := http.StripPrefix(pwaIconPrefix, http.FileServer(http.FS(icons)))
	mux.HandleFunc(pwaIconPrefix, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ".png") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=86400")
		iconServer.ServeHTTP(w, r)
	})
}
