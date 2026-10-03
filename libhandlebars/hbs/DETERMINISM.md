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
helper and value walk on its slowest known input class and pins each case's
exact step count. Its timing check (`ceilingFails`, used by every timing
test in `hbs` and `libhandlebars`) enforces exactly this: a case slower
than 400 ns a step always fails; one between 200 and 400 ns fails unless it
is within 6 times the plain evaluator measured at that moment, with that
measurement capped at 50 ns a step (so past 300 ns a case fails however
loaded the machine). Other packages' tests running in parallel slow both
alike; a missing charge makes a case hundreds of times slower than the
evaluator.

Costs that only a Go caller building types at run time can trigger
(`reflect.StructOf` types, very long type names) are charged where the walk
looks at them, but reflection's own first use of such a type (building its
pointer and method types, about a microsecond plus its name's length) is
bounded only per type per process. A phylum cannot create Go types.

A render's fixed setup (its renderer, buffers and frames, about 200 ns) is
not charged: an empty template with no context costs one step. It is
bounded per call, so the caller's own per-call cost covers it.

Units: `hash(n)` = max(1, ceil(n/256)) for hashing, comparing and copying
(under 1 ns a byte); `scan(n)` = max(1, ceil(n/16)) for parsing or scanning
a byte at a time (2-3 ns a byte); `fmt(n)` = max(1, ceil(n/8)) for
formatting a number's n bytes; `KiB(n)` = ceil(n/1024).

