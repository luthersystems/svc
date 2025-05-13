package static

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"

	"github.com/sirupsen/logrus"
)

type swaggerHandler []byte

// NOTE WIP

// call with // srvpb/v1/oracle.swagger.json
func SwaggerHandlerOrPanic(filePath string, file embed.FS) http.Handler {
	if h, err := httpHandler(filePath, file); err != nil {
		panic(err)
	} else {
		return h
	}
}

type svcHandler []byte

func (b svcHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, err := io.Copy(w, bytes.NewReader([]byte(b)))
	if err != nil {
		logrus.Error(err)
	}
}
func httpHandler(filePath string, files embed.FS) (http.Handler, error) {
	b, err := fs.ReadFile(files, filePath) // srvpb/v1/oracle.swagger.json
	if err != nil {
		return nil, err
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("document does not contain a valid json object")
	}
	return svcHandler(b), nil
}
