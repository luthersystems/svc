/*
Package midware defines an interface for the common http middleware pattern and
provides functionality for sensibly chaining middleware to create complex http
handlers.  In addition to defining abstractions related to the interface the
package provides a small collection of middleware implementations.  Many more
exist in third-party packages which can be found by searching for "http
middleware" on https://pkg.go.dev.

For example, automatic gzip compression of response bodies (if supported by the
receiving client) can be found in package github.com/NYTimes/gziphandler.  The
middleware from the gziphandler package can be composed with middleware from
this package using the Func and Chain types.

	middleware := midware.Chain{
		// the gzip handler is first in the chain because it has the highest
		// priority, it will see the incoming request first and the last
		// middleware to touch the response body which is particularly
		// important here.
		midware.Func(gziphandler.GzipHandler),
		midware.TraceHeaders("", false),
		// Because of its placement here the path override handler will see
		// request tracing headers and any response body it serves will be
		// compressed by the outermost gzip middleware.
		midware.PathOverrides{
			"/override": overrideHandler,
		},
	}
	http.ListenAndServe(":8080", middleware.Wrap(myapp))
*/
package midware
