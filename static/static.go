package static

import (
	"io/fs"
	"net/http"
)

// RegisterStaticDirectory mounts a file server at /static/ for anything under the given dir.
func RegisterStaticDirectory(fsys fs.FS, dir string, register func(route string, handler http.Handler)) {
	handler := http.StripPrefix("/static/", http.FileServer(http.FS(fsys)))
	register("/static/", handler)
}
