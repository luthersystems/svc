# Determinism

`hbs` must render the same bytes for the same template and context on every
peer, whatever its CPU. This file lists the places where raymond (and svc's
helpers on it) did not, and what `hbs` does instead.

## `to-int` on NaN, ±Inf and out-of-range floats

svc's `to-int` converted a float64 with Go's `int(f)`. The Go spec leaves that
conversion implementation-defined when the value does not fit in the integer
type, and the result depends on the CPU:

| Input | amd64 (`CVTTSD2SQ`) | arm64 (`FCVTZS`) |
|---|---|---|
| NaN | `math.MinInt64` | `0` |
| +Inf, `1e19`, `2^63` | `math.MinInt64` | `math.MaxInt64` |
| -Inf, `-1e19` | `math.MinInt64` | `math.MinInt64` |
| `-2^63` (in range) | `math.MinInt64` | `math.MinInt64` |
| finite, in `[-2^63, 2^63)` | truncated toward zero | truncated toward zero |

amd64 returns the "integer indefinite" value `0x8000000000000000` for every
input it cannot convert.

`hbs` pins the **amd64 result on every CPU, in both `ModeCompat` and
`ModeFixed`** (`floatToInt` in `helpers_svc.go`). It range-checks first and
uses the hardware conversion only for in-range values, where the spec defines
the result. Examples: `{{to-int (div 1 0)}}`, `{{to-int (div 0 0)}}` and
`{{to-int x}}` with context `x = 1e19` all render `-9223372036854775808`.

The same rule covers float32 values in `ModeFixed` (a float32 widens to
float64 exactly, and amd64's `CVTTSS2SQ` has the same semantics). In
`ModeCompat` a float32 is still a render error, as svc's type assertion
panicked.

This was audited for every float-to-int conversion in `hbs`:

- `to-int` (`toInt`): pinned as above.
- `int` helper parameters in `ModeFixed` (`convertArg`): only integral
  values inside `[-2^63, 2^63)` convert; others are a type error.
- `prettyp-num-en` (go-humanize `FormatFloat`): handles NaN and ±Inf itself
  and converts only the fractional part times 100, which is always in range.
- Other numeric helpers (`times`, `div`, `mod`, `plus`, `minus`, `gt`...)
  stay in float64 and print with `strconv`, which is the same everywhere.

32-bit CPUs are not supported: `int` is 32 bits there, so results differ
from amd64 for large values even though they are deterministic.

### Differential tests

The frozen reference (`internal/raymondref` with svc's helpers) still uses
the hardware conversion. On amd64 it agrees with `hbs`, and the tests compare
these cases. On any other CPU the differential and fuzz tests skip the
comparison for a run in which the reference's `to-int` got a float that is
NaN, ±Inf or outside `[-2^63, 2^63)` (`refNoteToInt` in `diff_test.go`).
Those runs are allowed differences for non-amd64 reference runs.

### Upgrade note

On an amd64 peer nothing changes. A non-amd64 peer that ran raymond rendered
different text for these inputs; it now renders the amd64 text.

## Integer literals near 2^63

The lexer parses a template's number literal as a float64, and an integer
literal is then converted with `int()`. A literal within about 512 of 2^63
(for example `9223372036854775807`) rounds to the float64 2^63, which is out
of range, so raymond's `int()` gave math.MinInt64 on amd64 and saturated on
arm64. `ast.NumberLiteral.Number` now converts with the same pinned rule as
`to-int` (`internal/floatint`), in both modes: `{{to-str
9223372036854775807}}` renders `-9223372036854775808` on every CPU. A
literal of 9223372036854775295 or less is in range and unchanged.

The harness allowlist entry for non-amd64 reference runs covers these
cases too. On amd64 nothing changes.
