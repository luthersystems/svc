// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars

import (
	"encoding/json"
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

// Render renders tpl with the Go value ctx in hbs.ModeCompat, under
// hbs.DefaultLimits(). It is RenderWith with no options.
func Render(tpl Template, ctx interface{}) (string, error) {
	return RenderWith(tpl, ctx)
}

// RenderOption configures RenderWith.
type RenderOption func(*renderConfig)

type renderConfig struct {
	json bool
}

// WithJSONContext converts ctx through JSON, as handlebars:render converts
// an ELPS context: json.Marshal, then hbs.FromJSON. Every number becomes a
// float64, so a Go int 3 renders {{to-str n}} as "3.000000" and is not a
// literal zero for includeZero. ctx must marshal to a JSON object or null.
func WithJSONContext() RenderOption {
	return func(c *renderConfig) { c.json = true }
}

// WithGoContext renders the Go value ctx itself (see RenderWith), whatever
// SVC_HANDLEBARS_JSON_GO_CONTEXT says.
func WithGoContext() RenderOption {
	return func(c *renderConfig) { c.json = false }
}

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

// RenderWith renders tpl with the Go value ctx in hbs.ModeCompat, under
// hbs.DefaultLimits().
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
func RenderWith(tpl Template, ctx interface{}, opts ...RenderOption) (string, error) {
	cfg := renderConfig{json: jsonGoContextDefault()}
	for _, o := range opts {
		o(&cfg)
	}
	lim := hbs.DefaultLimits()
	var v hbs.Value
	if cfg.json {
		// The conversion counts against the render's MaxSteps: the
		// marshal is charged by a walk first, then the decode.
		bud := &goBudget{max: lim.MaxSteps}
		deep, err := goJSONCost(bud, reflect.ValueOf(ctx), 0, jsonGoMaxDepth, map[uintptr]bool{})
		if err != nil {
			return "", err
		}
		if deep != nil {
			return "", deep
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
