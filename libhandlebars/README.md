# Luther Handlebars Templating Library

Luther's templating library is an extension of [handlebars](https://handlebarsjs.com/) with some additional bulitins. These helper functions make writing complex templates simpler, while striving to maintain the spirit of handlebar's declarative and minimal style.

## Differences from handlebars
  - Rendered by `hbs`, a native Go engine for the Handlebars 3 dialect that luthersystems/raymond rendered (raymond is kept only as a frozen test reference in `internal/raymondref`)
  - New builtins: eq, len, not, and, or, gt, gte, lt, lte, times, div, mod, plus, minus, select, global
  - log builtin is disabled
  - printing maps is disabled (attempting to print a map will result in the string "UNPRINTABLE")

## ELPS functions
  - `(handlebars:render tpl ctx)`: renders tpl with ctx. Output is byte-compatible with the raymond-based releases, helper bugs included (`hbs.ModeCompat`). The only changes: `plus`/`minus` add hash values in sorted key order (the old order was random), `{{@this}}` is a render error instead of a crash, and `to-int` of NaN, ±Inf or an out-of-range number is `-9223372036854775808` on every CPU.
  - `(handlebars:render-fixed tpl ctx)`: renders with the known helper bugs fixed (`hbs.ModeFixed`; the list is in `hbs/helpers_svc.go`). A phylum opts in by calling it; its output can differ from `render`'s. It also keeps ELPS ints as ints: `render` converts the context through JSON, where every number is a float64, so for an ELPS int 3 `{{to-str n}}` renders `3.000000` with `render` and `3` with `render-fixed`, and an int above 2^53 prints exactly. A bytes (JSON) context is decoded as JSON by both.
  - `(handlebars:must-parse tpl)`: validates tpl without rendering it.
  - `(handlebars:libname)` returns `"luthersystems/svc/hbs"`, and `(handlebars:version)` returns the engine version (`hbs.Version`). The version changes whenever a release changes any output, error or step charge.

