package static

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

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

// PublicHandler returns an http.Handler that serves embedded files under
// the "public/" subdirectory of the provided embed.FS.
//
// The handler will serve files at the /public/ URL path,
// using a prefix-stripped view rooted at "public".
//
// Example embed directive:
//
//	//go:embed public/**
//
// Example result:
//	/public/index.html → serves embedded file public/index.html

func PublicHandler(staticFS embed.FS) (http.Handler, error) {
	return publicContentHandler(staticFS, "public", "public")
}
