package main

import "testing"

func TestScriptableType(t *testing.T) {
	for ct, want := range map[string]bool{
		"text/html; charset=utf-8":  true,
		"image/svg+xml":             true,
		"application/xhtml+xml":     true,
		"text/xml; charset=utf-8":   true,
		"application/xml":           true,
		"application/rdf+xml":       true,
		"application/xslt+xml":      true,
		"text/xsl":                  true,
		"application/pdf":           false,
		"image/png":                 false,
		"text/plain; charset=utf-8": false,
		"application/json":          false,
		"":                          false,
		"garbage;;=":                true,
	} {
		if got := scriptableType(ct); got != want {
			t.Errorf("scriptableType(%q) = %v, want %v", ct, got, want)
		}
	}
}
