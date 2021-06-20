// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package midware

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChain_empty(t *testing.T) {
	var c Chain
	h := c.Wrap(staticBytes([]byte("hello")))
	testServer(t, h, func(t *testing.T, server *httptest.Server) {
		assert.Equal(t, []byte("hello"), testRequest(t, server, "GET", "/", nil, nil))
	})
}

func TestChain(t *testing.T) {
	headerappend := func(header, value string) Middleware {
		return Func(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add(header, value)
				next.ServeHTTP(w, r)
			})
		})
	}
	c := Chain{
		headerappend("X-Test", "1"),
		headerappend("X-Test", "2"),
		headerappend("X-Test", "3"),
	}
	h := c.Wrap(staticBytes([]byte("hello")))
	testServer(t, h, func(t *testing.T, server *httptest.Server) {
		assert.Equal(t, []byte("hello"), testRequest(t, server, "GET", "/", nil, nil))
		assert.Equal(t, []string{"1", "2", "3"},
			testResponseHeaders(t, server, "GET", "/", nil, nil).Header.Values("X-Test"))
	})
}

func staticBytes(b []byte) http.Handler {
	return &staticHandler{body: b}
}

type staticHandler struct {
	code   int
	header http.Header
	body   []byte
}

func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	for k, v := range h.header {
		for i := range v {
			header.Add(k, v[i])
		}
	}
	if h.code == 0 {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(h.code)
	}
	b := h.body
	for len(b) > 0 {
		n, err := w.Write(b)
		b = b[n:]
		if err != nil {
			log.Printf("static handler error: %v", err)
		}
		if n == 0 {
			panic("static handler: no write progress")
		}
	}
}

func testServer(t *testing.T, h http.Handler, fn func(t *testing.T, server *httptest.Server)) {
	server := httptest.NewServer(h)
	defer server.Close()
	fn(t, server)
}

func testRequest(t *testing.T, server *httptest.Server, method string, rpath string, header http.Header, body io.Reader) []byte {
	t.Helper()
	r, err := http.NewRequest(method, serverURL(server, rpath), body)
	require.NoError(t, err, "invalid request parameters")
	for k, v := range header {
		for i := range v {
			r.Header.Add(k, v[i])
		}
	}
	resp, err := (&http.Client{}).Do(r)
	require.NoError(t, err, "request failure")
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "unable to read response")
	return b
}

func testResponseHeaders(t *testing.T, server *httptest.Server, method string, rpath string, header http.Header, body io.Reader) *http.Response {
	t.Helper()
	r, err := http.NewRequest(method, serverURL(server, rpath), body)
	require.NoError(t, err, "invalid request parameters")
	for k, v := range header {
		for i := range v {
			r.Header.Add(k, v[i])
		}
	}
	resp, err := (&http.Client{}).Do(r)
	require.NoError(t, err, "request failure")
	defer resp.Body.Close()
	return resp
}

func serverURL(server *httptest.Server, rpath string) string {
	return server.URL + rpath
}
