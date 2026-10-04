// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars

import (
	"fmt"

	"github.com/luthersystems/elps/elpsutil"
	"github.com/luthersystems/svc/libhandlebars/hbs"
)

// Config is every setting of the Handlebars package an embedder may
// choose. A zero field takes its DefaultConfig() value, and a negative one
// is an error; there is no package-level setting.
//
// Fields marked consensus-visible change what a render returns (its
// output, its error, or the steps it charges): every peer that endorses a
// transaction, and every check made before a deploy, must use the same
// value, and the embedder must ensure it. Fields marked performance-only
// change only speed and memory, and may differ between peers.
//
// The other bounds the engine applies are fixed: they mirror encoding/json
// and libjson (an ELPS context nests to the runtime's value-depth limit,
// as libjson's encoder allows, and decodes within encoding/json's 10,000
// levels; a native's load check refuses 10,000; a Go JSON-mode context
// nests 1024 container levels), or keep a render from crashing the process
// (hbs.MaxDepthCeiling, the 50,000-level Go value walk, the 64-level and
// 16384-field embedding searches), or are the cost model itself (the step
// units in hbs/DETERMINISM.md, which hbs.Version pins). See
// DETERMINISM.md, "Configuration".
type Config struct {
	// Limits are the template, output, depth and step limits, and the
	// produced-bytes factor (hbs.Limits; each consensus-visible):
	//   - MaxTemplateBytes: a longer template fails to parse.
	//   - MaxDepth: deeper nesting fails to parse or render. It bounds the
	//     engine's recursion, so it is at most hbs.MaxDepthCeiling (10,000):
	//     a larger value is an error.
	//   - MaxOutputBytes: longer output fails to render.
	//   - MaxSteps: a render charging more steps fails. It meters the
	//     render and, for a Go caller in JSON mode, the context's
	//     conversion. The parse, and the ELPS package's context encode and
	//     decode, are charged to the caller's ELPS step budget only (the
	//     parse is bounded by MaxTemplateBytes, the encode by
	//     Runtime.MaxAlloc and libjson's nesting limits).
	//   - ProducedFactor: a render producing more than ProducedFactor ×
	//     MaxOutputBytes in all (captured sections, helper strings) fails.
	Limits hbs.Limits

	// ParseCacheMaxBytes and ParseCacheMaxEntryBytes bound the cache of
	// parse verdicts (performance-only: a hit returns and charges exactly
	// what a miss does). With both at their defaults, loaders and Go
	// callers share the process-wide cache; otherwise each loader uses a
	// cache of its own with these bounds, and ParseWith does not cache. An
	// entry bound above the byte bound is allowed: an entry is kept only
	// if it fits both.
	ParseCacheMaxBytes      int
	ParseCacheMaxEntryBytes int

	// GoContext is how RenderWith reads a Go context (Go API only; the
	// output can differ, so callers that must agree must use the same
	// mode): GoContextDefault follows SVC_HANDLEBARS_JSON_GO_CONTEXT,
	// GoContextReflect reads the value itself, and GoContextJSON converts
	// it through JSON. The environment variable is read once, at the
	// process's first Render or RenderWith call. The ELPS package ignores
	// it.
	GoContext GoContextMode
}

// GoContextMode is Config.GoContext.
type GoContextMode string

// The Go context modes.
const (
	GoContextDefault GoContextMode = ""
	GoContextReflect GoContextMode = "reflect"
	GoContextJSON    GoContextMode = "json"
)

// DefaultConfig is the configuration LoadPackage, Parse and Render use.
func DefaultConfig() Config {
	return Config{
		Limits:                  hbs.DefaultLimits(),
		ParseCacheMaxBytes:      hbs.DefaultParseCacheMaxBytes,
		ParseCacheMaxEntryBytes: hbs.DefaultParseCacheMaxEntryBytes,
	}
}

// Option sets part of a Config. Options apply in order, each over the
// last: WithConfig sets the whole Config, the others one field.
type Option func(*Config)

// WithConfig sets every field to c's.
func WithConfig(c Config) Option { return func(cfg *Config) { *cfg = c } }

// WithLimits sets Config.Limits.
func WithLimits(lim hbs.Limits) Option { return func(cfg *Config) { cfg.Limits = lim } }

// WithMaxTemplateBytes sets Config.Limits.MaxTemplateBytes.
func WithMaxTemplateBytes(n int) Option { return func(cfg *Config) { cfg.Limits.MaxTemplateBytes = n } }

// WithMaxDepth sets Config.Limits.MaxDepth.
func WithMaxDepth(n int) Option { return func(cfg *Config) { cfg.Limits.MaxDepth = n } }

// WithMaxOutputBytes sets Config.Limits.MaxOutputBytes.
func WithMaxOutputBytes(n int) Option { return func(cfg *Config) { cfg.Limits.MaxOutputBytes = n } }

// WithMaxSteps sets Config.Limits.MaxSteps.
func WithMaxSteps(n int64) Option { return func(cfg *Config) { cfg.Limits.MaxSteps = n } }

// WithProducedFactor sets Config.Limits.ProducedFactor.
func WithProducedFactor(n int) Option { return func(cfg *Config) { cfg.Limits.ProducedFactor = n } }

