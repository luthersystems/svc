// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package oracle

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"

	jwtgo "github.com/golang-jwt/jwt/v4"
	"github.com/luthersystems/lutherauth-sdk-go/jwt"
	"github.com/luthersystems/svc/grpclogging"
	"github.com/luthersystems/svc/midware"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CSRFTokenHeader is the HTTP request header that carries the CSRF token in
// the double-submit check. lutherauth sets the token as a cookie that page
// script can read (ui_csrf_token, when AUTH_OAUTH_PROTECT_CSRF is on), and
// the frontend sends its value back in this header.
const CSRFTokenHeader = "X-CSRF-Token" //nolint:gosec // G101: a header name, not a credential

// csrfProtect makes the CSRF double-submit check (Config.CheckCSRFToken)
// before next serves the request. It refuses with 403 PermissionDenied, not
// 401: the session is valid, so a frontend must not treat the refusal as an
// expired login. The response body is the gateway's JSON error.
func (orc *Oracle) csrfProtect(next http.Handler) http.Handler {
	cookieName := orc.cfg.authCookieForwarder.cookieName
	marshaler := orc.jsonMarshaler()
	errorHandler := orc.gatewayErrorHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !csrfDoubleSubmitOK(r, cookieName) {
			ctx := grpclogging.NewContext(r.Context())
			grpclogging.AddLogrusFields(ctx, logrus.Fields{
				"req_id": orc.requestID(r),
				"method": r.Method,
				"path":   r.URL.Path,
			})
			// No token or header value is logged.
			orc.Log(ctx).Warn("CSRF check refused a request: the " + CSRFTokenHeader +
				" header does not match the auth cookie's nonce")
			err := status.Error(codes.PermissionDenied,
				"CSRF check failed: the "+CSRFTokenHeader+" header does not match the session")
			errorHandler(ctx, nil, marshaler, w, r, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// csrfDoubleSubmitOK reports whether a request passes the double-submit
// check. GET, HEAD and OPTIONS pass: they change nothing, and a top-level
// navigation cannot send the header. On any other method, a request whose
// auth cookie holds a token with a nonce claim must send that nonce in the
// X-CSRF-Token header. A request with an Authorization header is checked
// too, because the oracle reads the token from the cookie even then.
func csrfDoubleSubmitOK(r *http.Request, cookieName string) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	// Check every token a route can read: a gateway route reads the Cookie
	// headers the gateway forwards, and a route added with HandlePath may
	// read the raw first Cookie header.
	header := r.Header.Get(CSRFTokenHeader)
	cookies := r.Header.Values("Cookie")
	for _, values := range [][]string{gatewayHeaderValues(cookies), cookies} {
		c, ok := findCookie(values, cookieName)
		if ok && !nonceMatches(c.Value, header) {
			return false
		}
	}
	return true
}

// nonceMatches reports whether a token has no nonce claim or header equals
// it.
func nonceMatches(token, header string) bool {
	nonce := unverifiedNonce(token)
	if nonce == "" {
		return true
	}
	// Hashing first makes the comparison constant time whatever the lengths.
	got := sha256.Sum256([]byte(header))
	want := sha256.Sum256([]byte(nonce))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

// unverifiedNonce reads the nonce claim of a JWT without verifying it. It
// decodes the token as the SDK's verifier does (golang-jwt, into the SDK's
// claims struct), so that it reads the nonce the verifier reads. A token
// that does not decode reads as having no nonce: the verifier decodes it
// the same way first, so it refuses that token.
func unverifiedNonce(token string) string {
	var claims jwt.Claims
	if _, _, err := jwtgo.NewParser().ParseUnverified(token, &claims); err != nil {
		return ""
	}
	return claims.Nonce
}

// requestID is the request ID the trace middleware (midware.TraceHeaders)
// put on the request.
func (orc *Oracle) requestID(r *http.Request) string {
	if id := r.Header.Get(orc.cfg.RequestIDHeader); id != "" {
		return id
	}
	return r.Header.Get(midware.DefaultTraceHeader)
}
