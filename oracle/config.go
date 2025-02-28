package oracle

import (
	"fmt"
	"net/http"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/luthersystems/svc/opttrace"
)

// DefaultConfig returns a default config.
func DefaultConfig() *Config {
	return &Config{
		Verbose:   true,
		EmulateCC: false,
		// IMPORTANT: Phylum bootstrap expects ListenAddress on :8080 for
		// FakeAuth IDP. Only change this if you know what you're doing!
		ListenAddress:     ":8080",
		PhylumPath:        "./phylum",
		PhylumServiceName: "phylum",
		ServiceName:       "oracle",
		RequestIDHeader:   "X-Request-ID",
		Version:           "v0.0.1",
	}
}

// Config configures an oracle.
type Config struct {
	// swaggerHandler configures an endpoint to serve the
	// swagger API.
	swaggerHandler http.Handler
	// ListenAddress is an address the oracle HTTP listens on.
	ListenAddress string `yaml:"listen-address"`
	// PhylumPath is the the path for the business logic.
	PhylumPath string `yaml:"phylum-path"`
	// GatewayEndpoint is an address to the shiroclient gateway.
	GatewayEndpoint string `yaml:"gateway-endpoint"`
	// PhylumServiceName is the app-specific name of the conneted phylum.
	PhylumServiceName string `yaml:"phylum-service-name"`
	// ServiceName is the app-specific name of the Oracle.
	ServiceName string `yaml:"service-name"`
	// RequestIDHeader is the HTTP header encoding the request ID.
	RequestIDHeader string `yaml:"request-id-header"`
	// Version is the oracle version.
	Version string `yaml:"version"`
	// TraceOpts are tracing options.
	TraceOpts []opttrace.Option
	// Verbose increases logging.
	Verbose bool `yaml:"verbose"`
	// EmulateCC emulates chaincode in memory (for testing).
	EmulateCC bool `yaml:"emulate-cc"`
	// gatewayOpts configures the grpc gateway.
	gatewayOpts []runtime.ServeMuxOption
	// ForwardedHeaders are user-defined HTTP headers that the gateway passes to the app.
	ForwardedHeaders []string
	// DependentTxCookie sets dependent transaction ID on a cookie.
	DependentTxCookie string `yaml:"dependent-tx-cookie"`
	// InsecureCookies
	InsecureCookies bool `yaml:"insecure-cookies"`
}

const (
	dependentTxCookieMaxAge  = 5 * time.Minute
	dependentTxSecureCookie  = true
	grpcMetadataCookiePrefix = "luther-cookie-"
	grpcMetadataHeaderPrefix = "luther-header-"
)

// SetSwaggerHandler configures an endpoint to serve the swagger API.
func (c *Config) SetSwaggerHandler(h http.Handler) {
	if c == nil {
		return
	}
	c.swaggerHandler = h
}

// SetOTLPEndpoint is a helper to set the OTLP trace endpoint.
func (c *Config) SetOTLPEndpoint(endpoint string) {
	if c == nil || endpoint == "" {
		return
	}
	c.TraceOpts = append(c.TraceOpts, opttrace.WithOTLPExporter(endpoint))
}

// Valid validates an oracle configuration.
func (c *Config) Valid() error {
	if c == nil {
		return fmt.Errorf("missing phylum config")
	}
	if c.ListenAddress == "" {
		return fmt.Errorf("missing listen address")
	}
	if c.PhylumPath == "" {
		return fmt.Errorf("missing phylum path")
	}
	if c.PhylumServiceName == "" {
		return fmt.Errorf("missing phylum service name")
	}
	if c.ServiceName == "" {
		return fmt.Errorf("missing service name")
	}
	if c.RequestIDHeader == "" {
		return fmt.Errorf("missing request ID header")
	}
	if c.Version == "" {
		return fmt.Errorf("missing version")
	}
	return nil
}

// addGRPCGatewayOption is a low-level helper method to accumulate ServeMux
// options without directly modifying the GatewayOptions slice.
// *IMPORTANT*: Only use this if you know what you're doing.
func (c *Config) addGRPCGatewayOptions(opt ...runtime.ServeMuxOption) {
	c.gatewayOpts = append(c.gatewayOpts, opt...)
}

// AddCookieForwarder configures a bridge from a gRPC metadata key to an HTTP
// response cookie. The returned CookieForwarder can be used within your gRPC
// server methods to set the cookie value by calling its SetValue(ctx, val)
// method. That value will then appear as an HTTP cookie named cookieName in the
// final HTTP response.
func (c *Config) AddCookieForwarder(cookieName string, maxAge int, secure, httpOnly bool) *CookieForwarder {
	grpcKey := grpcMetadataCookiePrefix + cookieName
	cf := newCookieForwarder(grpcKey, cookieName, maxAge, secure, httpOnly)
	c.addGRPCGatewayOptions(
		runtime.WithForwardResponseOption(cf.forwardResponseOption()),
	)
	return cf
}

// AddHeaderForwarder configures a bridge from a gRPC metadata key to an HTTP
// response header. The returned HeaderForwarder can be used within your gRPC
// server methods to set the header value by calling its SetValue(ctx, val)
// method. That value will then appear as an HTTP response header of name
// httpHeaderName in the final HTTP response.
func (c *Config) AddHeaderForwarder(httpHeaderName string) *HeaderForwarder {
	grpcKey := grpcMetadataHeaderPrefix + httpHeaderName
	hf := newHeaderForwarder(grpcKey, httpHeaderName)
	c.ForwardedHeaders = append(c.ForwardedHeaders, httpHeaderName)
	c.addGRPCGatewayOptions(
		runtime.WithForwardResponseOption(hf.forwardResponseOption()),
	)
	return hf
}
