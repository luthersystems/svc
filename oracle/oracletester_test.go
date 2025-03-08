package oracle

import (
	"testing"

	"github.com/luthersystems/lutherauth-sdk-go/jwt"
	"github.com/stretchr/testify/require"
)

// TestNewTestOracle demonstrates usage of NewTestOracle and snapshot/restore logic.
func TestNewTestOracle(t *testing.T) {
	cfg := &Config{
		PhylumPath:        "./testservice/phylum",
		ServiceName:       "test_oracle",
		PhylumServiceName: "phylum",
		EmulateCC:         true,
		RequestIDHeader:   "X-Request-ID",
	}

	_, err := cfg.AddFakeIDP(t)
	require.NoError(t, err, "add fake IDP")
	orc, closeFunc := NewTestOracle(t, cfg)
	defer closeFunc()

	snap1 := orc.Snapshot(t)
	require.NotNil(t, snap1, "Snapshot should not be nil")
	require.NotEmpty(t, snap1, "Snapshot should not be empty")

	t.Logf("First snapshot length: %d bytes", len(snap1))

	orc2, closeFunc2 := NewTestOracle(t, cfg, WithSnapshot(snap1))
	defer closeFunc2()

	snap2 := orc2.Snapshot(t)
	require.NotNil(t, snap2, "Second snapshot should not be nil")
	require.NotEmpty(t, snap2, "Second snapshot should not be empty")
	t.Logf("Second snapshot length: %d bytes", len(snap2))

	fakeCtx := orc.MakeTestAuthContext(t, jwt.NewClaims("sam@luther.systems", "luther:auth:svc-local", "lutherapp:svc"))
	require.NotNil(t, fakeCtx, "fake context")
}
