// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbdiff

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"regexp"
	"unicode/utf8"
)

// Golden is the recorded reference result of one case. A nondeterministic
// reference case records only that fact.
type Golden struct {
	Out    *string `json:"out,omitempty"`
	OutB64 []byte  `json:"out_base64,omitempty"` // instead of Out, when not valid UTF-8
	SHA256 string  `json:"sha256,omitempty"`     // instead of Out, for large outputs
	Len    int     `json:"len,omitempty"`
	Kind   ErrKind `json:"kind,omitempty"`
	Msg    string  `json:"msg,omitempty"`
	MsgB64 []byte  `json:"msg_base64,omitempty"` // instead of Msg, when not valid UTF-8
	Nondet bool    `json:"nondeterministic,omitempty"`
}

// GoldenOf records r. With hashOut, the output is stored as its SHA-256
// and length.
func GoldenOf(r Result, alt []Result, hashOut bool) Golden {
	if len(alt) > 0 {
		return Golden{Nondet: true}
	}
	g := Golden{Kind: r.ErrKind, Msg: r.ErrMsg}
	if !utf8.ValidString(r.ErrMsg) {
		g.Msg, g.MsgB64 = "", []byte(r.ErrMsg)
	}
	if r.ErrKind != KindNone {
		return g
	}
	switch {
	case hashOut:
		g.SHA256, g.Len = hashString(r.Out), len(r.Out)
	case !utf8.ValidString(r.Out):
		g.OutB64 = []byte(r.Out)
	default:
		out := r.Out
		g.Out = &out
	}
	return g
}

func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// Matches reports whether r is the recorded result. Nondeterministic
// goldens match anything.
func (g Golden) Matches(r Result) bool {
	if g.Nondet {
		return true
	}
	msg := g.Msg
	if g.MsgB64 != nil {
		msg = string(g.MsgB64)
	}
	if r.ErrKind != g.Kind || engineNeutral(r.ErrMsg) != engineNeutral(msg) {
		return false
	}
	if r.ErrKind != KindNone {
		return true
	}
	if g.Out != nil {
		return *g.Out == r.Out
	}
	if g.OutB64 != nil {
		return string(g.OutB64) == r.Out
	}
	return g.SHA256 == hashString(r.Out) && g.Len == len(r.Out)
}

// unmarshalField is the part of a json.Unmarshal type error that only Go
// 1.27's v2 JSON engine writes for a map value: v2 names the field ("Go
// struct field .a of type float64"), v1 does not ("Go value of type
// float64"). The reference decodes with json.Unmarshal, so its error text
// depends on the engine the test runs on.
var unmarshalField = regexp.MustCompile(`into Go struct field \.[^ ]* of type `)

// engineNeutral rewrites msg to the v1 engine's text, so that a golden
// recorded on either engine matches a run on either.
func engineNeutral(msg string) string {
	return unmarshalField.ReplaceAllString(msg, "into Go value of type ")
}

// ReadGoldens reads a golden file. A missing file is an empty set.
func ReadGoldens(file string) (map[string]Golden, error) {
	b, err := os.ReadFile(file) //nolint:gosec // harness testdata path
	if os.IsNotExist(err) {
		return map[string]Golden{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]Golden{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// WriteGoldens writes a golden file with sorted keys and no HTML escaping,
// so diffs of it are readable.
func WriteGoldens(file string, m map[string]Golden) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return err
	}
	return os.WriteFile(file, b.Bytes(), 0o600)
}