| Operation | Where | Charge |
|---|---|---|
| Each AST node evaluated | `eval.go` `at` | 1 (helper parameters and hash pairs are collected as each is evaluated and charged, never allocated for all up front) |
| A literal used as a lookup key (`{{1e-320}}`, `{{"x"}}`, `{{true}}`) | `evalExpr` | fmt(len of its canonical form) |
| Each `@../` frame a data path climbs | `evalDataPathExpression` | 1 |
| `mod` | `hMod` | 1 per 8 bits the operands' exponents differ by (math.Mod's reduction loop) |
| Evaluation error text | `errorf`, `dumpLen` | the whole message's length, node dump included (computed without building it, exact: `TestDumpLen`), checked against the produced-bytes bound and charged scan(len) before it is built; then charged as produced |
| A helper's type error naming a Go type (`select`, `global`, a wrong argument type) | `failType`, `typeString` | the type's name (which can hold its struct tags) sized and charged scan(len) before the text is built |
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
| String argument read by a helper | `read` via `convertArg`, `hashStr` | hash(n); an empty string 0 (the call's own step covers it) |
| Parsing a number from a string (`toFloat`: `gt`, `plus`, `times`..., `round-to-nth`, `prettyp-num-en`) | `parseFloat`, `floatCost` | scan(n), plus 512 + n when the literal is in `ParseFloat`'s slow class for its bit size: more than 19 significant digits, or a decimal exponent <= -307 or >= 309 (float64; from 1.8e308 up ParseFloat takes over 20 us, and 1e308 is charged as slow too), or a first significant digit at 10^-37 or below or 10^38 or above (float32: `round-to-nth` in ModeCompat parses with bitSize 32, whose fast paths give up on float32 subnormals, underflow and overflow), judged on the numeric prefix ParseFloat parses before rejecting a malformed suffix; about 20 us however short |
| Parsing an integer from a string (`to-int`, `round-to-nth`'s precision) | `toInt`, `hRoundToNth` | scan(n) |
| Formatting a number (printing, `str`, `%v`, `to-str` in ModeFixed) | `formatted` | fmt(len) |
| `to-str` of a float in ModeCompat (`%f`) | `hToStr` | ceil(len/2) |
| `prettyp-num-en` (go-humanize) | `hPrettyNumEn` | len of the result |
| `format-phone-gb` on a non-empty input (phonenumbers: 40-175 us) | `hFormatPhoneGB` | 2048 |
| `escape-uri-component` | `hEscapeURIComponent` | ceil(n/8), exact escaped length checked before escaping |
| `possessive` (`TrimRight`) | `hPossessive` | scan(n); result checked before it is built |
| Date helpers that discard the parse error | `parseISODate` | input that is not 10 bytes fails without `time.Parse` |
| Date formatters' error text | `dateFormatHelper` | n steps, and the least possible message length checked against the produced-bytes bound, before parsing a non-10-byte input; then the exact length (`parseErrorLen`) checked before the text is built |
| String built from an array (`str`) | `measureLeaves`, `copyLeaves` | 1 per element and a read per string leaf, nested arrays against MaxDepth, all before allocating; numbers fmt(len); then produced bytes |
| fmt `%v` text (`prettyp-num-en` errors) | `appendV` | 1 per element, leaves and keys checked and charged before copying, depth-bounded |
| `select` / `in-string-array` element scanned | helpers | hash(len(key)) / 1, plus compares; `where` parsed without allocation |
| `global` read or write | `hGlobal` | hash(len(ns) + len(key)) |
| `round-to-nth` | `hRoundToNth` | precision checked against the produced-bytes bound and charged fmt(precision) before formatting |
| Printing an array | `writeValue` | 1 per element; nested arrays count against MaxDepth |
| Escaped output | `writeEscaped` | refused before scanning if even the unescaped length cannot fit; scan(len) charged before the scan for escapable bytes; then, if any, scan(n) of the escaped length once it is known to fit |
| Writing unescaped output (content, triple-stash, helper results) | `writeString` | 1 per whole 16 bytes (short writes are covered by their node's step) |
| Produced bytes: output, captured sections, helper results, error text | `wrote`, `produced`, `fail`, `errorf` | KiB of the running total; bounded by 8 x MaxOutputBytes |

The ELPS entry points add:

| Operation | Where | Charge |
|---|---|---|
| Parsing a template (`must-parse`, `render`), cache hit or miss alike | `hbs.ParseCachedMetered`, `hbs.ParseCost` | 6 per lexer token + 1 per started 16 bytes (SHA-256 and plain text) + `floatCost` of each number literal, charged after the depth prescan counts the tokens and before the recursive parse; a cache hit charges what its miss did |
| Encoding an ELPS context to JSON | `chargeEncode`, then `json:dump-bytes` | before encoding, a walk in the encoder's order: 3 per value + 1 per started KiB of estimated JSON (strings at their exact escaped length, after a scan charged 1 per started 64 bytes; a sorted map's entries 1 + log2(n) each for the encoder's collect and sort, before the cap is checked, then each key's bytes, 1 + 1 per 256, times 1 + log2(n), twice (the walk's sort and the encoder's), before any value is walked (after the walk's own sort, which the elps map API offers no way to see before: its cost precedes the charge); a map whose int key spells one of its string keys stops the walk there, so the encoder reports that collision before any native error below it), stopping where the encoder fails (invalid value, NaN or infinity, value depth limit, a value that contains itself, `Runtime.MaxAlloc`), so the encoder then reports its own error. A native is marshalled once, here. One encoding/json walks by reflection is charged first by `goJSONCost`, which walks it exactly as encoding/json does (its encoder per type; its struct fields, each omitted or not 1 + 1 per started 4 embedding hops; pointer and interface hops, map key scans, map entry copies, omitzero tests, json.Number checks and the nesting bound as in the Go API row below; a native whose JSON nests deeper than encoding/json's decoder allows (10,000) is left to the encoder, whose load check reports it; a struct type's field list 12 + its embedding depth a field + names/16 + tags/16 at their escaped length the first time; a field's name at its HTML-escaped length; an omitzero field type's analysis 1 per distinct type and field it reaches, each type once, the first time; a `json.RawMessage`'s bytes 2 per started 16 (checked by the walk, failing as encoding/json does, and by encoding/json; also behind a `json.Marshaler`-typed field and as the native itself; a Marshaler that reaches one by embedding, its MarshalJSON resolved on the value by Go's selector rules (the shallowest embedded field bringing one wins; where several tie, Go's rules have none but reflect.StructOf promotes its first field's, so each is followed and every RawMessage they reach is charged; a RawMessage or an interface declares it, an interface is followed to its value), has its bytes charged likewise before encoding/json runs, whether or not it declares its own MarshalJSON, and encoding/json reports any error; the search, found or not, costs 1 + fields/2 for each struct it looks at (and each embedded struct whose declaration it checks), 3 for each embedded field there bringing a MarshalJSON, and its method lookups 1 + 2 per started 64 bytes of each looked-up type's name (a reflect.StructOf type's name spells out every type it embeds), charged alike whether its per-type table was cached or built, and more than 64 interface hops, 64 embedding levels or 16,384 such fields it fails with "json: Marshaler embedding nests deeper than 64" before encoding/json runs (a value embedding itself through an interface is one)); a `[]byte` 1 per started 32 bytes of its base64; a skipped marshaler's output, called to report an earlier error, 1 per 16 bytes before it is checked; a `,string` field at its doubly escaped length; its sorted map keys) and fails where and as it fails, with its text charged by length (a cycle is reported without running it, a MarshalJSON or MarshalText passed over is called to report its error first, two map keys that encode alike are an error, and of several keys whose MarshalText fails, the least error text is reported (encoding/json reports the first in Go's map order); a shared subtree is charged again from a memo, not walked again, and fails the level bound where walking it again would, as json.Marshal has no memo); and if a lower bound of the JSON written so far (its leaves' exact or least lengths, a container's opener on entry and its closer and colons only after its contents, as libjson checks its cap before each value against what it has written) passes `Runtime.MaxAlloc`, json:dump-bytes's allocation error is returned without marshalling it, unless a marshaler encoding/json would call first fails (its error then, as json.Marshal reports it); where a native fails to encode, the encoder itself runs up to a stand-in that fails there, so an allocation failure before it is reported first. Where the walk stops at the cap before a value, the encoder itself runs up to that point (charged by the walk, which follows its order) and reports what it meets first. A `json.Marshaler`'s own work is the embedder's. The bytes are charged by `JSONCost` (the encoder decodes them to check they load), before the allocation cap is checked against them, and handed to the encoder as a `json.RawMessage`, so the output is unchanged; the copy of the context that carries them visits only the containers the charged walk marked as leading to a native, charging each copied container's cells (1 per 16). Then `json:dump-bytes`'s own 1 per whole KiB written |
| Decoding the context | `hbs.FromJSONMetered` (and, for `handlebars:render`, `lisp.ChargeStartedKiB` first) | `handlebars:render` and `render-fixed` first charge 1 per started KiB of the JSON; then, before validating: 1 per started 16 bytes and 1 per started 4 whitespace bytes (every pass reads them); then 2 per `{` or `[`, 1 per `,`, `:`, `null`, `true`, `false`, 1 + ceil(len/8) per string, 8 + `floatCost` per number (each distinct literal parsed once); invalid or non-object JSON 1 per started 8 bytes |
| Retyping ELPS ints (`render-fixed` only) | `intTyper` | 1 per value walked |
| Reading a Go value (Go API) | `goreflect.go` | each pointer or interface followed 1 (more than MaxDepth in a row is a limit error); a struct type's plan, the first time a render uses the type (resolved breadth-first with `FieldByName`'s rules, each embedded type once): 12 + its embedding depth per field (embedded structs' included) + 1 per started 16 bytes of its name and of its tags, charged field by field before it is built (cached or not); a struct lookup hash(len(name)) + fmt-unit(len(name)) for `strings.Title` + 1 per started 8 embedding hops to a promoted field; a method check hash(len(name)); a map key hash(len); a slice index scan(len); boxing a value (`Interface`, and `MapIndex`'s copy) 1 per started 128 bytes of its size when over 8; printing, `#each` and array blocks 1 per element, depth-bounded (`#each` over a map whose key type cannot hold a string reads no keys); `%v` (prettyp-num-en's error) 1 per node, hash(len) per string, (1 + log2 n) per key for fmt's key sort, times the key's comparison cost (1 + bytes/256 for a string; 1 + elements/4 for an array of scalars, element by element otherwise; a struct field by field; 4 for a float, 4 + its value's for an interface) and 1 per started 128 bytes of each entry's key and value copies, before fmt runs (a key's NaN test and comparison together visit at most what MaxSteps leaves; a map of two or more keys with one nested deeper than MaxDepth is a depth error before fmt runs, as fmt's sort compares keys all the way down, past their String and Error methods); a numeric scalar's `%v` is fmt's, uncharged beyond its node (about 100 ns); raymond's `SafeString` prints as its text, unescaped when it is the value itself, as raymond did (the harness renders with its frozen copy, `raymondref`, whose own SafeString it matches; real raymond's type is matched by package path, which the harness cannot exercise) |
| Converting a Go context through JSON (Go API, `WithJSONContext`) | `goJSONCost`, `goBudget` | before `json.Marshal`, encoding/json's own walk (as for an ELPS native): 3 per value + 1 per started KiB of estimated JSON, strings scanned 1 per started 64 bytes, n(1 + log2 n) per map plus its keys' bytes/256 × log2 n for the sort, each map key scanned 1 + 1 per whole 16 bytes (a map's key charges summed and applied at once, so a budget failure does not depend on Go's map order), each map entry copied 1 per started 32 bytes of key and value, each pointer or interface hop 16 (charged before it is followed, so a walk that fails below it has paid), an omitzero field's zero test (twice: the walk's and Marshal's) 2 per started 256 bytes of its size when it is plain memory, else 1 per 8 of the values IsZero visits in it (floats, strings and padded structs are tested element by element), a json.Number validated 1 per started 16 bytes (its invalid-literal text 1 per 4 bytes before it is built), each struct field visited (omitted or not) 1 + 1 per started 4 embedding hops, a struct type's field list 12 + its embedding depth a field + name bytes/16 + tag bytes/16 (at their escaped length) the first time the walk meets it, a field's name at its HTML-escaped length, an omitzero field type's analysis 1 per distinct type and field it reaches (each type once) the first time, a `json.RawMessage`'s bytes 2 per started 16 (checked by the walk as encoding/json checks them, also behind a `json.Marshaler`-typed field, or reached by embedding, searched and bounded as for a native), a `[]byte` 1 per started 32 bytes of its base64, a skipped marshaler's output 1 per 16 bytes when it is called to report an earlier error, a `,string` field at its doubly escaped length; its errors returned without running it (a failing MarshalJSON or MarshalText before the failure point is called to report it first; a map with two keys that encode alike is an error); then the decode as `FromJSONMetered`; all against the render's MaxSteps; container nesting past 1024 is an error (pointer hops do not count; a shared subtree's memo entry is keyed on the container depth it starts at, so the bound does not depend on field order), and any value nesting past 50,000 walk levels (a pointer, an interface and a container each count one, so about 25,000 nested `[]any`), a limit error well before the walk's stack (about 1.3 KB a level) could grow near Go's 1 GB abort; levels past 64 cost 8 each for the stack growth |

`TestBuiltinCostCeiling` (`libhandlebars/ceiling_test.go`) runs these end to
end through `handlebars:must-parse` and `handlebars:render` on tag-dense 1
MiB templates (miss and hit), structural JSON, distinct number literals and
an ELPS context sharing one value a million times, with the same
ceiling check; they measure at most about 100 ns a step.

`TestCostModelSites` (`costguard_test.go`) times length-sensitive sites at
1 KiB and 256 KiB and bounds allocation per step; `TestCostModelGuard` does
both over the grammar generator; `TestCostCeiling` (`ceiling_test.go`) is
the per-site ceiling with pinned step counts. A new operation on strings or
a new library call must be charged through these primitives and get a row
here and a case in `ceilingCases`.

`TestCostModelSites` (in `costguard_test.go`) times each site above with 1 KiB
and 256 KiB strings and fails if the time per step grows more than about 3x
(an uncharged site grows by about 256x), and `TestCostModelGuard` runs the
grammar generator with short and long vocabularies as a coarser net. A new
operation on strings must be charged through these primitives and get a row
here and a case in `costSites`.

## Contexts: JSON, ELPS and Go values

A context's number types are visible to templates: compat mode treats only an
int literal 0 as zero for `includeZero`, and `to-str` prints a float64 with
`%f` (`3.000000`) but an int with `%d`. Each entry point fixes how numbers
arrive, so the output is a function of the value and the entry point:

- `handlebars:render` (ModeCompat) serializes the ELPS value with libjson and
  decodes it with `FromJSON`, as svc always did: every number is a float64,
  byte for byte what raymond rendered.
- `handlebars:render-fixed` (ModeFixed) takes the same route, then walks the
  ELPS value alongside the decoded one and puts back each ELPS int as a Go
  int (a step per value). JSON cannot tell 3 from 3.0 (libjson writes both
  as `3`), so the ELPS value is the only record of which numbers were ints.
  The walk only retypes numbers: structure, errors and limits are the JSON
  route's. A bytes (JSON text) context has no ELPS ints and stays float64.
- `libhandlebars.Render` / `RenderWith` (Go API, ModeCompat) pass the Go
  value itself, and the engine reads it lazily by reflection, as raymond
  did (`goreflect.go`, ported from raymond's evalField, indirect,
  isTrueValue, strValue and eachHelper): Go's number types and named types
  reach helpers as themselves, structs and `interface{}`-keyed maps are
  looked up by raymond's rules, and only what the template touches is read
  (`TestGoContextDifferential` and the blocker tests compare against the
  frozen raymond with Go-typed contexts). Every read is charged against the
  render's own MaxSteps and bounded by MaxDepth. Where raymond called Go
  code (a method or func a lookup reaches) the render fails with an error
  naming it, and where raymond panicked or looped forever (an unexported
  value, a nil embedded pointer, printing a channel, a pointer cycle the
  template touches) it fails with an error. `WithJSONContext()`, or
  `SVC_HANDLEBARS_JSON_GO_CONTEXT=true` read once per process (exactly
  `true`; any other value is logged and ignored), selects the JSON route
  instead. Output for a Go value is deterministic, including where raymond's
  was not: `%v` in `prettyp-num-en`'s error text, where fmt prints a process
  address (a non-nil chan, func or unsafe pointer, or a pointer it does not
  follow: one nested anywhere, as fmt sees a value inside an engine array
  or object nested, or a top-level pointer to a scalar), prints the value
  as its type in parentheses instead, e.g. `(chan int)` or `[(*[]int)]`
  (an intended difference: raymond's text held the address, which differs
  between processes; the hbdiff corpus cannot hold a Go value, so it has no
  allowed-diffs entry). That `%v` is also the one place Go code runs: fmt calls a value's String
  or Error method, as raymond's did. It is sized first by an order-free
  walk (each node up to MaxDepth, fmt's map-key sort charged), so its
  charge and its error do not depend on Go's map order. A Go value inside
  an engine array prints by raymond's element rule (a pointer, chan or func
  there is UNPRINTABLE). Printing a Go chan fails with its type in the
  error, where raymond's panic text held an address. A map with more than
  one NaN key has no deterministic `%v` (fmt orders those keys by Go's map
  order), so printing one is an error.

## encoding/json fidelity

`libhandlebars/jsongo.go` reproduces encoding/json's traversal and error
texts, as shipped with the Go toolchain `go.mod` names (go 1.26); it
departs from encoding/json where encoding/json's result depends on Go's
map order (the key-error choice and duplicate keys above), and at one
bound: the search for a RawMessage a Marshaler reaches by embedding fails
with "json: Marshaler embedding nests deeper than 64" past more than 64
interface hops, 64 embedding levels or 16,384 embedded MarshalJSON-bearing
fields, so that no RawMessage's bytes go uncharged. A few values
encoding/json encodes are refused there: a node embedding itself through
an interface whose types declare their own MarshalJSON, or a chain of 65
such wrappers. (Where no type on the path declares one, encoding/json
itself recurses until the stack overflows.) The hbdiff corpus cannot hold
a Go Marshaler, so this has no allowed-diffs entry.
`TestEncodingJSONTexts` pins those texts and fails if a Go release changes
them, and `TestGoJSONCostMatchesMarshal` compares the walk with
`json.Marshal` over many shapes. Building with `GOEXPERIMENT=jsonv2`
replaces encoding/json's implementation and is not supported.
