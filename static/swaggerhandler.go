package static

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
)

// RegisterSwagger mounts a single Swagger JSON file at /swagger.json
func RegisterSwagger(fsys fs.FS, path string, register func(route string, handler http.Handler)) error {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return fmt.Errorf("read swagger file %q: %w", path, err)
	}
	if !json.Valid(data) {
		return fmt.Errorf("swagger file %q is invalid JSON", path)
	}
	register("/swagger.json", swaggerHandler(data))
	return nil
}

type swaggerHandler []byte

func (b swaggerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.Copy(w, bytes.NewReader(b))
}
