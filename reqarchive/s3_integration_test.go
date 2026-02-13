// Copyright © 2021 Luther Systems, Ltd. All right reserved.

//go:build integration

package reqarchive

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
)

var (
	testS3Region = flag.String("test-s3-region", "", "test bucket region for S3 archiver")
	testS3Bucket = flag.String("test-s3-bucket", "", "test bucket for S3 archiver")
)

func TestS3Put(t *testing.T) {
	logger, hook := logtest.NewNullLogger()
	a, err := NewS3Archiver(*testS3Region, *testS3Bucket, "test",
		WithLogBase(logrus.NewEntry(logger)),
	)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/test", nil)
	setTraceHeader(req, "request-id")
	rr := httptest.NewRecorder()
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})
	a.Wrap(next).ServeHTTP(rr, req)
	s3a := a.(*archiver)
	s3a.backend.Done()
	require.Len(t, hook.Entries, 0)
}