// WithParseCache sets Config.ParseCacheMaxBytes and ParseCacheMaxEntryBytes.
func WithParseCache(maxBytes, maxEntryBytes int) Option {
	return func(cfg *Config) { cfg.ParseCacheMaxBytes, cfg.ParseCacheMaxEntryBytes = maxBytes, maxEntryBytes }
}

// WithJSONContext converts a Go ctx through JSON, as handlebars:render
// converts an ELPS context: json.Marshal, then hbs.FromJSON. Every number
// becomes a float64, so a Go int 3 renders {{to-str n}} as "3.000000" and
// is not a literal zero for includeZero. ctx must marshal to a JSON object
// or null. It sets Config.GoContext to GoContextJSON.
func WithJSONContext() Option { return func(cfg *Config) { cfg.GoContext = GoContextJSON } }

// WithGoContext renders the Go value ctx itself (see RenderWith), whatever
// SVC_HANDLEBARS_JSON_GO_CONTEXT says. It sets Config.GoContext to
// GoContextReflect.
func WithGoContext() Option { return func(cfg *Config) { cfg.GoContext = GoContextReflect } }

// buildConfig applies opts over a zero Config and resolves it.
func buildConfig(opts []Option) (Config, error) {
	var cfg Config
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	return resolveConfig(cfg)
}

// resolveConfig is c with each zero field set to its default; a negative
// field, or an unknown GoContext, is an error.
func resolveConfig(c Config) (Config, error) {
	l := c.Limits
	for _, f := range []struct {
		name string
		v    int64
	}{
		{"Limits.MaxTemplateBytes", int64(l.MaxTemplateBytes)},
		{"Limits.MaxDepth", int64(l.MaxDepth)},
		{"Limits.MaxOutputBytes", int64(l.MaxOutputBytes)},
		{"Limits.MaxSteps", l.MaxSteps},
		{"Limits.ProducedFactor", int64(l.ProducedFactor)},
		{"ParseCacheMaxBytes", int64(c.ParseCacheMaxBytes)},
		{"ParseCacheMaxEntryBytes", int64(c.ParseCacheMaxEntryBytes)},
	} {
		if f.v < 0 {
			return Config{}, fmt.Errorf("libhandlebars: %s is %d; must be >= 0", f.name, f.v)
		}
	}
	if l.MaxDepth > hbs.MaxDepthCeiling {
		return Config{}, fmt.Errorf("libhandlebars: Limits.MaxDepth is %d; must be <= %d (hbs.MaxDepthCeiling)", l.MaxDepth, hbs.MaxDepthCeiling)
	}
	switch c.GoContext {
	case GoContextDefault, GoContextReflect, GoContextJSON:
	default:
		return Config{}, fmt.Errorf("libhandlebars: unknown GoContext %q", c.GoContext)
	}
	def := DefaultConfig()
	setDefault(&c.Limits.MaxTemplateBytes, def.Limits.MaxTemplateBytes)
	setDefault(&c.Limits.MaxDepth, def.Limits.MaxDepth)
	setDefault(&c.Limits.MaxOutputBytes, def.Limits.MaxOutputBytes)
	setDefault(&c.Limits.MaxSteps, def.Limits.MaxSteps)
	setDefault(&c.Limits.ProducedFactor, def.Limits.ProducedFactor)
	setDefault(&c.ParseCacheMaxBytes, def.ParseCacheMaxBytes)
	setDefault(&c.ParseCacheMaxEntryBytes, def.ParseCacheMaxEntryBytes)
	return c, nil
}

func setDefault[T int | int64](v *T, def T) {
	if *v == 0 {
		*v = def
	}
}

// parser parses through the process-wide cache under the defaults' cache
// bounds, and through a cache of its own otherwise.
type parser struct {
	cache *hbs.ParseCache // nil: the process-wide cache
}

func newParser(c Config) parser {
	def := DefaultConfig()
	if c.ParseCacheMaxBytes == def.ParseCacheMaxBytes && c.ParseCacheMaxEntryBytes == def.ParseCacheMaxEntryBytes {
		return parser{}
	}
	return parser{hbs.NewParseCache(c.ParseCacheMaxBytes, c.ParseCacheMaxEntryBytes)}
}

func (p parser) parse(src string, lim hbs.Limits, m hbs.Meter) (*hbs.Program, error) {
	if p.cache == nil {
		if m == nil {
			return hbs.ParseCached(src, lim)
		}
		return hbs.ParseCachedMetered(src, lim, m)
	}
	return p.cache.Parse(src, lim, m)
}

// LoadPackageWith returns a loader for the package configured by opts
// (applied in order over a zero Config: see Config). Its render,
// render-fixed and must-parse parse and render under the configured
// limits, in every environment it loads; LoadPackage is LoadPackageWith
// with no options. Substrate-style embedders pass WithConfig(cfg); others
// set only what they need, e.g. WithMaxTemplateBytes(4 << 20).
func LoadPackageWith(opts ...Option) (elpsutil.Loader, error) {
	cfg, err := buildConfig(opts)
	if err != nil {
		return nil, err
	}
	return elpsutil.PackageLoader(&handlebarsPackage{lim: cfg.Limits, parser: newParser(cfg)}), nil
}
