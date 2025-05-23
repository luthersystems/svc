// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package midware

import (
	"bytes"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// DefaultTraceHeader is the default header when TraceHeaders is given an empty
// string instead of a valid header name.
var DefaultTraceHeader = "X-Request-Id"

// DefaultAzureHeader is the default Azure header that contains a unique guid
// generated by application gateway for each client request and presented in
// the forwarded request to the backend pool member.
var DefaultAzureHeader = "X-Appgw-Trace-Id"

// DefaultAWSHeader is the default AWS header that can be used for request tracing
// to track HTTP requests from clients to targets or other services
var DefaultAWSHeader = "X-Amzn-Trace-Id"

// PathOverrides is middleware which overrides handling for a specified set of
// http request paths.  Each entry in a PathOverrides map is an http request
// path and the associated handler will be used to serve that path instead of
// allowing the middleware's "natural" inner handler to serve the request.
type PathOverrides map[string]http.Handler

// protectedPathOverrides behaves like PathOverrides, but also disallows overrides
// that would conflict with specific reserved subtrees (e.g., static mounts).
// The protectedSubtrees map defines path prefixes under which no additional
// overrides may be registered — for example, to protect paths like "/v1/public/"
// from nested handlers like "/v1/public/foo".
type protectedPathOverrides struct {
	overrides         PathOverrides
	protectedSubtrees map[string]bool
}

func NewProtectedPathOverrides(overrides map[string]http.Handler, protectedSubtrees []string) protectedPathOverrides {
	ps := make(map[string]bool, len(protectedSubtrees))
	for _, path := range protectedSubtrees {
		ps[path] = true
	}
	return protectedPathOverrides{
		overrides:         overrides,
		protectedSubtrees: ps,
	}
}

// Wrap implements the Middleware interface.
func (m protectedPathOverrides) Wrap(next http.Handler) http.Handler {
	var prefixes []string

	for path := range m.overrides {
		for root := range m.protectedSubtrees {
			if path != root && strings.HasPrefix(path, root) {
				panic(fmt.Sprintf("PathOverride conflict: attempted to register route %q under protected subtree %q", path, root))
			}
		}
		if strings.HasSuffix(path, "/") {
			prefixes = append(prefixes, path)
		}
	}

	sort.Slice(prefixes, func(i, j int) bool {
		return len(prefixes[i]) > len(prefixes[j])
	})

	return &pathOverridesHandler{
		m:        m.overrides,
		prefixes: prefixes,
		next:     next,
	}
}

func (m PathOverrides) Wrap(next http.Handler) http.Handler {
	return NewProtectedPathOverrides(m, []string{}).Wrap(next)
}

type pathOverridesHandler struct {
	m        PathOverrides
	prefixes []string
	next     http.Handler
}

func (h *pathOverridesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Exact match
	if handler, ok := h.m[path]; ok {
		handler.ServeHTTP(w, r)
		return
	}

	// do longest match first
	for _, prefix := range h.prefixes {
		if strings.HasPrefix(path, prefix) {
			h.m[prefix].ServeHTTP(w, r)
			return
		}
	}

	// Default to next handler
	h.next.ServeHTTP(w, r)
}

// ServerResponseHeader returns a middleware that renders the given sequence of
// server components (presumably in "software[/version]" format) and includes
// them in the Server response header.  Any secondary components which are
// supplied in addition to primary will be rendered in sequence and delimited
// by a single whitespace.  Any component which renders an empty string or one
// consisting solely of whitespace is ignored and other values will have
// leading and trailing whitespace trimmed.  ServerResponseHeader overwrites
// any Server header that was set earlier (by another middleware).
//
// ServerResponseHeader will panic immediately if the primary component does
// not contain a valid token (RFC2616).  It is recommended that the primary
// component be the result of ServerFixed called with a const, non-empty name
// argument.
//
// BUG:  Neither ServerResponseHeader nor its returned middleware check
// components for invalid control characters.  Because of this it is important
// that application end users and unchecked code not be permitted to inject
// content into server response header components.
func ServerResponseHeader(primary string, secondary ...func() string) Middleware {
	primary = strings.TrimSpace(primary)
	if primary == "" {
		panic("http server header primary component is invalid")
	}
	return Func(func(next http.Handler) http.Handler {
		return &serverListHandler{p: primary, s: secondary, next: next}
	})
}

type serverListHandler struct {
	p    string
	s    []func() string
	next http.Handler
}

func (h *serverListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s := h.header()
	// NOTE:  s cannot be empty in any allowed construction of h but we include
	// this branch which cannot panic just to protect against subtle future
	// bugs.
	if s == "" {
		// The RFC2616 grammar for Server dictates that it must contain a
		// nonempty token.  The application expects a Server header to be
		// injected here and we don't want to crash an inflexible http client
		// library by injecting an invalid header so we inject something
		// generic that is still valid according to the RFC.
		s = "server"
	}
	w.Header().Set("Server", s)
	h.next.ServeHTTP(w, r)
}

func (h *serverListHandler) header() string {
	if len(h.s) == 0 {
		return h.p
	}
	var b bytes.Buffer
	b.WriteString(h.p) // space has already been trimmed
	for i := range h.s {
		s := strings.TrimSpace(h.s[i]())
		if s != "" {
			b.WriteByte(' ')
			b.WriteString(h.s[i]())
		}
	}
	return b.String()
}

// ServerFixed returns a string indented to be used as the primary component in
// ServerResponseHeader.  ServerFixed ignores any leading and trailing
// whitespace in its arguments.  If version is non-empty the server header
// component will render the two strings joined by a slash, like the following:
//
//	fmt.Sprintf("%s/%s", name, version)
//
// The name argument of ServerFixed should be non-empty but that is not
// enforced.  If passed two empty strings ServerFixed will return an empty
// string.
func ServerFixed(name string, version string) string {
	if version == "" {
		return strings.TrimSpace(name)
	}
	return strings.TrimSpace(name) + "/" + strings.TrimSpace(version)
}

// ServerFixedFunc returns a function which can be used as a secondary
// component in ServerResponseHeader for cases where the software's name and
// version is known ahead of time.  The returned component is equivalent to the
// following function closure:
//
//	func() string {
//		return ServerFixed(name, version)
//	}
func ServerFixedFunc(name string, version string) func() string {
	fixed := ServerFixed(name, version)
	return func() string { return fixed }
}

// TraceHeaders ensures all incoming http requests have an identifying header
// for tracing and automatically includes a matching header in http responses.
// If allow is true then requests are allowed to specify their own ids which
// are assumed to be unique, otherwise any existing header will be overwritten
// before deferring to the inner http handler.  If header is the empty string
// then DefaultTraceHeader will contain the tracing identifier.
func TraceHeaders(header string, allow bool) Middleware {
	if header == "" {
		header = DefaultTraceHeader
	}
	return Func(func(next http.Handler) http.Handler {
		return &traceRequestHeader{
			header: header,
			allow:  allow,
			next:   next,
		}
	})
}

type traceRequestHeader struct {
	header string
	allow  bool
	next   http.Handler
}

func (h *traceRequestHeader) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var reqid string
	precedenceHeaders := []string{DefaultTraceHeader, DefaultAzureHeader, DefaultAWSHeader}

	if h.allow {
		reqid = r.Header.Get(h.header)
		for _, header := range precedenceHeaders {
			headerValue := r.Header.Get(header)

			if headerValue != "" {
				h.header = header
				reqid = headerValue
				break
			}
		}
	}
	if reqid == "" {
		reqid = uuid.New().String()
		r.Header.Set(h.header, reqid)
	}

	// Always set DefaultTraceHeader on request and response since a lot of
	// logging is hard-coded to use this header.
	if h.header != DefaultTraceHeader {
		r.Header.Set(DefaultTraceHeader, reqid)
		w.Header().Set(DefaultTraceHeader, reqid)
	}

	w.Header().Set(h.header, reqid)
	h.next.ServeHTTP(w, r)
}
