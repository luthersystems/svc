// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package reqarchive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	jwtgo "github.com/golang-jwt/jwt/v4"
	"github.com/sirupsen/logrus"
)

var (
	defaultTimeout = 1 * time.Minute
)

type archiver struct {
	logBase      *logrus.Entry
	traceHeader  string
	ignoredPaths map[string]bool
	backend      backend
}

type backend interface {
	Write(ctx context.Context, reqID string, content []byte)
	Done()
}

type objectData struct {
	Path   string                  `json:"path"`
	Query  string                  `json:"query"`
	Method string                  `json:"method"`
	Body   *json.RawMessage        `json:"body"`
	Claims *jwtgo.RegisteredClaims `json:"claims"`
}

// Wrap implements the Middleware interface
func (a *archiver) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ignoredPath(a.ignoredPaths, r.URL.Path) {
			err := a.put(r)
			if err != nil {
				a.log(r).WithError(err).Error("request archiver put failed")
			}
		}
		next.ServeHTTP(w, r)
	})
}

// copyBody returns a copy of a request body and resets the body to a new reader
// for future use
func copyBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return []byte{}, nil
	}
	bodyContent, err := io.ReadAll(r.Body)
	if err == nil {
		_ = r.Body.Close()
	}
	r.Body = io.NopCloser(bytes.NewBuffer(bodyContent))
	return bodyContent, err
}

func hasJSONBody(r *http.Request, bodyContent *[]byte) (bool, error) {
	if len(*bodyContent) == 0 {
		return false, nil
	}
	// Check Content-Type header
	contentType := r.Header.Get("Content-Type")
	mType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false, fmt.Errorf("unable to parse Content-Type header '%s': %v", contentType, err)
	}
	// Only support JSON for now
	if mType != "application/json" {
		return false, fmt.Errorf("unable to handle Content-Type: %s", contentType)
	}
	return true, nil
}

func requestCookie(request *http.Request, name string) *http.Cookie {
	cookies := request.Cookies()
	var foundCookie *http.Cookie
	for _, cookie := range cookies {
		if strings.EqualFold(cookie.Name, name) {
			foundCookie = cookie
			break
		}
	}
	return foundCookie
}

// put writes a JSON document containing a request path, method, query string
// and body to S3
func (a *archiver) put(r *http.Request) error {
	reqID := a.reqID(r)
	if reqID == "" {
		return errors.New("request archiver failed to get request id")
	}
	bodyContent, err := copyBody(r)
	if err != nil {
		return err
	}
	bodyIsJSON, err := hasJSONBody(r, &bodyContent)
	if err != nil {
		// log error, then proceed without saving body
		a.log(r).WithError(err).Debug("request archiver unable to read body")
	}
	var reqClaims *jwtgo.RegisteredClaims
	cookie := requestCookie(r, "authorization")
	if cookie != nil {
		parser := &jwtgo.Parser{}
		token, _, err := parser.ParseUnverified(cookie.Value, &jwtgo.RegisteredClaims{})
		// Don't log, just omit invalid cookies
		if err == nil {
			reqClaims, _ = token.Claims.(*jwtgo.RegisteredClaims)
		}
	}
	content := objectData{
		Path:   r.URL.Path,
		Query:  r.URL.RawQuery,
		Method: r.Method,
		Body:   nil,
		Claims: reqClaims,
	}
	if bodyIsJSON {
		body := json.RawMessage(bodyContent)
		content.Body = &body
	}
	jsonContent, err := json.Marshal(content)
	if err != nil {
		return err
	}
	a.backend.Write(r.Context(), reqID, jsonContent)
	return nil
}

func (a *archiver) logReqID(reqID string) *logrus.Entry {
	return a.logBase.WithField("req_id", reqID)
}

func (a *archiver) log(r *http.Request) *logrus.Entry {
	return a.logReqID(a.reqID(r))
}

func (a *archiver) reqID(r *http.Request) string {
	return r.Header.Get(a.traceHeader)
}

func ignoredPath(ignoredPaths map[string]bool, path string) bool {
	if _, ignored := ignoredPaths[path]; ignored {
		return true
	}
	return false
}
