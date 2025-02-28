// Copyright © 2024 Luther Systems, Ltd. All right reserved.

package oracle

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
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

	cfg.ListenAddress = port

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
	server.state = oracleStateTesting
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Verbose {
		logger.SetLevel(logrus.DebugLevel)
	}

	orcStop := func() {
		err := server.close()
		require.NoError(t, err)
	}

	return server, orcStop
}
