// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package midware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

var basicHandler = staticBytes([]byte("applicationdata"))

func TestPathOverrides(t *testing.T) {
	t.Run("PathOverrides.Wrap() handles matching without subtree validation", func(t *testing.T) {
		basicOverride := PathOverrides{
			"/override":        staticBytes([]byte("overridden")),
			"/api/":            staticBytes([]byte("api handler")),
			"/api/nested-api/": staticBytes([]byte("nested api handler")),
			"/v1/public/":      staticBytes([]byte("public handler")),
		}

		h := basicOverride.Wrap(staticBytes([]byte("applicationdata")))

		testServer(t, h, func(t *testing.T, server *httptest.Server) {
			t.Run("falls back to next handler on root", func(t *testing.T) {
				assert.Equal(t, []byte("applicationdata"), testRequest(t, server, "GET", "/", nil, nil))
			})

			t.Run("falls back to next handler on unmatched path", func(t *testing.T) {
				assert.Equal(t, []byte("applicationdata"), testRequest(t, server, "GET", "/hello/world", nil, nil))
			})

			t.Run("exact match override works", func(t *testing.T) {
				assert.Equal(t, []byte("overridden"), testRequest(t, server, "GET", "/override", nil, nil))
			})

			t.Run("non-exact override should fall back", func(t *testing.T) {
				assert.Equal(t, []byte("applicationdata"), testRequest(t, server, "GET", "/override/2", nil, nil))
			})

			t.Run("prefix match with /api/ works", func(t *testing.T) {
				assert.Equal(t, []byte("api handler"), testRequest(t, server, "GET", "/api/user/42", nil, nil))
			})

			t.Run("prefix match with /api/nested-api/ chooses longest path (/api/nested-api/)", func(t *testing.T) {
				assert.Equal(t, []byte("nested api handler"), testRequest(t, server, "GET", "/api/nested-api/user/42", nil, nil))
			})

			t.Run("prefix match with v1/public/ works", func(t *testing.T) {
				assert.Equal(t, []byte("public handler"), testRequest(t, server, "GET", "/v1/public/assets/logo.png", nil, nil))
			})
		})
	})

	t.Run("ProtectedPathOverrides.Wrap() panics on nested override under blocked subtree", func(t *testing.T) {
		assert.PanicsWithValue(t,
			`PathOverride conflict: attempted to register route "/v1/public/nested/" under protected subtree "/v1/public/"`,
			func() {
				_ = NewProtectedPathOverrides(
					map[string]http.Handler{
						"/v1/public/":        staticBytes([]byte("good")),
						"/v1/public/nested/": staticBytes([]byte("bad")),
					},
					[]string{"/v1/public/"},
				).Wrap(staticBytes([]byte("fallback")))
			})
	})
}

func TestServerResponseHeader(t *testing.T) {
	h := ServerResponseHeader(ServerFixed("testsvc", "")).Wrap(basicHandler)
	testServer(t, h, func(t *testing.T, server *httptest.Server) {
		assert.Len(t, testResponseHeaders(t, server, "GET", "/", nil, nil).Values("Server"), 1)
		assert.Equal(t, "testsvc", testResponseHeaders(t, server, "GET", "/", nil, nil).Get("Server"))
	})
	h = ServerResponseHeader(ServerFixed("testsvc", "1.0")).Wrap(basicHandler)
	testServer(t, h, func(t *testing.T, server *httptest.Server) {
		assert.Len(t, testResponseHeaders(t, server, "GET", "/", nil, nil).Values("Server"), 1)
		assert.Equal(t, "testsvc/1.0", testResponseHeaders(t, server, "GET", "/", nil, nil).Get("Server"))
	})
	h = ServerResponseHeader(ServerFixed("testsvc", "1.0"), ServerFixedFunc("downstreamsvc", "")).Wrap(basicHandler)
	testServer(t, h, func(t *testing.T, server *httptest.Server) {
		assert.Len(t, testResponseHeaders(t, server, "GET", "/", nil, nil).Values("Server"), 1)
		assert.Equal(t, "testsvc/1.0 downstreamsvc", testResponseHeaders(t, server, "GET", "/", nil, nil).Get("Server"))
	})

	assert.Panics(t, func() { ServerResponseHeader("") })
	assert.Panics(t, func() { ServerResponseHeader(" ") })

	h = &serverListHandler{next: basicHandler} // not a valid construction
	testServer(t, h, func(t *testing.T, server *httptest.Server) {
		assert.Len(t, testResponseHeaders(t, server, "GET", "/", nil, nil).Values("Server"), 1)
		assert.NotEmpty(t, testResponseHeaders(t, server, "GET", "/", nil, nil).Get("Server"))
	})
}

func TestTraceHeaders(t *testing.T) {
	h := TraceHeaders("", false).Wrap(basicHandler)
	testServer(t, h, func(t *testing.T, server *httptest.Server) {
		assert.NotEmpty(t, testResponseHeaders(t, server, "GET", "/", nil, nil).Get(DefaultTraceHeader))
		reqid1 := testResponseHeaders(t, server, "GET", "/", nil, nil).Get(DefaultTraceHeader)
		reqid2 := testResponseHeaders(t, server, "GET", "/", nil, nil).Get(DefaultTraceHeader)
		assert.NotEqual(t, reqid1, reqid2)
		header := testResponseHeaders(t, server, "GET", "/", nil, nil)
		if assert.Len(t, header[DefaultTraceHeader], 1) {
			assert.Equal(t, header.Get(DefaultTraceHeader), header[DefaultTraceHeader][0])
		}
		badid := "no"
		assert.NotEqual(t, badid, testResponseHeaders(t, server, "GET", "/", http.Header{DefaultTraceHeader: []string{badid}}, nil).Get(DefaultTraceHeader))
	})
	h = TraceHeaders("", true).Wrap(basicHandler)
	testServer(t, h, func(t *testing.T, server *httptest.Server) {
		assert.NotEmpty(t, testResponseHeaders(t, server, "GET", "/", nil, nil).Get(DefaultTraceHeader))
		fixed := "yes"
		assert.Equal(t, fixed, testResponseHeaders(t, server, "GET", "/", http.Header{DefaultTraceHeader: []string{fixed}}, nil).Get(DefaultTraceHeader))
	})
	h = TraceHeaders(DefaultAzureHeader, true).Wrap(basicHandler)
	testServer(t, h, func(t *testing.T, server *httptest.Server) {
		traceId1 := "ee59e664-dda3-4cea-b9e2-17ff84770814"
		assert.Equal(t, traceId1, testResponseHeaders(t, server, "GET", "/", http.Header{DefaultTraceHeader: []string{traceId1}}, nil).Get(DefaultTraceHeader))

		traceId2 := "585d8935-11bd-4c7e-a428-9a9094adf28b"
		assert.Equal(t, traceId2, testResponseHeaders(t, server, "GET", "/", http.Header{
			DefaultAWSHeader:   []string{traceId1},
			DefaultAzureHeader: []string{traceId2},
		}, nil).Get(DefaultAzureHeader))
		assert.Empty(t, testResponseHeaders(t, server, "GET", "/", http.Header{
			DefaultAWSHeader:   []string{traceId1},
			DefaultAzureHeader: []string{traceId2},
		}, nil).Get(DefaultAWSHeader))
	})
}
