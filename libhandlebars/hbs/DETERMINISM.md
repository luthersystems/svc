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

## Cost model

A render's steps (the `Meter`'s units, bounded by `Limits.MaxSteps`) are a
pure function of the template, the context and the options. Work that grows
with the length of a string or key is charged by its length, through the
primitives in `output.go`, so steps bound wall time and memory. `KiB(n)` is
`max(1, ceil(n/1024))`.

| Operation | Where | Charge |
|---|---|---|
| Each AST node evaluated | `eval.go` `at` | 1 |
| Each path segment resolved | `evalPath` | 1, plus the key lookup below |
| Each context a lookup tries (mustache climb) | `evalDepthPath` | 1 |
| Each array element a path is mapped over | `evalCtxPath` | 1 |
| Map lookup or insert of a key of n bytes: context fields, string-literal paths, hash pair keys, `#each` object keys, `plus`/`minus` hash keys | `lookup`, `hashKey` | KiB(n) |
| Helper name lookup | `findHelper` | KiB(len(name)) |
| Array index segment of n bytes (`strconv.Atoi`) | `evalField` | max(1, ceil(n/64)) |
| Block parameter scan | `blockParam` | 1 per frame, plus each compare |
| String compare of equal lengths n (block params, `select`, `in-string-array`) | `compare` | KiB(n); unequal lengths 0 |
| Sorting k keys (`#each` objects, `plus`/`minus`, `%v` of objects) | `sortKeys` | sum of KiB(len) x ceil(log2(k+1)), before sorting |
| Collecting an object's keys for `#each` | `helperEach` | 1 per key, before collecting |
| `#each` iteration | `visitBlock`, `helperEach` | 1 |
| Helper call | `callHelper` | 1 |
| String argument of n bytes read by a helper | `read` via `convertArg`, `hashStr`, `toFloat`, `toInt` | KiB(n) |
| String built from a non-string value (`str`), element by element | `appendStrBounded` | 1 per element, plus produced bytes |
| `select` / `in-string-array` element scanned | helpers | KiB(len(key)) / 1, plus compares |
| `global` read or write | `hGlobal` | KiB(len(ns) + len(key)) |
| `round-to-nth` | `hRoundToNth` | precision checked against the produced-bytes bound before formatting; result charged as produced |
| `escape-uri-component` | `hEscapeURIComponent` | exact escaped length checked before escaping; result charged as produced |
| `prettyp-num-en` error text (fmt `%v` of the value) | `appendV` | 1 per element, depth-bounded, produced bytes |
| Printing an array | `writeValue` | 1 per element; nested arrays count against MaxDepth |
| String leaf copied while stringifying an array | `appendStrBounded` | checked against the produced-bytes bound and read-charged before copying |
| Escaping a string for output | `writeEscaped` | rejected before scanning if its unescaped length cannot fit; written bytes charged as output |
| `select` where-clause | `hSelect` | parsed with `Cut`/`Count`, no allocation per separator |
| Error message of a helper (`fail`) | `fail`, `failWith` | produced bytes; a message holding a whole argument is checked against the bound before it is built |
| Produced bytes: output written, captured sections, helper results | `wrote`, `produced` | 1 per started KiB of the running total; bounded by 8 x MaxOutputBytes |

The ELPS binding adds: parsing, 1 step per started KiB of template on every
call; encoding the context (as `json:dump-bytes`, under `Runtime.MaxAlloc`),
1 step per whole KiB written; decoding it, 1 step per started KiB.

`TestCostModelSites` (in `costguard_test.go`) times each site above with 1 KiB
and 256 KiB strings and fails if the time per step grows more than about 3x
(an uncharged site grows by about 256x), and `TestCostModelGuard` runs the
grammar generator with short and long vocabularies as a coarser net. A new
operation on strings must be charged through these primitives and get a row
here and a case in `costSites`.
