// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package oracle

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	jwtgo "github.com/golang-jwt/jwt/v4"
	"github.com/luthersystems/lutherauth-sdk-go/jwt"
)

// CSRFTokenHeader is the HTTP request header that carries the CSRF token in
// the double-submit check. lutherauth sets the token as a cookie that page
// script can read (ui_csrf_token, when AUTH_OAUTH_PROTECT_CSRF is on), and
// the frontend sends its value back in this header.
const CSRFTokenHeader = "X-CSRF-Token" //nolint:gosec // G101: a header name, not a credential

// csrfProtect makes the CSRF double-submit check (Config.CheckCSRFToken)
// before next serves the request. It refuses with 403, not 401: the session
// is valid, so a frontend must not treat the refusal as an expired login.
func csrfProtect(cookieName string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !csrfDoubleSubmitOK(r, cookieName) {
			http.Error(w, "CSRF check failed: the "+CSRFTokenHeader+" header does not match the session", http.StatusForbidden)
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
	token, ok := authCookieValue(r, cookieName)
	if !ok {
		return true
	}
	nonce := unverifiedNonce(token)
	if nonce == "" {
		return true
	}
	// Hashing first makes the comparison constant time whatever the lengths.
	got := sha256.Sum256([]byte(r.Header.Get(CSRFTokenHeader)))
	want := sha256.Sum256([]byte(nonce))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

// authCookieValue returns the auth cookie as CookieForwarder.GetValue reads
// it from the forwarded metadata: from the first Cookie header, the first
// cookie whose name matches, ignoring case.
func authCookieValue(r *http.Request, name string) (string, bool) {
	headers := r.Header.Values("Cookie")
	if len(headers) == 0 {
		return "", false
	}
	first := http.Request{Header: http.Header{"Cookie": headers[:1]}}
	for _, c := range first.Cookies() {
		if strings.EqualFold(c.Name, name) {
			return c.Value, true
		}
	}
	return "", false
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
