// Copyright © 2024 Luther Systems, Ltd. All right reserved.

package oracle

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/luthersystems/lutherauth-sdk-go/jwt"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type testWriter struct {
	t *testing.T
	b *bytes.Buffer
}

func newTestWriter(t *testing.T) *testWriter {
	var b bytes.Buffer
	return &testWriter{t: t, b: &b}
}

func (tw testWriter) Write(p []byte) (n int, err error) {
	tw.t.Helper()
	for _, b := range p {
		if b == '\n' {
			tw.t.Log(tw.b.String())
			tw.b.Reset()
			continue
		}
		// bytes.Buffer panics on error
		tw.b.WriteByte(b)
	}
	return n, nil
}

// Snapshot takes a snapshot of the current oracle.
func (orc *Oracle) Snapshot(t *testing.T) []byte {
	orc.stateMut.RLock()
	defer orc.stateMut.RUnlock()
	if orc.state != oracleStateTesting {
		panic(fmt.Errorf("snapshot: invalid oracle state: %d", orc.state))
	}

	var snapshot bytes.Buffer
	err := orc.phylum.MockSnapshot(&snapshot)
	require.NoError(t, err)
	return snapshot.Bytes()
}

type testCfg struct {
	snapshot []byte
}

// TestOpt configures a test oracle.
type TestOpt func(*testCfg)

// WithSnapshot restores the test oracle from a snapshot.
func WithSnapshot(b []byte) TestOpt {
	return func(cfg *testCfg) {
		cfg.snapshot = make([]byte, len(b))
		copy(cfg.snapshot, b)
	}
}

func getFreeAddr() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0") // OS assigns an available port
	if err != nil {
		return "", fmt.Errorf("failed to get a free port: %w", err)
	}
	defer l.Close() // Close immediately so it can be reused
	return l.Addr().String(), nil
}

// NewTestOracle is used to create an oracle for testing.
func NewTestOracle(t *testing.T, cfg *Config, testOpts ...TestOpt) (*Oracle, func()) {
	cfg.Verbose = testing.Verbose()
	cfg.EmulateCC = true
	cfg.Version = "test"

	port, err := getFreeAddr()
	require.NoError(t, err)

	if cfg.ListenAddress == "" {
		cfg.ListenAddress = port
	}

	require.NoError(t, cfg.Valid())

	testCfg := &testCfg{}
	for _, opt := range testOpts {
		opt(testCfg)
	}

	logger := logrus.New()
	logger.SetOutput(newTestWriter(t))

	var r io.Reader
	if testCfg.snapshot != nil {
		r = bytes.NewReader(testCfg.snapshot)
	}

	orcOpts := []option{
		withLogBase(logger.WithFields(nil)),
		withMockPhylumFrom(cfg.PhylumPath, r),
	}

	server, err := newOracle(cfg, orcOpts...)
	if err != nil {
		t.Fatal(err)
	}

	server.state = oracleStateTesting

	if cfg.Verbose {
		logger.SetLevel(logrus.DebugLevel)
	}

	orcStop := func() {
		err := server.close()
		require.NoError(t, err)
	}

	return server, orcStop
}

// mockServerTransportStream is a mock implementation of grpc.ServerTransportStream.
type mockServerTransportStream struct {
}

// Method satisfies the grpc.ServerTransportStream interface.
func (m *mockServerTransportStream) Method() string {
	return ""
}

// SetHeader satisfies the grpc.ServerTransportStream interface.
func (m *mockServerTransportStream) SetHeader(md metadata.MD) error {
	return nil
}

// SendHeader satisfies the grpc.ServerTransportStream interface.
func (m *mockServerTransportStream) SendHeader(md metadata.MD) error {
	return nil
}

// SetTrailer satisfies the grpc.ServerTransportStream interface.
func (m *mockServerTransportStream) SetTrailer(md metadata.MD) error {
	return nil
}

func makeTestContext(_ *testing.T) context.Context {
	// Create a context with a mock server transport stream.
	return grpc.NewContextWithServerTransportStream(context.Background(), &mockServerTransportStream{})
}

func (orc *Oracle) MakeTestAuthContext(t *testing.T, claims *jwt.Claims) context.Context {
	if orc == nil || orc.cfg.fakeIDP == nil || orc.cfg.authCookieForwarder == nil {
		t.Fatal("oracle not configured for auth")
	}

	// Create a fake token using the fake IDP.
	token, err := orc.cfg.fakeIDP.MakeFakeIDPAuthToken(claims)
	if err != nil {
		t.Fatalf("failed to create fake auth token: %v", err)
	}

	// Create a new test context.
	ctx := makeTestContext(t)

	// Use the auth cookie forwarder to set the token.
	// GetValue will return the cached (last written) token if available,
	// and if not, it will fall back to the incoming metadata.
	orc.cfg.authCookieForwarder.SetValue(ctx, token)

	return ctx
}
