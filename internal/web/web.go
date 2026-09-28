// Package web serves the embedded browser interface. It contains only static
// assets; all data flows through the authenticated JSON API.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
)

//go:embed static
var files embed.FS

// CSP allows only same-origin scripts and styles, no inline code, and requires
// Trusted Types so that no HTML string sink can be used by the application.
const CSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; media-src 'self'; frame-src 'self'; connect-src 'self'; manifest-src 'self'; " +
	"form-action 'none'; base-uri 'none'; frame-ancestors 'none'; require-trusted-types-for 'script'; trusted-types 'none'"

var types = map[string]string{".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8", ".svg": "image/svg+xml", ".json": "application/json; charset=utf-8",
	".png": "image/png", ".ico": "image/x-icon", ".webmanifest": "application/manifest+json"}

func serve(w http.ResponseWriter, name string) {
	data, err := fs.ReadFile(files, "static/"+name)
	typ, known := types[path.Ext(name)]
	if err != nil || !known {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Security-Policy", CSP)
	w.Header().Set("Content-Type", typ)
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(data)
}

// Index serves the single page.
func Index(w http.ResponseWriter, _ *http.Request) { serve(w, "index.html") }

// Favicon serves /favicon.ico, which browsers request at the site root.
func Favicon(w http.ResponseWriter, _ *http.Request) { serve(w, "favicon.ico") }

// Share serves the public link page; the token stays in the URL and is read by
// share.js. The page is not indexed and never sends a Referer.
func Share(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	serve(w, "share.html")
}

// Asset serves one embedded file by exact name; there are no directories.
func Asset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "index.html" || name == "share.html" || name != path.Base(name) {
		http.NotFound(w, r)
		return
	}
	serve(w, name)
}
