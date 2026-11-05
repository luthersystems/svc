// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package oracle

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/metadata"
)

// CSRFTokenHeader is the HTTP request header that carries the CSRF token in
// the double-submit check. lutherauth sets the token as a cookie that page
// script can read (ui_csrf_token, when AUTH_OAUTH_PROTECT_CSRF is on), and
// the frontend sends its value back in this header.
const CSRFTokenHeader = "X-CSRF-Token" //nolint:gosec // G101: a header name, not a credential

// errMissingCSRFToken is returned when a request has no CSRF token header.
var errMissingCSRFToken = errors.New("missing CSRF token header")

// csrfTokenFromMetadata reads the CSRF token header from the incoming gRPC
// metadata. The gateway puts it there only when Config.CheckCSRFToken is on.
func csrfTokenFromMetadata(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", errMissingCSRFToken
	}
	vals := md.Get(strings.ToLower(CSRFTokenHeader))
	if len(vals) == 0 {
		return "", errMissingCSRFToken
	}
	return vals[0], nil
}
