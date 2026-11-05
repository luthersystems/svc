// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package oracle

import (
	"context"
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
