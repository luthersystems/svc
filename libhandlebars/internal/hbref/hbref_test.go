package hbref

import (
	"errors"
	"testing"
)

func TestRenderJSONStages(t *testing.T) {
	tests := []struct {
		name, tpl, ctx string
		out            string
		stage          Stage
		elps           string
	}{
		{name: "ok", tpl: "{{a}}", ctx: `{"a":"<x>"}`, out: "&lt;x&gt;"},
		{name: "null context", tpl: "[{{a}}]", ctx: `null`, out: "[]"},
		{name: "unmarshal", tpl: "{{a}}", ctx: `[1]`, stage: StageUnmarshal,
			elps: "error while unmarshaling: json: cannot unmarshal array into Go value of type map[string]interface {}"},
		{name: "parse", tpl: "{{x.5}}", ctx: `{}`, stage: StageParse,
			elps: "error parsing template: Parse error on line 1:\nExpecting ID, got: 'Number{\"5\"}'"},
		{name: "render", tpl: "{{global \"a\" key=missing}}", ctx: `{}`, stage: StageRender,
			elps: "error while rendering template: global: missing key"},
		{name: "panic", tpl: "{{@this}}", ctx: `{}`, stage: StagePanic,
			elps: "internal error (recovered panic): runtime error: index out of range [0] with length 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := RenderJSON(tt.tpl, []byte(tt.ctx))
			if tt.stage == "" {
				if err != nil || out != tt.out {
					t.Fatalf("got %q, %v; want %q", out, err, tt.out)
				}
				return
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("got %q, %v; want stage %s", out, err, tt.stage)
			}
			if e.Stage != tt.stage || e.ELPS != tt.elps {
				t.Fatalf("got %s %q; want %s %q", e.Stage, e.ELPS, tt.stage, tt.elps)
			}
		})
	}
}

func TestMustParse(t *testing.T) {
	if err := MustParse("{{#if a}}x{{/if}}"); err != nil {
		t.Fatal(err)
	}
	// must-parse accepts @this; only rendering panics.
	if err := MustParse("{{@this}}"); err != nil {
		t.Fatal(err)
	}
	var e *Error
	if err := MustParse("{{#if a}}"); !errors.As(err, &e) || e.Stage != StageParse {
		t.Fatalf("got %v", err)
	}
}
