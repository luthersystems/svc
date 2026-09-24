// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package oracle

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// withForwardedValue returns a context carrying the gRPC header metadata the
// gateway would hand a ForwardResponseOption after the server set val.
func withForwardedValue(cf *CookieForwarder, val string) context.Context {
	md := runtime.ServerMetadata{HeaderMD: metadata.Pairs(cf.header, val)}
	return runtime.NewServerMetadataContext(context.Background(), md)
}

// TestCookieForwarderAttributes pins the Set-Cookie attributes to the
// AddCookieForwarder arguments. HttpOnly was previously hardcoded to true, so
// the httpOnly argument was silently ignored.
func TestCookieForwarderAttributes(t *testing.T) {
	tests := []struct {
		name         string
		maxAge       int
		secure       bool
		httpOnly     bool
		wantSameSite http.SameSite // 0 = no SameSite attribute emitted
	}{
		{name: "secure httpOnly", maxAge: 3600, secure: true, httpOnly: true, wantSameSite: http.SameSiteNoneMode},
		{name: "secure not httpOnly", maxAge: 3600, secure: true, httpOnly: false, wantSameSite: http.SameSiteNoneMode},
		{name: "insecure httpOnly", maxAge: 60, secure: false, httpOnly: true, wantSameSite: 0},
		{name: "insecure not httpOnly", maxAge: 60, secure: false, httpOnly: false, wantSameSite: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{}
			cf := cfg.AddCookieForwarder("myCookie", tt.maxAge, tt.secure, tt.httpOnly)
			require.NotNil(t, cf)

			rec := httptest.NewRecorder()
			err := cf.forwardResponseOption()(withForwardedValue(cf, "cookie-value"), rec, nil)
			require.NoError(t, err)

			rawHeaders := rec.Result().Header.Values("Set-Cookie")
			require.Len(t, rawHeaders, 1)
			raw := rawHeaders[0]

			cookies := rec.Result().Cookies()
			require.Len(t, cookies, 1)
			c := cookies[0]

			assert.Equal(t, "myCookie", c.Name)
			assert.Equal(t, "cookie-value", c.Value)
			assert.Equal(t, tt.maxAge, c.MaxAge)
			assert.Equal(t, "/", c.Path)
			assert.Equal(t, tt.httpOnly, c.HttpOnly, "HttpOnly must follow the httpOnly arg: %s", raw)
			assert.Equal(t, tt.secure, c.Secure, "Secure must follow the secure arg: %s", raw)
			assert.Equal(t, tt.wantSameSite, c.SameSite, "SameSite: %s", raw)

			// Check the wire format too, independent of net/http's parser.
			assert.Equal(t, tt.httpOnly, strings.Contains(raw, "HttpOnly"), raw)
			assert.Equal(t, tt.secure, strings.Contains(raw, "Secure"), raw)
		})
	}
}

// TestCookieForwarderNoValue confirms no cookie is written when the server
// never set a value.
func TestCookieForwarderNoValue(t *testing.T) {
	cfg := &Config{}
	cf := cfg.AddCookieForwarder("myCookie", 3600, true, true)

	rec := httptest.NewRecorder()
	err := cf.forwardResponseOption()(context.Background(), rec, nil)
	require.NoError(t, err)
	assert.Empty(t, rec.Result().Header.Values("Set-Cookie"))
}
