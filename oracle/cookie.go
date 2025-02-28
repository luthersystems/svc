package oracle

import (
	"context"
	"net/http"

	"google.golang.org/protobuf/proto"
)

// CookieForwarder holds all the parameters for bridging a gRPC header
// into an HTTP cookie via the gRPC-Gateway.
type CookieForwarder struct {
	// The gRPC/HTTP header used to store the cookie’s value in metadata.
	header string

	// The actual "Set-Cookie" name you want in the HTTP response.
	cookieName string

	// The cookie’s max age in seconds.
	maxAge int

	// Whether to mark the cookie as “secure” (i.e. HTTPS-only).
	secure bool

	// Whether to mark the cookie as “httpOnly”.
	// Typically “true” if you don’t want JS to read it.
	httpOnly bool
}

// newCookieForwarder constructs a forwarder for a particular cookie name/header.
func newCookieForwarder(header, cookieName string, maxAge int, secure, httpOnly bool) *CookieForwarder {
	return &CookieForwarder{
		header:     header,
		cookieName: cookieName,
		maxAge:     maxAge,
		secure:     secure,
		httpOnly:   httpOnly,
	}
}

// SetCookie sets the given value into gRPC metadata with the
// forwarder's configured header. The gRPC-Gateway can then turn it into a cookie.
func (cf *CookieForwarder) SetValue(ctx context.Context, val string) {
	setGRPCHeader(ctx, cf.header, val)
}

func cookieHandler(header string, name string, maxAge int, secureCookie bool) func(context.Context, http.ResponseWriter, proto.Message) error {
	return func(ctx context.Context, w http.ResponseWriter, resp proto.Message) error {
		value := getGRPCHeader(ctx, header)
		if value == "" {
			return nil
		}

		cookie := &http.Cookie{
			Name:     name,
			Value:    value,
			MaxAge:   maxAge,
			Secure:   secureCookie,
			HttpOnly: true,
			Path:     "/",
		}
		if secureCookie {
			cookie.SameSite = http.SameSiteNoneMode
		}

		http.SetCookie(w, cookie)

		return nil
	}
}

// ForwardResponseOption returns a gRPC-Gateway ForwardResponseOption that reads
// the forwarder’s header from metadata and writes it as a Set-Cookie in HTTP.
func (cf *CookieForwarder) forwardResponseOption() func(ctx context.Context, w http.ResponseWriter, resp proto.Message) error {
	return cookieHandler(cf.header, cf.cookieName, cf.maxAge, cf.secure)
}
