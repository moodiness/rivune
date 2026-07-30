package webui

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed dist/* dist/assets/*
var content embed.FS

var files = mustSub(content, "dist")

const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: http: https:; media-src 'self' blob: http: https:; connect-src 'self' blob: http: https:; frame-src https://www.youtube-nocookie.com; worker-src blob:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func mustSub(source fs.FS, directory string) fs.FS {
	subtree, err := fs.Sub(source, directory)
	if err != nil {
		panic(err)
	}
	return subtree
}

// Handler serves the embedded web client and falls back to its entry point for
// browser routes. API-like paths never receive HTML.
func Handler(w http.ResponseWriter, r *http.Request) {
	requestPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if requestPath == "." {
		requestPath = "index.html"
	}

	file, err := fs.ReadFile(files, requestPath)
	if err == nil {
		serveFile(w, requestPath, file)
		return
	}
	if strings.HasPrefix(requestPath, "api/") || strings.HasPrefix(requestPath, ".well-known/") || requestPath == "health" || path.Ext(requestPath) != "" {
		http.NotFound(w, r)
		return
	}

	index, indexErr := fs.ReadFile(files, "index.html")
	if indexErr != nil {
		http.Error(w, "web client unavailable", http.StatusServiceUnavailable)
		return
	}
	serveFile(w, "index.html", index)
}

func serveFile(w http.ResponseWriter, name string, content []byte) {
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	} else if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(content)
}
