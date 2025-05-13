package static

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

// staticContentHandlerOrPanic returns an http.Handler that serves embedded files from a
// subdirectory within the embed.FS (e.g., "static") and maps them to a given URL prefix.
//
// For example:
//   - Embedded files live under embed.FS path "static/**"
//   - You want to serve them at the URL prefix "/assets/"
//
// Call:
//
//	staticContentHandlerOrPanic(staticFS, "static", "assets")
//
// Then a request to /assets/index.html will serve embedded file "static/index.html".
func publicContentHandlerOrPanic(staticFS embed.FS, staticDirPath, urlPrefix string) http.Handler {
	cleanStaticDir := strings.Trim(staticDirPath, "/")
	cleanURLPrefix := strings.Trim(urlPrefix, "/")

	subFS, err := fs.Sub(staticFS, cleanStaticDir)
	if err != nil {
		panic(fmt.Errorf("cannot create sub FS: %w", err))
	}

	prefix := fmt.Sprintf("/%s/", cleanURLPrefix)
	return http.StripPrefix(prefix, http.FileServer(http.FS(subFS)))
}

// PublicHandlerOrPanic returns an http.Handler that serves files embedded under
// the "public/" directory. It mounts them at the /public/ URL path.
//
// This assumes the embed directive looks like:
//
//	//go:embed public/**
//
// A request to /public/file.json will serve the embedded file
// "public/file.json". Files outside of public/ will not be served.
func PublicHandlerOrPanic(staticFS embed.FS) http.Handler {
	return publicContentHandlerOrPanic(staticFS, "public", "public")
}
