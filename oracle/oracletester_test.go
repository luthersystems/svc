package oracle

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNewTestOracle demonstrates usage of NewTestOracle and snapshot/restore logic.
func TestNewTestOracle(t *testing.T) {
	// 1) Create a config for the test oracle.
	//    Typically you'd set EmulateCC = true and any other fields you need.
	cfg := &Config{
		PhylumPath:        "./testservice/phylum",
		ServiceName:       "test_oracle",
		PhylumServiceName: "phylum",
		EmulateCC:         true,
		ListenAddress:     ":0",           // or some other port, e.g. ":8080"
		RequestIDHeader:   "X-Request-ID", // Add this to satisfy "missing request ID header"
	}

	// 2) Create a brand-new test oracle (no snapshot).
	orc, closeFunc := NewTestOracle(t, cfg)
	defer closeFunc()

	// 3) Take a snapshot of the current oracle’s state.
	snap1 := orc.Snapshot(t)
	require.NotNil(t, snap1, "Snapshot should not be nil")
	require.NotEmpty(t, snap1, "Snapshot should not be empty")

	t.Logf("First snapshot length: %d bytes", len(snap1))

	// 4) Now create a second test oracle from that snapshot.
	orc2, closeFunc2 := NewTestOracle(t, cfg, WithSnapshot(snap1))
	defer closeFunc2()

	// 5) Confirm it runs & can produce a second snapshot.
	snap2 := orc2.Snapshot(t)
	require.NotNil(t, snap2, "Second snapshot should not be nil")
	require.NotEmpty(t, snap2, "Second snapshot should not be empty")
	t.Logf("Second snapshot length: %d bytes", len(snap2))

	// Optionally, you can confirm that both snapshots differ or are the same,
	// depending on whether you changed anything in orc2. Usually the
	// second snapshot might differ slightly due to phylum timestamps, etc.
}
