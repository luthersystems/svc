// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package reqarchive

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luthersystems/svc/midware"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
)

type mockBackend struct {
	test func(reqID string, content []byte)
}

func (b *mockBackend) Write(_ context.Context, reqID string, content []byte) {
	b.test(reqID, content)
}

func (b *mockBackend) Done() {}

func setTraceHeader(r *http.Request, id string) {
	r.Header.Set(midware.DefaultTraceHeader, id)
}

func TestPut(t *testing.T) {
	backend := &mockBackend{
		test: func(_ string, content []byte) {
			var data objectData
			err := json.Unmarshal(content, &data)
			require.NoError(t, err)
			require.Equal(t, "/foo", data.Path)
			require.NotNil(t, data.Body)
			var m map[string]bool
			err = json.Unmarshal(*data.Body, &m)
			require.NoError(t, err)
			require.True(t, m["Hello"])
		},
	}
	logger, hook := logtest.NewNullLogger()
	archiver := &archiver{
		logBase:     logrus.NewEntry(logger),
		backend:     backend,
		traceHeader: midware.DefaultTraceHeader,
	}
	logrus.SetLevel(logrus.DebugLevel)
	b, err := json.Marshal(map[string]bool{"Hello": true})
	require.NoError(t, err)
	body := bytes.NewReader(b)
	req := httptest.NewRequest(http.MethodPut, "/foo", body)
	req.Header.Set("Content-Type", "application/json")
	setTraceHeader(req, "request-id")
	err = archiver.put(req)
	require.NoError(t, err)
	require.Len(t, hook.Entries, 0)
}

func TestFilter(t *testing.T) {
	backend := &mockBackend{
		test: func(_ string, _ []byte) {
			t.Fatal("didn't expect archival call")
		},
	}
	logger, hook := logtest.NewNullLogger()
	archiver := &archiver{
		logBase:      logrus.NewEntry(logger),
		ignoredPaths: map[string]bool{"/healthcheck": true},
		backend:      backend,
	}
	logrus.SetLevel(logrus.DebugLevel)
	req := httptest.NewRequest(http.MethodPut, "/healthcheck", nil)
	rr := httptest.NewRecorder()
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})
	archiver.Wrap(next).ServeHTTP(rr, req)
	require.Len(t, hook.Entries, 0)
}
