package static

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
)

type swaggerHandler []byte

// call with // srvpb/v1/oracle.swagger.json
func SwaggerHandlerOrPanic(filePath string, file embed.FS) http.Handler {
	if h, err := httpHandler(filePath, file); err != nil {
		panic(err)
	} else {
		return h
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
