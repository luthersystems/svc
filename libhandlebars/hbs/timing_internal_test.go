// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import "os"

// timingGuards reports whether the tests that assert wall-clock time run:
// only with HBS_TIMING=1, as their thresholds depend on the machine and its
// load (go test runs packages in parallel).
func timingGuards() bool { return os.Getenv("HBS_TIMING") == "1" }
