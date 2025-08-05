// Package static provides HTTP handlers for serving embedded static content.
//
// It supports serving files from a subdirectory within an embed.FS at a specified
// URL prefix, such as mounting embedded "public/**" content at the "/public/" path.
//
// This package is typically used to expose browser-accessible static files like
// JavaScript bundles, CSS, or HTML generated at build time.
//
// Example usage:
//
//	//go:embed public/**
//	var PublicFS embed.FS
//
//	http.Handle("/public/", static.PublicHandler(PublicFS))
package static

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

const PublicFSDirSegment = "public"

// PublicHandler returns an http.Handler that serves files under the
// "public/" subdirectory of the provided fs.FS.  URL prefix should begin and
// end with "/" e.g. /v1/public/
func PublicHandler(staticFS fs.FS, mountPrefix string) (http.Handler, error) {
	return publicContentHandler(staticFS, PublicFSDirSegment, CleanPathPrefix(mountPrefix))
}

// CleanPathPrefix normalizes a URL path prefix, ensuring it starts
// and ends with a single "/" character. This is especially useful
// for mounting static directories or registering path-based handlers.
func CleanPathPrefix(prefix string) string {
	p := "/" + strings.Trim(prefix, "/")
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

// staticContentHandlerreturns an http.Handler that serves embedded files from a
// subdirectory within the embed.FS (e.g., "static") and maps them to a given URL prefix.
//
// For example:
//   - Embedded files live under embed.FS path "static/**"
//   - You want to serve them at the URL prefix "/assets/"
//
// Call:
//
//	staticContentHandler(staticFS, "static", "assets")
//
// Then a request to /assets/index.html will serve embedded file "static/index.html".
func publicContentHandler(embeddedFS fs.FS, subdir, urlPrefix string) (http.Handler, error) {
	cleanStaticDir := strings.Trim(subdir, "/")
	cleanURLPrefix := strings.Trim(urlPrefix, "/")
	subFS, err := fs.Sub(embeddedFS, cleanStaticDir)
	if err != nil {
		return nil, fmt.Errorf("cannot create sub FS: %w", err)
	}

	prefix := fmt.Sprintf("/%s/", cleanURLPrefix)
	return http.StripPrefix(prefix, http.FileServer(http.FS(subFS))), nil
}
