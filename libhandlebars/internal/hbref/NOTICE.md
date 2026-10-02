# hbref — FROZEN

This package is the frozen reference for the differential harness
(`internal/hbdiff`). It reproduces svc's handlebars pipeline as it stood
before the native engine:

- `helpers.go` is `addHelpers` and the helper functions after it, copied
  verbatim from `libhandlebars/libhandlebars.go`, with only the raymond
  import pointed at `internal/raymondref` (itself a frozen copy).
- `hbref.go` calls them the way `handlebars:render` and
  `handlebars:must-parse` did: `json.Unmarshal` into
  `map[string]interface{}`, `raymond.Parse`, `addHelpers`, `Exec`. Errors
  carry the stage that failed and the exact message the ELPS builtin showed.
  A runtime-error or non-error panic, which raymond re-panics and ELPS
  reports as `internal-panic`, is recovered and reported as stage `panic`.

**Do not edit this package.** Its behaviour, bugs included, is the
specification the new engine's compat mode is tested against. The golden
files in `internal/hbdiff/testdata/golden` pin its output; if a Go toolchain
or dependency bump (`net/url`, `go-humanize`, `phonenumbers`, `strconv`,
`time`) changes that output, the golden test fails and the change must be
reviewed, not regenerated blindly. Production code must not import it.
