package oracle

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/grpc/metadata"
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

func cookieHandler(grpcHeader string, cookieName string, maxAge int, secureCookie bool) func(context.Context, http.ResponseWriter, proto.Message) error {
	return func(ctx context.Context, w http.ResponseWriter, resp proto.Message) error {
		value := getGRPCHeader(ctx, grpcHeader)
		fmt.Printf("WTF: in cookie handler for: grpcHeader=[%s] cookieName=[%s] val=[%s]\n", grpcHeader, cookieName, value)
		if value == "" {
			return nil
		}

		cookie := &http.Cookie{
			Name:     cookieName,
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

// getIncomingCookie retrieves the named cookie from the gRPC metadata that
// the gRPC-Gateway sets after parsing the original HTTP Cookie header.
func getIncomingCookie(ctx context.Context, cookieName string) (*http.Cookie, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, errors.New("missing metadata")
	}
	cookies := md.Get(cookieName)
	if len(cookies) < 1 {
		return nil, fmt.Errorf("missing cookie header: %s", cookieName)
	}
	// Usually there's exactly one 'cookie' string, e.g. "k1=v1; k2=v2"
	// but you can handle multiple if needed.
	rawCookieHeader := cookies[0]

	// We can parse the cookie using net/http logic:
	header := http.Header{}
	header.Add("Cookie", rawCookieHeader)
	request := http.Request{Header: header}
	allCookies := request.Cookies()

	var found *http.Cookie
	for _, c := range allCookies {
		if strings.EqualFold(c.Name, cookieName) {
			found = c
			break
		}
	}
	if found == nil {
		return nil, errors.New("cookie not found in metadata")
	}
	return found, nil
}

// GetCookie is just a small helper that fetches a cookie and returns
// its value as a string token.
func GetCookie(ctx context.Context, cookieName string) (string, error) {
	c, err := getIncomingCookie(ctx, cookieName)
	if err != nil {
		return "", err
	}
	return c.Value, nil
}
