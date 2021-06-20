package midware

import "net/http"

// Middleware defines the basic pattern for http middleware.  Middleware can be
// thought of as a configuration for which the Wrap method produces an instance
// of the configured middleware.
type Middleware interface {
	// Wrap returns an intermediate http handler which may handle requests
	// itself or invoke next to serve the request (performing intermediate
	// processing including modification of requests or outgoing data.
	Wrap(next http.Handler) http.Handler
}

// Chain is an ordered sequence of middleware which itself acts as a compound
// middleware.  A chain defines middlewares the order in which middleware are
// applied, from the outermost http handler (reading the raw http transport) to
// the innermost handler (just prior to the application serving the request)
//
// Chain can be a helpful abstraction because it tends to match a person's
// intuition about ordering middleware and reduce confusion.  The Middleware
// interface otherwise forces middleware handlers to be bound to the
// application in the opposite order from which they are applied.
type Chain []Middleware

// Wrap implements the Middleware interface.
func (c Chain) Wrap(h http.Handler) http.Handler {
	for i := len(c) - 1; i >= 0; i-- {
		h = c[i].Wrap(h)
	}
	return h
}

// Func is a function that acts as middleware.  Typically third-party
// middleware will need to be wrapped as a Func before they may be used in a
// Chain.
type Func func(http.Handler) http.Handler

// Wrap implements the Middleware interface.
func (fn Func) Wrap(h http.Handler) http.Handler {
	return fn(h)
}
