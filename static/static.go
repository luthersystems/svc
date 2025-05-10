package static

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"

	"github.com/sirupsen/logrus"
)

// RegisterStaticDirectory mounts a file server at /static/ for anything under the given dir.
func RegisterStaticDirectory(fsys fs.FS, dir string, register func(route string, handler http.Handler)) {
	handler := http.StripPrefix("/static/", http.FileServer(http.FS(fsys)))
	register("/static/", handler)
}

// call with "static"
func StaticContentHandlerOrPanic(files embed.FS, dirPath string) http.Handler {
	entries, err := fs.ReadDir(files, dirPath)
	if err != nil {
		log.Fatalf("cannot list embedded %s/: %v", dirPath, err)
	}
	for _, e := range entries {
		fmt.Println("Embedded static file:", e.Name())
	}
	subFS, err := fs.Sub(files, dirPath)
	if err != nil {
		panic(fmt.Errorf("cannot create sub FS: %w", err))
	}
	// removes the /<dirPath>/ prefix from incoming request URLs, then passes the
	// modified URL to the handler.
	return http.StripPrefix(fmt.Sprintf("/%s/", dirPath), http.FileServer(http.FS(subFS)))
}

type svcHandler []byte

func (b svcHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, err := io.Copy(w, bytes.NewReader([]byte(b)))
	if err != nil {
		logrus.Error(err)
	}
}
