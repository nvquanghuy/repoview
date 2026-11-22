package main

import (
	"context"
	"mime"
	"net/http"
	"strings"
)

// scriptableType reports whether a browser could run script from a response
// of this Content-Type when it is opened as a document.
func scriptableType(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return contentType != "" // unparseable: be safe
	}
	switch {
	case mt == "text/html", mt == "image/svg+xml", mt == "application/xhtml+xml":
		return true
	case strings.HasSuffix(mt, "/xml"), strings.HasSuffix(mt, "+xml"), strings.Contains(mt, "xsl"):
		return true
	}
	return false
}

// sandboxWriter decides on the final Content-Type (after http.ServeFile has
// looked at the extension or sniffed the content) whether the response needs
// a sandbox, so a script-bearing file never runs with this origin's cookies,
// whatever it is named. Other types are left alone: PDF viewers refuse to
// render inside a sandbox.
//
// A top-level navigation (Sec-Fetch-Dest: document) gets a sandbox without
// scripts: shown full-page on our domain, a planted page could otherwise
// draw a convincing fake login form. The in-app preview iframe keeps
// scripts, still in an opaque origin.
//
// The exception is "Open original" (a full tab, scripts running, still in an
// opaque origin): allowed for preview capability URLs, which only someone
// already signed in (or holding the share) can obtain, and in local mode,
// which has no login form to fake. See withTopLevelScripts.
type sandboxWriter struct {
	http.ResponseWriter
	wroteHeader bool
	topLevel    bool
}

type topLevelScriptsKey struct{}

// withTopLevelScripts marks a request whose script-bearing response may run
// scripts even when opened as a top-level page.
func withTopLevelScripts(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), topLevelScriptsKey{}, true))
}

func topLevelScriptsAllowed(r *http.Request) bool {
	ok, _ := r.Context().Value(topLevelScriptsKey{}).(bool)
	return ok
}

func newSandboxWriter(w http.ResponseWriter, r *http.Request) *sandboxWriter {
	topLevel := r.Header.Get("Sec-Fetch-Dest") == "document" && !topLevelScriptsAllowed(r)
	return &sandboxWriter{ResponseWriter: w, topLevel: topLevel}
}

func (w *sandboxWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		if scriptableType(h.Get("Content-Type")) {
			if w.topLevel {
				h.Set("Content-Security-Policy", "sandbox")
			} else {
				// connect-src 'none': a page's scripts can't read other files
				// (e.g. with a preview URL's token) or send what they read.
				h.Set("Content-Security-Policy", "sandbox allow-scripts; connect-src 'none'")
			}
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *sandboxWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
