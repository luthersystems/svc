// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

type phoneStepCounter struct{ n int64 }

func (m *phoneStepCounter) Charge(steps int64) error {
	m.n += steps
	return nil
}

// TestPhoneWarmLazy runs, in a subprocess (so no other test has formatted a
// number first), a process that links hbs: the phonenumbers warm-up has not
// run at init; the first format-phone-gb runs it, and renders and charges
// exactly what a later, warm call does.
func TestPhoneWarmLazy(t *testing.T) {
	if os.Getenv("HBS_PHONE_WARM_CHILD") == "1" {
		require.False(t, phoneWarmed.Load(), "warm-up ran before any format-phone-gb")
		p, err := Parse(`{{format-phone-gb p}}`, DefaultLimits())
		require.NoError(t, err)
		ctx := map[string]any{"p": "+44 20 7946 0958"}
		render := func() (string, int64) {
			m := &phoneStepCounter{}
			out, err := p.Render(ctx, Options{Meter: m})
			require.NoError(t, err)
			return out, m.n
		}
		cold, coldSteps := render()
		require.True(t, phoneWarmed.Load(), "the first format-phone-gb did not warm phonenumbers")
		warm, warmSteps := render()
		require.Equal(t, "020 7946 0958", cold)
		require.Equal(t, warm, cold)
		require.Equal(t, warmSteps, coldSteps)
		require.GreaterOrEqual(t, coldSteps, int64(phoneCallCost))
		fmt.Println("PHONE-WARM-OK")
		return
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPhoneWarmLazy$", "-test.v") //nolint:gosec // this test binary
	cmd.Env = append(os.Environ(), "HBS_PHONE_WARM_CHILD=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	require.Contains(t, string(out), "PHONE-WARM-OK")
}
