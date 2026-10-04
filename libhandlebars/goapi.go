// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sync"

	"github.com/luthersystems/svc/libhandlebars/hbs"
	"github.com/sirupsen/logrus"
)

// Template is a parsed template.
type Template = *hbs.Program

// Parse parses a template with hbs.DefaultLimits().
func Parse(template string) (Template, error) {
	return hbs.ParseCached(template, hbs.DefaultLimits())
}

// ParseWith parses a template under the configured template-size and
// nesting limits (see Config). Render it with the same options to render
// under the same limits. With the default cache bounds it parses through
// the process-wide cache; with others it does not cache (a Template is
// the caller's to keep).
func ParseWith(template string, opts ...Option) (Template, error) {
	cfg, err := buildConfig(opts)
	if err != nil {
		return nil, err
	}
	if p := newParser(cfg); p.cache != nil {
		return hbs.Parse(template, cfg.Limits)
	}
	return hbs.ParseCached(template, cfg.Limits)
}

// Render renders tpl with the Go value ctx in hbs.ModeCompat, under
// hbs.DefaultLimits(). It is RenderWith with no options.
func Render(tpl Template, ctx interface{}) (string, error) {
	return RenderWith(tpl, ctx)
}

// RenderOption configures RenderWith: any Option (see Config).
type RenderOption = Option

// JSONGoContextEnv is the environment variable that makes JSON conversion
// the default for Render and RenderWith. It is read once, at the first
// render; only the exact value "true" enables it, and any other non-empty
// value is ignored with a warning.
const JSONGoContextEnv = "SVC_HANDLEBARS_JSON_GO_CONTEXT"

var jsonGoContextDefault = sync.OnceValue(func() bool {
	v, ok := os.LookupEnv(JSONGoContextEnv)
	return jsonGoContextSetting(v, ok, logrus.StandardLogger())
})

// jsonGoContextSetting interprets JSONGoContextEnv: only "true" enables it;
// another non-empty value is ignored and logged.
func jsonGoContextSetting(v string, ok bool, log logrus.FieldLogger) bool {
	switch {
	case !ok || v == "":
		return false
	case v == "true":
		log.WithField("env", JSONGoContextEnv).Info("libhandlebars: Go render contexts are converted through JSON")
		return true
	default:
		log.WithField("env", JSONGoContextEnv).WithField("value", v).Warn(`libhandlebars: ignoring the value; only "true" enables JSON Go contexts`)
		return false
	}
}

// RenderWith renders tpl with the Go value ctx in hbs.ModeCompat, configured
// by opts (see Config: under DefaultConfig() unless they set otherwise).
//
// By default the engine reads ctx itself, lazily and by reflection, with
// the Go semantics raymond gave Go values (see "Go values in a render
// context" in hbs/goreflect.go): ints stay ints, named types reach helpers
// as themselves, structs are read by field name and handlebars tag, and
// only what the template touches is read. Where raymond called Go code (a
// method or func the template looks up) the render fails instead.
// WithJSONContext, or SVC_HANDLEBARS_JSON_GO_CONTEXT=true, converts ctx
// through JSON instead, as handlebars:render does; that conversion counts
// against the render's MaxSteps and fails past 1024 levels of nesting.
//
// The caller's own Go code is the caller's responsibility, in cost and in
// determinism: prettyp-num-en's error text prints a value with fmt's %v,
// which calls its String or Error method, as raymond did, and JSON mode
// calls MarshalJSON and MarshalText methods.
func RenderWith(tpl Template, ctx interface{}, opts ...RenderOption) (out string, err error) { //nolint:nonamedreturns // set by the recover below
	// A panic from the caller's own methods (MarshalJSON, String) or a
	// bug becomes an error, as the ELPS path's does, not a crash.
	defer func() {
		if p := recover(); p != nil {
			out, err = "", fmt.Errorf("libhandlebars: render panicked: %v", p)
		}
	}()
	return renderWith(tpl, ctx, opts...)
}

func renderWith(tpl Template, ctx interface{}, opts ...RenderOption) (string, error) {
	cfg, err := buildConfig(opts)
	if err != nil {
		return "", err
	}
	lim := cfg.Limits
	asJSON := cfg.GoContext == GoContextJSON || cfg.GoContext == GoContextDefault && jsonGoContextDefault()
	var v hbs.Value
	if asJSON {
		// The conversion counts against the render's MaxSteps: the
		// marshal is charged by a walk first, then the decode.
		bud := &goBudget{max: lim.MaxSteps}
		// A value encoding/json would refuse fails here, with its text,
		// before json.Marshal runs.
		if err := goJSONCost(bud, reflect.ValueOf(ctx), jsonGoMaxDepth); err != nil {
			return "", err
		}
		b, err := json.Marshal(ctx)
		if err != nil {
			return "", err
		}
		if v, err = hbs.FromJSONMetered(b, bud); err != nil {
			return "", err
		}
		lim.MaxSteps = max(1, lim.MaxSteps-bud.used)
	} else {
		// The engine reads a Go value lazily, by reflection, as raymond did.
		v = ctx
	}
	return tpl.Render(v, hbs.Options{Mode: hbs.ModeCompat, Limits: lim})
}
