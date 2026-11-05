// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package oracle

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
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
	nonce, ok := unverifiedNonce(token)
	if !ok {
		return false
	}
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

// unverifiedNonce reads the nonce claim of a JWT without verifying it. A
// token whose payload does not decode has no claims the verifier could
// accept, so it reports an empty nonce and leaves the refusal to the
// verifier. It reports false only for a nonce claim that is not a string,
// which fails closed.
func unverifiedNonce(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", true
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", true
	}
	// Decode as golang-jwt does into the SDK's claims struct (a json.Decoder,
	// which ignores data after the first value, into a struct field, which
	// matches the key ignoring case), so that this reads the nonce the
	// verifier reads.
	var fields struct {
		Nonce json.RawMessage `json:"nonce"`
	}
	if err := json.NewDecoder(bytes.NewReader(payload)).Decode(&fields); err != nil {
		return "", true
	}
	if fields.Nonce == nil {
		return "", true
	}
	var nonce string
	if err := json.Unmarshal(fields.Nonce, &nonce); err != nil {
		return "", false
	}
	return nonce, true
}