## Go API
  - `Parse` returns a `Template`, now `*hbs.Program` (it was a raymond `*Template`), and `Render(tpl, ctx)` renders it in compat mode.
  - By default `Render` reads the Go `ctx` itself, lazily and by reflection, as raymond did, so Go callers see the old behaviour: an `int` stays an `int` (`{{to-str n}}` is `3`, and `{{#if n includeZero=true}}` with `n` = 0 is true), named types reach helpers as themselves, structs are read by field name (`name` finds `Name`, promoted fields included) and `handlebars` tag, maps with `interface{}` keys are read by string key, and `#each` visits struct fields in declaration order and map keys sorted. Only what the template touches is read, every read is charged against the render's step limit, and pointer chains are bounded, so a cyclic or huge value costs what the template does with it. Where raymond ran Go code, the render fails instead with an error naming it: a method a template looks up (`{{getName}}`, or `{{name}}` when the type has a `Name` method) or a func it reaches. Where raymond panicked or hung (an unexported tagged field, a nil embedded pointer, printing a channel, a pointer cycle the template touches), the render fails with an error. Methods of the caller's own types that still run, as they did under raymond (`String`/`Error` in `prettyp-num-en`'s error text, `MarshalJSON`/`MarshalText` in JSON mode), are the caller's responsibility for cost and determinism.
  - JSON mode converts `ctx` with `json.Marshal` and the engine's decoder, as `handlebars:render` converts an ELPS value: every number becomes a `float64` and structs follow their JSON encoding. Select it per call with `RenderWith(tpl, ctx, WithJSONContext())`, or for the process with the environment variable `SVC_HANDLEBARS_JSON_GO_CONTEXT=true`, read once at the first render (only the exact string `true` enables it; any other value is ignored and logged). `WithGoContext()` selects the native conversion whatever the variable says.

## Limits and steps
  - Templates are limited to 1 MiB and 256 levels of nesting (`handlebars-parse`). A render is limited to 16 MiB of output, 128 MiB of bytes produced in all (output, sections a helper captured, and strings helpers build), and 2^25 evaluation steps, whether or not an ELPS step budget is set (`handlebars-render`).
  - These are the defaults (`libhandlebars.DefaultConfig()`). An embedder sets others per loader with `libhandlebars.LoadPackageWith(...)`: `WithConfig(cfg)` for a whole `Config`, or `WithMaxTemplateBytes(4 << 20)` and the like for one field (for example for templates that embed images as data URIs); later options override earlier ones. Go callers pass the same options to `ParseWith` and `RenderWith`. A zero field keeps its default; a negative one is an error. Limits change what renders, so every peer that endorses a transaction, and every pre-deploy check, must use the same consensus-visible settings; the parse cache bounds only change speed and memory (see `hbs/DETERMINISM.md`, "Configuration").
  - Steps bound time: every operation is charged so that no step takes much more than the evaluator's base cost (about 80 ns at worst on a 2.1 GHz vCPU), so the 2^25-step limit ends any render within a few seconds. Parsing costs 6 steps per lexer token plus 1 per 16 bytes of template on every call (parses are cached, but a hit costs the same); encoding and decoding the context are charged per value and per byte. The full cost model is the table in `hbs/DETERMINISM.md`. A render that exhausts the ELPS step budget stops early with the budget condition.

## Errors
  - Template syntax errors and template limits signal `handlebars-parse`; evaluation errors and the output limit signal `handlebars-render`.

## Builtins
* *eq*: check equality on strings and numbers.
```
template: {{eq foo 1.2}}
context: (sorted-map "foo" 1.2)
output: true
```
* *len*: get the length of a sequence.
```
template: {{len array}}
context: (vector 1 2 3)
output: 3
```
* *not*: Logical negation.
```
template: {{not foo }}
context: (sorted-map "foo" true)
output: false
```
* *and*: Logical conjunction.
```
template: {{and test1=true test2=true test3=foo }}
context: (sorted-map "foo" false)
output: false
```
* *or*: Logical disjunction.
```
template: {{and test1=foo test2=false test3=false }}
context: (sorted-map "foo" true)
output: true
```
* *gt*: Check if a number is greater than another number.
```
template: {{#if (gt foo 2.2)}}yes{{/if}}
context: (sorted-map "foo" 13.1)
output: yes
```
* *gte*: Check if a number is greater than or equal to another number.
```
template: {{#if (gte foo 2.2)}}yes{{/if}}
context: (sorted-map "foo" 2.2)
output: yes
```
* *lt*: Check if a number is less than another number.
```
template: {{#if (lt foo 13.1)}}yes{{/if}}
context: (sorted-map "foo" 2)
output: yes
```
* *lte*: Check if a number is less than or equal to another number.
```
template: {{#if (lte foo 2.2)}}yes{{/if}}
context: (sorted-map "foo" 2.2)
output: yes
```
* *times*: Multiply two numbers.
```
template: {{times foo foo}}"""
context: (sorted-map "foo" 2)
output: 4
```
 * *divide*: Divide two numbers.
```
template: {{div foo 2}}
context: (sorted-map "foo" 5)
output: 2.5
```
*  *plus*: Add two numbers.
```
template: {{plus var=foo const1=2 const2=3}}
context: (sorted-map "foo" 2)
output: 7
```
* *minus*: Subtract two numbers.
```
template: {{minus foo const1=2 const2=1}}
context: (sorted-map "foo" 4)
output: 1
```
* *mod*: The modulo of two numbers (remainder).
```
template: {{mod foo 2}}
context: (sorted-map "foo" 3)
output: 1
```
* *select*: Retrieve fields from filtered maps that are within an array of maps. It works similar to the SQL pattern of `SELECT <col> FROM <table> WHERE <cond>`, where here the table is a list of maps, the col is a field on that map whose value is retrieved, and cond is a condition that selects only the maps with a certain key-value pair.
```
template: {{#select from=metadata where="name=JWKS_URI"}}{{string_val}}{{/select}}
context:  (sorted-map "metadata"
            (vector
              (sorted-map "name" "AUTH_NAME" "string_val" "Luther")
              (sorted-map "name" "JWKS_URI" "string_val" "www.luthersystems.com")
              (sorted-map "name" "ENABLED" "bool_val" true)))
output: www.luthersystems.com
```
* *global*: create [string,string] maps in the template. This allows you to replace enum values like "INVALID_STATUS" with human readable names like "Invalid Status". It exposes an ability to create name spaces where the template can set keys on a name space using the command:
    ```{{global "testns1" key="INVALID" val="Invalid"}}```

    Here the namespace is "testns1", and the command is inserting a value "Invalid" for key "INVALID" into that namespace. To retrieve the previously inserted value, use the same command without the put:
    ```{{global "testns1" key=tKey}}```

    In this example, `tKey` is a variable available within the context.
```
template: {{global "testns1" key="INVALID" val="Invalid"}}{{global"testns1" key=tKey}}
context: (sorted-map "tKey" "INVALID")
output: Invalid
```
* *is-after*: Check if a given date is after a reference date.
```
template: {{is-after "2020-01-01" "2019-10-01"}}
output: true
```
* *date-diff-month*: Calculate the difference between two dates in months. Always rounded up.
```
template: {{date-diff-month "2020-01-01" "2019-01-02"}}
output: 1
```
* *date-add-months*: Add X months to a given date. Supports negative months.
```
template: {{date-add-months "2020-01-01" -1}}
output: 2019-12-01
```
* *round-to-nth*: Round a float to the nearest n decimal digit string.
```
template: {{round-to-nth 1.999 2}}
output: 2.00
```

* *in-string-array*: Check if element exist inside an array. Return true or false.
```
template: {{in-string-array haystack=array needle="foo"}}
context: (sorted-map "array" (list "foo" "bar"))
output: true
```

## Pretty Printing

### **prettyp-num-en**:
Pretty print numbers with `en` formatting. All numbers formatted to 2dp.

```
template: {{prettyp-num-en 20000}}
output: 20,000.00
```

Formatted to 2dp
```
template: {{prettyp-num-en 10.1}}
output: 10.10
```

```
template: {{prettyp-num-en 0001}}
output: 1
```

 Number in string is accepted
```
template: {{prettyp-num-en "1212.12"}}
output: 1,212.12
```

### **possessive**:
Format possessives terms. Eg. `John` -> `John's`  or `lloyds` -> `lloyds'`

⚠️ Please remember to use `{{{ }}}` so that HTML is not escaped. If you see `&apos;` in your output, this is more likely because `{{ }}` is used.

Word of warning: Possessives have exceptions which is not addressed in this implementation.

```
template: {{{possessive name}}}
context: (sorted-map "name" "Chris")
output: Chris'
```

```
template: {{{possessive name}}}
context: (sorted-map "name" "David")
output: David's
```

It adds possessives on the last word only, so full name is accepted.
```
template: {{{possessive name}}}
context: (sorted-map "name" "David Fincher")
output: David Fincher's
```

Trailing spaces will be ignored
```
template: {{{possessive name}}}
context: (sorted-map "name" "David Fincher             ")
output: David Fincher's
```

## Date Formatting

### **Date-Beautify**
Format YYYY-MM-DD into writing form. eg. 13 January 2020

```
template: {{{date-beautify "2020-01-13}}}
output: 13 January 2020
```

### **Date-DDMMYY-slash**
Format YYYY-MM-DD into DD/MM/YY

```
template: {{{date-DDMMYY-slash "2020-01-13}}}
output: 13/01/20
```

### **Date-DDMMYYYY-slash**
Format YYYY-MM-DD into DD/MM/YYYY

```
template: {{{date-DDMMYYYY-slash "2020-01-13}}}
output: 13/01/2020
```

### **Date-DDMMYYYY**
Format YYYY-MM-DD into DD-MM-YYYY

```
template: {{{date-DDMMYYYY "2020-01-13}}}
output: 13-01-2020
```
