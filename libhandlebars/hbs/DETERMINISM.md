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
pure function of the template, the context and the options.

**Convergence criterion.** Every operation costs at most a bounded constant
times the evaluator's base cost per step, so the time to exhaust the default
MaxSteps (2^25) is bounded: about 2.5 s on the reference machine (4 vCPU,
2.1 GHz; the slowest site measures about 76 ns a step), a tenth of the
peer's 30 s execute timeout. Work that grows with a string's length is
charged by length in units sized from measurement; library calls with a
large fixed cost carry a per-call premium. `TestCostCeiling` runs every
helper and value walk on its slowest known input class and fails above
200 ns a step (CI margin), and pins each case's exact step count.

Units: `hash(n)` = max(1, ceil(n/256)) for hashing, comparing and copying
(under 1 ns a byte); `scan(n)` = max(1, ceil(n/16)) for parsing or scanning
a byte at a time (2-3 ns a byte); `fmt(n)` = max(1, ceil(n/8)) for
formatting a number's n bytes; `KiB(n)` = ceil(n/1024).

| Operation | Where | Charge |
|---|---|---|
| Each AST node evaluated | `eval.go` `at` | 1 |
| Each path segment resolved | `evalPath` | 1, plus the key lookup |
| Each context a lookup tries (mustache climb) | `evalDepthPath` | 1 |
| Each array element a path is mapped over | `evalCtxPath` | 1 |
| Map lookup or insert of an n-byte key: context fields, string-literal paths, hash pair keys, `#each` object keys, `plus`/`minus` keys, helper names | `lookup`, `hashKey`, `findHelper` | hash(n) |
| Array index segment (`strconv.Atoi`) | `evalField` | scan(n) |
| Block parameter scan | `blockParam` | 1 per frame, plus each compare |
| String compare of equal lengths n | `compare` | hash(n); unequal lengths 0 |
| Sorting k keys | `sortKeys` | sum of hash(len) x ceil(log2(k+1)), before sorting |
| Collecting an object's keys | `helperEach`, `appendV` | 1 per key, before collecting |
| `#each` iteration; helper call | `visitBlock`, `helperEach`, `callHelper` | 1 |
| String argument read by a helper | `read` via `convertArg`, `hashStr` | hash(n) |
| Parsing a number from a string (`toFloat`: `gt`, `plus`, `times`..., `round-to-nth`, `prettyp-num-en`) | `parseFloat`, `floatCost` | scan(n), plus 512 + n when the literal is in `ParseFloat`'s slow class (more than 19 significant digits, or a decimal exponent <= -307 or >= 309; about 20 us however short) |
| Parsing an integer from a string (`to-int`, `round-to-nth`'s precision) | `toInt`, `hRoundToNth` | scan(n) |
| Formatting a number (printing, `str`, `%v`, `to-str` in ModeFixed) | `formatted` | fmt(len) |
| `to-str` of a float in ModeCompat (`%f`) | `hToStr` | ceil(len/2) |
| `prettyp-num-en` (go-humanize) | `hPrettyNumEn` | len of the result |
| `format-phone-gb` on a non-empty input (phonenumbers: 40-175 us) | `hFormatPhoneGB` | 2048 |
| `escape-uri-component` | `hEscapeURIComponent` | ceil(n/8), exact escaped length checked before escaping |
| `possessive` (`TrimRight`) | `hPossessive` | scan(n); result checked before it is built |
| Date helpers that discard the parse error | `parseISODate` | input that is not 10 bytes fails without `time.Parse` |
| Date formatters' error text | `dateFormatHelper` | n, and 8n + 128 checked against the produced-bytes bound, before parsing a non-10-byte input |
| String built from an array (`str`) | `measureLeaves`, `copyLeaves` | 1 per element and a read per string leaf, nested arrays against MaxDepth, all before allocating; numbers fmt(len); then produced bytes |
| fmt `%v` text (`prettyp-num-en` errors) | `appendV` | 1 per element, leaves and keys checked and charged before copying, depth-bounded |
| `select` / `in-string-array` element scanned | helpers | hash(len(key)) / 1, plus compares; `where` parsed without allocation |
| `global` read or write | `hGlobal` | hash(len(ns) + len(key)) |
| `round-to-nth` | `hRoundToNth` | precision checked against the produced-bytes bound before formatting |
| Printing an array | `writeValue` | 1 per element; nested arrays count against MaxDepth |
| Escaped output | `writeEscaped` | scan(n) of the escaped length (or of the input when nothing needs escaping), rejected before scanning if it cannot fit |
| Produced bytes: output, captured sections, helper results, error text | `wrote`, `produced`, `fail`, `errorf` | KiB of the running total; bounded by 8 x MaxOutputBytes |

The ELPS binding adds: parsing, 1 step per started KiB of template on every
call; encoding the context (as `json:dump-bytes`, under `Runtime.MaxAlloc`),
1 step per whole KiB written; decoding it, 1 step per started KiB, plus each
number token's `floatCost` (`FromJSONMetered`: tokens charged in document
order, each distinct literal parsed once). An encode that fails is charged
1 step per started KiB of the JSON it got through (`chargeFailedEncode`).

`TestCostModelSites` (`costguard_test.go`) times length-sensitive sites at
1 KiB and 256 KiB and bounds allocation per step; `TestCostModelGuard` does
both over the grammar generator; `TestCostCeiling` (`ceiling_test.go`) is
the per-site ceiling with pinned step counts. A new operation on strings or
a new library call must be charged through these primitives and get a row
here and a case in `ceilingCases`.

## Go API: contexts go through JSON

`libhandlebars.Render(tpl, ctx)` converts a Go `ctx` with `json.Marshal` and
the engine's decoder, as `handlebars:render` always converted ELPS values.
Under raymond a Go context kept its Go types, so this is a difference for Go
callers only (no ELPS-visible change): every number becomes a `float64`
(`{{#if n includeZero=true}}` with `map[string]any{"n": 0}` rendered `yes`
and now renders `no`; `{{to-str n}}` prints `0.000000`), and structs follow
their JSON encoding instead of raymond's field-name rules. The conversion is
one function, `libhandlebars.Render`, so a native conversion can replace it
without touching the engine. `TestRenderGoValueTypes` pins the behaviour.

`TestCostModelSites` (in `costguard_test.go`) times each site above with 1 KiB
and 256 KiB strings and fails if the time per step grows more than about 3x
(an uncharged site grows by about 256x), and `TestCostModelGuard` runs the
grammar generator with short and long vocabularies as a coarser net. A new
operation on strings must be charged through these primitives and get a row
here and a case in `costSites`.
