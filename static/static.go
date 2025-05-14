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
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

const PublicFSDirSegment = "public"
const PublicPathPrefix = "/" + PublicFSDirSegment + "/"

// PublicHandler returns an http.Handler that serves embedded files under the
// "public/" subdirectory of the provided embed.FS. This content MUST be served
// under the /public pattern
func PublicHandler(staticFS embed.FS) (http.Handler, error) {
	return publicContentHandler(staticFS, PublicFSDirSegment, PublicFSDirSegment)
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
func publicContentHandler(embeddedFS embed.FS, subdir, urlPrefix string) (http.Handler, error) {

	cleanStaticDir := strings.Trim(subdir, "/")
	cleanURLPrefix := strings.Trim(urlPrefix, "/")
	subFS, err := fs.Sub(embeddedFS, cleanStaticDir)
	if err != nil {
		return nil, fmt.Errorf("cannot create sub FS: %w", err)
	}

	prefix := fmt.Sprintf("/%s/", cleanURLPrefix)
	return http.StripPrefix(prefix, http.FileServer(http.FS(subFS))), nil
}
