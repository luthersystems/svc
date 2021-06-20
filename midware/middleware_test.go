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
	basicOverride := &PathOverrides{"/override": staticBytes([]byte("overridden"))}
	h := (basicOverride).Wrap(basicHandler)
	testServer(t, h, func(t *testing.T, server *httptest.Server) {
		assert.Equal(t, []byte("applicationdata"), testRequest(t, server, "GET", "/", nil, nil))
		assert.Equal(t, []byte("applicationdata"), testRequest(t, server, "GET", "/hello/world", nil, nil))
		assert.Equal(t, []byte("overridden"), testRequest(t, server, "GET", "/override", nil, nil))
		assert.Equal(t, []byte("applicationdata"), testRequest(t, server, "GET", "/override/2", nil, nil))
	})
}

func TestServerResponseHeader(t *testing.T) {
	h := ServerResponseHeader(ServerFixed("testsvc", "")).Wrap(basicHandler)
	testServer(t, h, func(t *testing.T, server *httptest.Server) {
		assert.Len(t, testResponseHeaders(t, server, "GET", "/", nil, nil).Header.Values("Server"), 1)
		assert.Equal(t, "testsvc", testResponseHeaders(t, server, "GET", "/", nil, nil).Header.Get("Server"))
	})
	h = ServerResponseHeader(ServerFixed("testsvc", "1.0")).Wrap(basicHandler)
	testServer(t, h, func(t *testing.T, server *httptest.Server) {
		assert.Len(t, testResponseHeaders(t, server, "GET", "/", nil, nil).Header.Values("Server"), 1)
		assert.Equal(t, "testsvc/1.0", testResponseHeaders(t, server, "GET", "/", nil, nil).Header.Get("Server"))
	})
	h = ServerResponseHeader(ServerFixed("testsvc", "1.0"), ServerFixedFunc("downstreamsvc", "")).Wrap(basicHandler)
	testServer(t, h, func(t *testing.T, server *httptest.Server) {
		assert.Len(t, testResponseHeaders(t, server, "GET", "/", nil, nil).Header.Values("Server"), 1)
		assert.Equal(t, "testsvc/1.0 downstreamsvc", testResponseHeaders(t, server, "GET", "/", nil, nil).Header.Get("Server"))
	})

	assert.Panics(t, func() { ServerResponseHeader("") })
	assert.Panics(t, func() { ServerResponseHeader(" ") })

	h = &serverListHandler{next: basicHandler} // not a valid construction
	testServer(t, h, func(t *testing.T, server *httptest.Server) {
		assert.Len(t, testResponseHeaders(t, server, "GET", "/", nil, nil).Header.Values("Server"), 1)
		assert.NotEmpty(t, testResponseHeaders(t, server, "GET", "/", nil, nil).Header.Get("Server"))
	})
}

func TestTraceHeaders(t *testing.T) {
	h := TraceHeaders("", false).Wrap(basicHandler)
	testServer(t, h, func(t *testing.T, server *httptest.Server) {
		assert.NotEqual(t, "", testResponseHeaders(t, server, "GET", "/", nil, nil).Header.Get(DefaultTraceHeader))
		reqid1 := testResponseHeaders(t, server, "GET", "/", nil, nil).Header.Get(DefaultTraceHeader)
		reqid2 := testResponseHeaders(t, server, "GET", "/", nil, nil).Header.Get(DefaultTraceHeader)
		assert.NotEqual(t, reqid1, reqid2)
		resp := testResponseHeaders(t, server, "GET", "/", nil, nil)
		if assert.Len(t, resp.Header[DefaultTraceHeader], 1) {
			assert.Equal(t, resp.Header.Get(DefaultTraceHeader), resp.Header[DefaultTraceHeader][0])
		}
		badid := "no"
		assert.NotEqual(t, badid, testResponseHeaders(t, server, "GET", "/", http.Header{DefaultTraceHeader: []string{badid}}, nil).Header.Get(DefaultTraceHeader))
	})
	h = TraceHeaders("", true).Wrap(basicHandler)
	testServer(t, h, func(t *testing.T, server *httptest.Server) {
		assert.NotEqual(t, "", testResponseHeaders(t, server, "GET", "/", nil, nil).Header.Get(DefaultTraceHeader))
		fixed := "yes"
		assert.Equal(t, fixed, testResponseHeaders(t, server, "GET", "/", http.Header{DefaultTraceHeader: []string{fixed}}, nil).Header.Get(DefaultTraceHeader))
	})
}
