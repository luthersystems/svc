// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package oracle

import (
	"context"

	"github.com/luthersystems/lutherauth-sdk-go/claims"
	"github.com/luthersystems/lutherauth-sdk-go/jwt"
)

// CSRFTokenHeader is the HTTP request header that carries the CSRF token in
// the double-submit check. lutherauth sets the token as a cookie that page
// script can read (ui_csrf_token, when AUTH_OAUTH_PROTECT_CSRF is on), and
// the frontend sends its value back in this header.
const CSRFTokenHeader = "X-CSRF-Token" //nolint:gosec // G101: a header name, not a credential

// csrfTokenFromMetadata reads the CSRF token header from the incoming gRPC
// metadata. The gateway puts it there only when Config.CheckCSRFToken is on.
// A missing header reads as "", which never equals a nonce, so the SDK
// refuses the token.
func csrfTokenFromMetadata(ctx context.Context) (string, error) {
	return GetIncomingHeader(ctx, CSRFTokenHeader), nil
}

// csrfClaims verifies the auth token, and makes the CSRF check only when the
// token came from the cookie the client sent. A token the server wrote with
// SetValue earlier in the same request (a login) is not compared with the
// request's header, which belongs to the old session.
type csrfClaims struct {
	// checked verifies the token and makes the CSRF check.
	checked claims.Getter
	// unchecked verifies the token without the CSRF check.
	unchecked claims.Getter
	// exempt reports whether a request skips the CSRF check.
	exempt func(ctx context.Context) bool
}

// Claims implements claims.Getter.
func (c *csrfClaims) Claims(ctx context.Context) (*jwt.Claims, error) {
	if c.exempt(ctx) {
		return c.unchecked.Claims(ctx)
	}
	return c.checked.Claims(ctx)
}
