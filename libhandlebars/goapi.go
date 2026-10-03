// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars

import (
	"encoding/json"
	"os"
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

// WithGoContext converts ctx natively (hbs.FromGo), whatever
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
// By default ctx is converted natively by hbs.FromGo, with the Go
// semantics the raymond engine gave Go values: ints stay ints, structs are
// looked up by field name and handlebars tag, and the output for such a
// context is what raymond rendered (see hbs.FromGo for the few Go values,
// such as funcs and methods, that are an error instead). WithJSONContext,
// or SVC_HANDLEBARS_JSON_GO_CONTEXT=true, converts it through JSON instead,
// as handlebars:render does.
func RenderWith(tpl Template, ctx interface{}, opts ...RenderOption) (string, error) {
	cfg := renderConfig{json: jsonGoContextDefault()}
	for _, o := range opts {
		o(&cfg)
	}
	lim := hbs.DefaultLimits()
	var v hbs.Value
	var err error
	if cfg.json {
		var b []byte
		if b, err = json.Marshal(ctx); err != nil {
			return "", err
		}
		v, err = hbs.FromJSON(b)
	} else {
		v, err = hbs.FromGo(ctx, lim, nil)
	}
	if err != nil {
		return "", err
	}
	return tpl.Render(v, hbs.Options{Mode: hbs.ModeCompat, Limits: lim})
}
