package static

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/http"

	"github.com/sirupsen/logrus"
)

// NOTE WIP

// StaticContentHandlerOrPanic returns an http.Handler that serves files embedded under the given
// staticDirPath. It strips the /<staticDirPath>/ prefix from the incoming request URL so that
// files can be looked up correctly in the embedded file system.
//
// For example, if you embed files under "static/**", and mount the handler at "/static/",
// a request for /static/index.html will be rewritten to "index.html" and served from the
// embedded "static/" subdirectory. Without stripping the prefix, the lookup would incorrectly
// try to find "static/static/index.html".
func StaticContentHandlerOrPanic(staticFS embed.FS, staticDirPath string) http.Handler {
	subFS, err := fs.Sub(staticFS, staticDirPath)
	if err != nil {
		panic(fmt.Errorf("cannot create sub FS: %w", err))
	}
	return http.StripPrefix(fmt.Sprintf("/%s/", staticDirPath), http.FileServer(http.FS(subFS)))
}

type svcHandler []byte

func (b svcHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, err := io.Copy(w, bytes.NewReader([]byte(b)))
	if err != nil {
		logrus.Error(err)
	}
}

// StaticHandlerOrPanic returns an http.Handler that serves embedded files from the "static" directory.
// It expects files to be embedded with a go:embed directive like: `//go:embed static/**`
// The handler is mounted at /static/, and strips the /static/ prefix before file lookup.
func StaticHandlerOrPanic(staticFS embed.FS) http.Handler {
	return StaticContentHandlerOrPanic(staticFS, "static")
}
