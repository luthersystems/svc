package oracle

import (
	"testing"
	"time"

	"github.com/luthersystems/lutherauth-sdk-go/jwt"
	"github.com/stretchr/testify/require"
)

func newTestConfig(t *testing.T) *Config {
	cfg := &Config{
		PhylumPath:        "./testservice/phylum",
		ServiceName:       "test_oracle",
		PhylumServiceName: "phylum",
		EmulateCC:         true,
		RequestIDHeader:   "X-Request-ID",
	}

	_ = cfg.AddAuthCookieForwarder("svc_authorization", int(5*time.Minute.Seconds()), false, true)

	_, err := cfg.AddFakeIDP(t)
	require.NoError(t, err, "add fake IDP")

	return cfg
}

// TestNewTestOracle demonstrates core oracle logic.
func TestNewTestOracle(t *testing.T) {
	orc, closeFun := NewTestOracle(t, newTestConfig(t))
	t.Cleanup(closeFun)

	t.Run("test snapshot", func(t *testing.T) {
		snap1 := orc.Snapshot(t)
		require.NotNil(t, snap1, "Snapshot should not be nil")
		require.NotEmpty(t, snap1, "Snapshot should not be empty")

		orc2, closeFunc2 := NewTestOracle(t, newTestConfig(t), WithSnapshot(snap1))
		t.Cleanup(closeFunc2)

		snap2 := orc2.Snapshot(t)
		require.NotNil(t, snap2, "Second snapshot should not be nil")
		require.NotEmpty(t, snap2, "Second snapshot should not be empty")
	})

	t.Run("test fake IDP context", func(t *testing.T) {
		fakeCtx := orc.MakeTestAuthContext(t, jwt.NewClaims("sam@luther.systems", "luther:auth:svc-local", "lutherapp:svc"))
		require.NotNil(t, fakeCtx, "fake context")

		claims, err := orc.GetClaims(fakeCtx)
		require.NoError(t, err, "get claims")
		require.Equal(t, "sam@luther.systems", claims.Subject)
	})
}
