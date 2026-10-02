# Design: raymond (Handlebars) replacement

Status: approved for build (option E). Refs luthersystems/svc#106.

## 0. Decisions and review (supersedes section 1 where they differ)

An independent adversarial review checked this design against the code and
against production phylum templates (described here only by shape). After
the review, the owner chose to build the native engine (option E) now.

| Topic | Decision |
|---|---|
| Option | **E: native engine**, `libhandlebars/hbs`. The frozen fork stays as a test-only reference (`libhandlebars/internal/raymondref`). |
| Bug fixes | **Opt-in fixed mode.** `handlebars:render` keeps byte-identical output (`ModeCompat`), including helper bugs. `ModeFixed` fixes them, and a phylum must ask for it. The one compat exception: `plus`/`minus` use sorted hash-key order, because today's output is not deterministic. |
| Harness | **Both:** a checked-in corpus (literal cases, shape skeletons of production templates with no production text, grammar fuzzer), and a private mode that reads a phylum folder and context JSON from outside the repo. |
| `libname` / `version` | **New honest strings**, tied to the engine build. Production writes them into stored document metadata, so the upgrade note must say so. |
| Rollout | substrate runs as external chaincode, so "coordinated upgrade" is a runbook: roll each peer while it does not endorse, and check `(handlebars:version)` on every peer before it endorses again. No two-engine switch for one channel. |

Review findings that change the design:

| # | Finding | Change |
|---|---|---|
| R1 | Some template inputs abort the process instead of failing the call. Templates are user uploads in production, checked by `must-parse` inside a transaction. (Details: private substrate issue.) | Size and nesting caps checked **before** any recursion. Blocker for release. |
| R2 | A failed parse leaks a goroutine (raymond's lexer goroutine is never drained). | Synchronous lexer. |
| R3 | Templates stored on the ledger are validated once and rendered forever. | Rollout gate: an operator-run checker compares old and new engines over each channel's stored templates and reports counts only. |
| R4 | Template number literals are Go `int`; context numbers are `float64`. Helpers behave differently for each (`{{to-str 3}}` gives `3`; `{{to-str n}}` gives `3.000000`). Section 3.2's `to-str` row was wrong. | The engine's value model keeps `int` and `float64` apart. |
| R5 | More compat traps: a nil argument skips the helper and renders `""`; mustache-style lookup climbs to the parent context inside `#each`; nil hash values are dropped; `round-to-nth` parses as 32-bit float; `{{@this}}` passes `must-parse` and then panics at render. | All are harness cases. `{{@this}}` becomes a render error (an allowed difference). |
| R6 | D1 can be reached by production templates, but 2-decimal rounding hides it today. | Keep the fix; severity medium. |
| R7 | More quadratic sites: block sections, `select`, array printing. | One output builder for the whole render. |
| R8 | Toolchain or dependency bumps (`QueryEscape`, go-humanize, phonenumbers, `FormatFloat`, `AddDate`) can change output without anyone noticing. | Reference golden outputs are committed and re-checked on every bump. |

Revised estimate: about 13-18 agent-days of build (harness first, then parser,
evaluator and helpers in parallel), about 1.5-2 calendar weeks, plus about one
engineer-week of human review. Not included: exporting stored templates from
each channel, the rollout runbook, and product sign-off on each allowed
difference.

## 1. Summary and recommendation (original, before the review)

| Option | Cost | Risk to tx results | Benefit | Verdict |
|---|---|---|---|---|
| A. Vendor the fork into svc, archive the fork | 0.5 day | None (same code, same bytes) | Closes #106. Fork repo can be archived. | **Do now** |
| B. Switch to upstream `aymerick/raymond` | 0.5 day | **High.** Output changes. Upstream is archived. | None | **Reject** |
| C. Use ELPS's own templates | n/a | n/a | n/a | **Not possible.** ELPS has no text-template engine. |
| D. Harden the vendored copy (determinism, step charging, linear output) | 1-2 weeks | Low, controlled. Behind a coordinated-upgrade switch. | Fixes the 3 real defects found below. | **Do next** |
| E. Native-Go rewrite over ELPS values | 5-8 weeks, plus a differential gate | Medium. Byte compatibility must be proven. | Speed, no JSON round-trip, no reflection, clean budget hooks. | **Defer.** Do only if data shows render cost matters. |

Recommendation: **A now, then D. Keep E as a designed, unfunded option.**
Confidence: about 80%.

The push-back: the ticket's question is "vendor or upstream". The answer is
"vendor". But the research found three defects that matter more than where
the code lives. All three are in svc's helpers and raymond's evaluator, not in
the fork's patches. A rewrite is not necessary to fix them. Section 7 gives the
rewrite design so the decision is informed, not so it is approved.

The three defects (all reproduced, see section 4):

1. **Non-deterministic output.** `plus` and `minus` add floats in Go map order.
   The same render gives different bytes on different runs:
   `{{plus a=0.1 b=0.2 c=0.3}}` gave `0.6000000000000001` 1773 times and
   `0.6` 227 times in 2000 runs. Endorsers can disagree. The transaction then
   fails its endorsement policy.
2. **No ELPS step charge.** `handlebars:render` costs the same steps for 1
   byte or 4 MB of output. A template can exceed the step budget
   (`SUBSTRATE_ELPS_MAX_STEPS`) without a charge.
3. **Quadratic output build.** raymond builds output with `result +=`. A nested
   `#each` that writes 9 MB took 30 s. That is the peer's default
   `chaincode.executetimeout`.

## 2. Who uses raymond

Scope of the search: substrate, svc, elps, hyphae, and every other
luthersystems repository that `git clone` could reach (58 listed; 28 public
cloned, hyphae added; 29 private repos refused the clone). Search terms:
`raymond`, `handlebars` (case-insensitive).

| Repo | Uses raymond? | How |
|---|---|---|
| svc `libhandlebars/` | **Yes, direct** | The ELPS package `handlebars` and 31 custom helpers. The only real consumer. |
| substrate `internal/substrate/shiro/libcc/handlebars.go` | **Yes, direct** | Loads svc's package into every production env (`libcc.go`). Calls `raymond.Parse` directly for a cached `must-parse` (substrate#548). |
| substrate `scripts/perfexp` | Indirect | Builds a shim of svc's `libhandlebars` for legacy refs. |
| libmxf | go.sum only | Transitive through svc. No code. |
| hyphae (private, cloned) | No | Former home of `libhandlebars`; moved to svc. |
| docs | No code | `common-operation-script/substrate/templates.md` copies the svc README. |
| elps | No | `docs/templates.md` is about immutable **VM** templates (`lisp.NewTemplate`), not text templates. Only `format-string` (`{}` placeholders) exists. |
| sandbox, sandbox-template, docs (connectorhub) | No | Match is the forename "Raymond" in test data. False positive. |
| buildenv | No | CVE backlog lists the JS/Java `handlebars` packages, unrelated. |
| 29 private repos | **No access** | lutherauth, common-infrastructure, ui-core, enterprise, luther, builtins, connectorhub, onboard, insideout, frontpage, reliable, license and others. |

**Gap:** production phylum templates live in phylum repositories this search
could not reach. The feature census in section 3.3 is from svc's tests and
docs. Phase 0 of the plan collects the real corpus.

### 2.1 How ELPS calls it

```
phylum (ELPS)                     svc libhandlebars (Go)              raymond
(handlebars:render tpl ctx) ---> builtInRender
                                   ctx -> libjson Dump -> []byte
                                   json.Unmarshal -> map[string]any
                                   raymond.Parse(tpl)  (every call)  -> AST
                                   addHelpers(tpl)     (every call)
                                   tpl.Exec(map)                     -> string (reflection)
                                 <- lisp.String(result)
(handlebars:must-parse tpl)  ---> substrate builtinCachedMustParse -> raymond.Parse (cached verdict)
(handlebars:version)         ---> "v1.1.1-0.20200710185833-e77462cef10d"
(handlebars:libname)         ---> "raymond"
```

Facts that any replacement must keep:

| Topic | Current behaviour |
|---|---|
| Context type | ELPS value is JSON-encoded, then decoded to `map[string]interface{}`. A `bytes` value is used as JSON directly. A top-level list or string fails with a plain error (`error while unmarshaling: ...`), not a condition. |
| Numbers | All numbers become `float64`. Ints above 2^53 lose precision: `9007199254740993` renders `9007199254740992`. Printed with `strconv.FormatFloat(f, 'f', -1, 64)`: `1e21` renders `1000000000000000000000`. |
| `()` / `false` | `()` becomes JSON `null`, renders `""`, falsy. `false` renders `false`. |
| Parse | Every `render` parses again. No cache. 31 helpers registered per call. |
| Errors | Parse error: condition `handlebars-parse`, message `error parsing template: <raymond text>`. Render error: `handlebars-render`, message `error while rendering template: <raymond text>`. The raymond text includes AST positions (`Current node: ...`). |
| Go panics | Helpers `panic(fmt.Errorf(...))`; raymond turns `error` panics into render errors. Runtime panics re-panic; the ELPS evaluator's `recover` turns them into Lisp errors. |
| Steps | Constant per call. Nothing scales with template, context or output size. |

### 2.2 Raymond API surface svc uses

`raymond.Parse`, `Template.RegisterHelper`, `Template.Exec`, `raymond.Options`
(`Hash`, `HashProp`, `HashStr`, `FnWith`), `raymond.IsTrue`, `raymond.Str`.
No partials are registered. No global helpers are added. No `SafeString` is
returned by svc helpers.

## 3. Fork vs upstream

Fork base: upstream `b565731` (2018-03-22, v2.0.2 + 1). Pinned fork commit:
`e77462c` (2020-07-10). Upstream has had no code change since 2018 and has been
**archived** since 2025-06 (README: "Project is archived").
`mailgun/raymond` is a maintained-looking fork (last commit 2022-11); it also
has none of our patches (it still iterates maps unsorted and keeps `lookup`).

### 3.1 Every patch

| # | Patch (file) | Effect | Upstream equivalent? |
|---|---|---|---|
| 1 | `#each` over a map sorts string keys (`helper.go`) | Deterministic order. Non-string keys are dropped. | **No.** Upstream order is Go map order (random). |
| 2 | Printing a map/struct gives `UNPRINTABLE` (`string.go`) | No `fmt` of Go maps in output. | **No.** Upstream prints `fmt.Sprintf("%s")`. |
| 3 | `log` and `lookup` helpers not registered (`helper.go`) | `{{log}}` and `{{lookup}}` render `""`. | **No.** |
| 4 | Strict helper arity; `*Options` only as last param (`eval.go`, INFR-103) | Fewer accidental matches. | **No.** |
| 5 | Module path `github.com/luthersystems/raymond`, `go.mod` | Build only. | n/a |
| 6 | Mustache spec vendored instead of a submodule | Tests only. | n/a |
| 7 | LICENSE: adds "Copyright (c) 2020 Luther Systems" | Legal. Keep MIT text and both lines. | n/a |
| 8 | Test expectations updated for 1-4 | Tests only. | n/a |

Conclusion: **switching to upstream changes output** for patches 1-4. Patch 1
alone makes `#each` over a map non-deterministic, which can fail endorsement.
Option B is rejected.

### 3.2 Behaviours a compatible engine must copy

Probed against the pinned code (Go probe, not committed). Some are bugs; a
compatible engine must keep them until a deliberate, coordinated change.

| Input | Output | Note |
|---|---|---|
| `{{x}}` with `<a href='x'>&"`=` | `&lt;a href=&apos;x&apos;&gt;&amp;&quot;`=` | Escapes `& < > " '` only. `'` is `&apos;` (handlebars.js: `&#x27;`, and also escapes `` ` `` and `=`). |
| `{{m}}` (map) | `UNPRINTABLE` | Patch 2. |
| `{{arr}}` with `["a",1,true]` | `a1true` | Arrays concatenate. |
| `{{#each m}}{{@key}}...` keys `b,a,B` | `B=3;a=2;b=1;` | Byte order sort. |
| `{{lookup m "a"}}`, `{{log "x"}}` | `""` | Patch 3. |
| `{{nohelper 1}}` | `""` | Missing helper with params is silent (handlebars.js errors). |
| `{{> p}}` | render error `Partial not found: p` | No partials exist. |
| `{{#*inline "p"}}` | parse error | Handlebars 3 grammar. |
| `{{{{raw}}}}{{x}}{{{{/raw}}}}` | `""` | Raw block calls a helper named `raw`; none exists. |
| `{{to-str 3}}` (literal int) and `{{to-str n}}` (context 3) | `3` and `3.000000` | Literal ints vs context float64 (`%f`). `int8..int32`, `float32` cases return `""`. |
| `{{mod 7 "x"}}` | `NaN` | Guard is `!ok1 && !ok2` (should be `||`). |
| `{{to-int "3.5"}}` | `0` | |
| `{{date-add-months "2020-01-31" 1}}` | `2020-03-02` | Go `AddDate` normalisation. |
| `{{prettyp-num-en 1234567.885}}` | `1,234,567.88` | go-humanize rounding. |
| `{{len "abc"}}` | render error | Helper takes `[]interface{}` only. |
| `{{global "n" key=1}}` | render error `global: invalid key type: int` | |
| Standalone `{{#if}}` lines | removed with their newline | Mustache standalone rules. |
| `{{~ x ~}}` | whitespace control | |
| `{{x.[0]}}`, `{{x.length}}` | `q`, `""` | No `.length`. |

### 3.3 Feature census (known so far)

From svc tests, svc README and the published docs. Phase 0 replaces this with
counts from the real corpus.

| Feature | Known in use | Notes |
|---|---|---|
| `{{path}}`, `{{a.b.c}}`, `{{../x}}`, `{{this}}`, `@index`, `@key`, `@first`, `@last` | Yes | Core. |
| `{{{raw}}}` triple-stash | Yes | README tells authors to use it for `possessive`. |
| `#if` / `else` / `#unless` / `#each` / `#with` | Yes | |
| Subexpressions `(gt a b)` | Yes | README examples. |
| Hash args `key=val` | Yes | `and`, `or`, `plus`, `minus`, `select`, `global`, `in-string-array`. |
| `equal` (raymond built-in block helper) | Unknown | Registered upstream helper. |
| Custom helpers (31) | Yes | `eq len not and or gt gte lt lte times div mod plus minus select global round-to-nth in-string-array prettyp-num-en possessive date-beautify date-DDMMYY-slash date-DDMMYYYY-slash date-DDMMYYYY date-diff-month is-after date-add-months to-int to-str format-phone-gb escape-uri-component`. |
| Partials, inline partials, decorators | No | Not possible today. |
| `lookup`, `log` | No (disabled) | |
| Helpers registered from ELPS | **No** | Not possible today. All helpers are Go. |
| Raw blocks `{{{{ }}}}` | No (broken) | |

## 4. Defects found

| # | Defect | Severity | Reproduction | Where |
|---|---|---|---|---|
| D1 | `plus`/`minus` sum floats in Go map order | **High (consensus)** | `{{plus a=0.1 b=0.2 c=0.3}}`: 2 distinct outputs in 2000 runs. `{{plus a=x b=y c=z}}` with `1e16, 1, -1e16`: `0` or `1`. | svc `addHelpers` |
| D2 | `render` charges no steps for parse, eval or output | **High (budget)** | 4,000,002-byte output: 1 step for the `render` call (the other steps built the input vector). | svc `builtInRender` |
| D3 | Quadratic string concatenation | Medium (liveness) | `{{#each a}}{{#each ../a}}x{{/each}}{{/each}}`, 3000 items: 9 MB, 30.7 s. | raymond `eachHelper`, `evalProgram` |
| D4 | No nesting cap | Low | 200,000 nested `{{#if}}` parse in 2.8 s, Go recursion. | raymond parser |
| D5 | `format-phone-gb` output depends on the `phonenumbers` metadata version | Medium (upgrade) | A dependency bump can change output for some numbers. That is a consensus-visible change. | svc helper |
| D6 | `to-str`, `mod`, `toFloat(float32)` bugs | Low | Section 3.2. | svc helpers |
| D7 | `global` helper map is per parse. Safe today only because `render` parses each call. | Low (latent) | A parse cache that shares a `*Template` across renders makes `global` leak state between renders and VMs. | svc helper |

D7 matters for any optimisation: **do not cache a raymond `*Template` with
helpers attached.** Cache the AST only, and give each render its own helper
state.

Not a defect: `and`/`or` iterate the hash in map order, but boolean AND/OR is
order-independent. `#each` over context maps is sorted (patch 1). Dates parse
with `time.Parse` (UTC); no clock, locale or randomness is read.

## 5. Requirements for any change

| ID | Requirement | Test |
|---|---|---|
| R1 | **Byte-identical output** for every template and context the corpus has, and for every row of section 3.2, unless a change is listed in an upgrade note. | Differential harness (section 6). |
| R2 | **Same error behaviour**: same condition (`handlebars-parse` / `handlebars-render` / plain error), same message text where phyla can see it. | Harness compares type, condition and message. |
| R3 | **Determinism**: no map-order dependence, no clock, no locale, no randomness, no process-global state. Same input gives the same bytes on every endorser, cold or template VM. | Run each case 100 times; run in cold and forked VMs; `-race`. |
| R4 | **Budget**: charge ELPS steps that scale with work: parse size, nodes evaluated, iterations, output bytes. Charges are a pure function of (template, context). | Step-count golden tests; cold/template parity test like `TestStepCountColdTemplateParity`. |
| R5 | **Size caps**: template size, nesting depth, output size. Cap breaches are deterministic errors. | Boundary tests at cap and cap+1. |
| R6 | **Linear time** in output size. | Benchmark: 9 MB case under 1 s. |
| R7 | No unbounded recursion. Partials stay off; if added, they need a depth cap and no self-cycle. | Fuzz with depth tests. |
| R8 | Template-safe natives: nothing mutable shared between VMs (substrate#448, `nativepayload`). | `make vet-natives`; template publication tests. |

Rule from substrate: a change to output, error or step count is
**consensus-visible**. It ships as a coordinated upgrade of every peer on a
channel, with an upgrade note. Fixing D1 changes output (it picks one of
today's possible outputs), and D2 changes step counts. Both qualify.

## 6. Compatibility gate: differential harness

One harness serves options A, D and E.

```
svc/libhandlebars/internal/hbdiff/
  corpus/            *.hbs + *.json pairs (from tests, docs, section 3.2, and the Phase 0 corpus)
  diff_test.go       for each pair: run REFERENCE and CANDIDATE, compare
  fuzz_test.go       go test -fuzz: template grammar fuzzer + JSON context fuzzer
  census/            AST walker: counts features and helpers per template
```

- **Reference:** the pinned fork code, frozen in a test-only package
  (`internal/raymondref`). It never changes.
- **Candidate:** the engine under test.
- **Compare:** output bytes; error yes/no; condition; message (exact, or with a
  documented normaliser for AST position text); ELPS steps (golden, per R4).
- **Fuzzing:** a grammar-aware generator (paths, blocks, `else`, `~`,
  subexpressions, hash args, every helper) plus a JSON value generator (deep
  maps, arrays, floats near 2^53, unicode, escapable characters). Seed with the
  corpus. Run in CI for a fixed time (e.g. 2 min) and nightly for longer.
- **Known differences** (D1, D6 if fixed) are an explicit allowlist with an
  issue number, like substrate's `scripts/api-breaks.txt`.
- **Production corpus:** templates are extracted from phylum source by a
  script that finds every `handlebars:render` / `must-parse` string literal.
  Contexts come from the phylum's own tests. The corpus stays in each phylum
  repo; the harness takes a path, so no production template is copied into svc.

## 7. Native-Go rewrite design (option E)

Only if Phase 3 data justifies it. Goal: same language and bytes, no JSON
round-trip, no reflection, budget-aware.

### 7.1 Package layout (in svc)

```
svc/libhandlebars/
  libhandlebars.go        ELPS package (unchanged API: render, must-parse, version, libname)
  engine/
    lex.go parse.go       Handlebars 3 grammar, same as raymond's (port, keep error text)
    ast.go                immutable AST; positions kept for error text
    whitespace.go         standalone and ~ rules (port of raymond parser/whitespace.go)
    compile.go            AST -> flat instruction list (closure tree or bytecode)
    eval.go               evaluator over a Value interface
    value.go              Value interface + ELPS adapter + JSON adapter (for the harness)
    escape.go             exact raymond escape set
    helpers_builtin.go    if unless each with equal (lookup, log off)
    helpers_luther.go     the 31 svc helpers, bug-compatible
    budget.go             Meter interface
    cache.go              parse cache, keyed by SHA-256 of template text
  internal/raymondref/    frozen reference for the harness (test only)
  internal/hbdiff/        section 6
```

### 7.2 Parser and cache

- Parse once per distinct template text. Key: SHA-256 of the bytes. Value:
  immutable compiled program, or the parse error verdict (as substrate's
  `must-parse` cache does today). Bounded by bytes, process-wide, read-mostly.
- The compiled program holds no helper state, no context and no mutable data,
  so VMs can share it (R8). Per-render state (`global` map, data frames) lives
  in a per-call struct.
- Parse cost is charged to ELPS steps on **every** call, hit or miss, from the
  template length, so a cache hit and a miss cost the same steps (the same
  rule as substrate's storage builtins).

### 7.3 Evaluator over ELPS values

- `Value` interface: `Kind()`, `Str()`, `Float()`, `Truthy()`, `Len()`,
  `Index(i)`, `Field(name)`, `Keys()` (sorted). The ELPS adapter wraps
  `*lisp.LVal` directly: sorted-map, vector/list, string, int, float, bool,
  `()`, bytes.
- **Compatibility trap:** today every number is a JSON `float64`. The adapter
  must convert ELPS ints to float64 semantics (precision loss above 2^53,
  `FormatFloat('f', -1)`), and must mirror the JSON encoder for every type
  (symbols, bytes, nested `()`, empty map vs empty list). The harness tests
  this per type. A later, separately flagged change can render big ints
  exactly.
- Output goes to one `strings.Builder` with a byte cap (R5, R6).
- Helpers take `(args []Value, hash Hash, opts *Options) (Value, error)`.
  `Hash` iterates in sorted key order. No reflection, no `panic` for control
  flow.

### 7.4 Helper registry

- One static, immutable table for the 36 built-in helpers, built at init.
- Helpers from ELPS (a phylum-defined helper) are **out of scope for v1**.
  They would add a Lisp call per helper invocation and re-entrancy into the
  VM. Add later only with a concrete need.

### 7.5 Budget hooks

```go
type Meter interface {
    Charge(steps int64) error // returns the ELPS budget condition when exhausted
}
```

| Work | Charge (proposal) |
|---|---|
| Parse (hit or miss) | 1 step per started KiB of template (`lisp.ChargeStartedKiB`) |
| Each node evaluated | 1 step |
| Each `#each` iteration | 1 step |
| Output | 1 step per started KiB written (charged as it grows, so it stops early) |
| Helper | 1 step, plus size-based cost for string helpers |

The ELPS builtin passes a `Meter` bound to `env`. Exhaustion raises
`step-budget-exceeded` from inside the render. The same `Meter` works for
option D (hardened raymond) at node and output granularity.

### 7.6 Caps

| Cap | Default | Error |
|---|---|---|
| Template size | 1 MiB | `handlebars-parse` |
| Nesting depth (blocks + subexpressions) | 256 | `handlebars-parse` |
| Output size | 16 MiB | `handlebars-render` |
| Context depth | ELPS's own value-depth limit | as today |

Caps are code constants, not configuration: endorsers must agree.

### 7.7 Error model

Keep the two conditions and the message prefixes. Message bodies come from
the ported parser and evaluator so that text matches raymond. Where an exact
match is costly (AST dumps in `Current node:`), list the difference in the
harness allowlist and the upgrade note.

### 7.8 Performance targets and benchmark plan

| Benchmark | Template | Target vs raymond path |
|---|---|---|
| `Small` | 1 line, 3 paths | 5x faster (no per-call parse, no JSON) |
| `Letter` | ~5 KiB, `#if`/`#each`, date and number helpers | 3x faster, 3x fewer allocs |
| `Table` | `#each` over 1000 rows, 10 fields | 3x faster |
| `NestedEach` | 3000 x 3000 | linear: under 1 s (today 30 s) |
| `MustParseHit` | cache hit | no regression vs substrate's cache |

Run with `go test -bench -count=10`, compare with `benchstat`, and gate with
the shared `benchgate` used by substrate. Inputs are synthetic, shaped like
the Phase 0 census, never copied production templates.

### 7.9 Estimate

5-8 engineer-weeks: port lexer/parser/whitespace (1.5), evaluator and
adapter (1.5), helpers bug-compatible (1), budget and caps (0.5), harness and
fuzzing (1-2), review and coordinated rollout (1).

## 8. Migration plan

| Phase | Work | Output change? | Ships as |
|---|---|---|---|
| 0 | Vendor the fork into `svc/libhandlebars/internal/raymond` (MIT LICENSE kept, NOTICE lists patches 1-4). Add `libhandlebars.CheckParse(tpl) error` so substrate stops importing raymond. Build the harness with the frozen reference. Run the corpus census on each phylum repo. | **None** | svc patch release; substrate bumps svc and drops `luthersystems/raymond`. Archive the fork. Closes #106. |
| 1 | Linear output (D3): `strings.Builder` in the vendored evaluator. Move substrate's must-parse cache into svc. | None (harness proves it) | svc minor; substrate normal release |
| 2 | Determinism (D1): `plus`/`minus` sum in sorted key order. Pin `phonenumbers` and gate its bumps with the harness (D5). | **Yes** (D1 picks one result) | Coordinated chaincode upgrade, upgrade note |
| 3 | Budget (D2) and caps (D4): `Meter` in raymond's evaluator. | **Steps yes; output no** | Coordinated upgrade, together with Phase 2. Measure with `SUBSTRATE_ELPS_MAX_STEPS=measure` first. |
| 4 (optional) | Rewrite (option E) behind the harness. Decide from Phase 1-3 benchmark and census data. | Only listed differences | Coordinated upgrade |

### 8.1 Switch for consensus-visible phases

Phases 2-4 change results. Follow substrate's rules for such changes:

- The new behaviour is the default in the release that ships it. The release
  is a coordinated chaincode upgrade of every peer on the channel and every
  pre-deploy checker.
- A temporary hatch, `SUBSTRATE_HANDLEBARS_LEGACY=true` (exact string only),
  keeps the old engine for a phylum that is not ready. Set it identically on
  every peer. Remove it after one release.
- No per-request or per-phylum switch inside one channel: two peers must never
  run two engines for one transaction.
- Before the upgrade, run the harness over each phylum's corpus. Any
  difference not on the allowlist stops the rollout.

## 9. Open questions

| # | Question | Recommended answer | Confidence |
|---|---|---|---|
| Q1 | Where does the vendored code live? | `svc/libhandlebars/internal/raymond`. substrate already imports svc, so the direction does not change. | 85% |
| Q2 | Fix D1 to sorted order, or to exact decimal sum? | Sorted order. Smallest change; decimal is a new behaviour. | 70% |
| Q3 | Can any production template reach D1 today? | Unknown until the Phase 0 census. If yes, raise Phase 2 priority. | n/a |
| Q4 | Are templates ever read from ledger data or requests? | Unknown. If yes, caps (Phase 3) become security work. | n/a |
