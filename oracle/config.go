package oracle

import (
	"errors"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/luthersystems/lutherauth-sdk-go/jwk"
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
	// PhylumPath is the the path for the business logic when testing.
	PhylumPath string `yaml:"phylum-path"`
	// TODO: PhylumConfigPath is the the path for the bootstrap yaml for when testing.
	PhylumConfigPath string `yaml:"phylum-config-path"`
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
	// InsecureCookies
	InsecureCookies bool `yaml:"insecure-cookies"`
	// extraJWKOptions has additional configuration for JWK claims.
	extraJWKOptions []jwk.Option
	// stopFns are functions that are called when the service stops.
	stopFns []func()
	// authCookieForwarder sets auth cokoies
	authCookieForwarder *CookieForwarder
	// depTxForwarder sets dep tx cokoies
	depTxForwarder *CookieForwarder
	// fakeIDP is for testing auth.
	fakeIDP *FakeIDP
}

const (
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
		return errors.New("missing oracle config")
	}
	if c.ListenAddress == "" {
		return errors.New("missing listen address")
	}
	if c.PhylumPath == "" {
		return errors.New("missing phylum path")
	}
	if c.PhylumServiceName == "" {
		return errors.New("missing phylum service name")
	}
	if c.ServiceName == "" {
		return errors.New("missing service name")
	}
	if c.RequestIDHeader == "" {
		return errors.New("missing request ID header")
	}
	if c.Version == "" {
		return errors.New("missing version")
	}
	return nil
}

// addGRPCGatewayOption is a low-level helper method to accumulate ServeMux
// options without directly modifying the GatewayOptions slice.
// *IMPORTANT*: Only use this if you know what you're doing.
func (c *Config) addGRPCGatewayOptions(opt ...runtime.ServeMuxOption) {
	if c == nil {
		return
	}
	c.gatewayOpts = append(c.gatewayOpts, opt...)
}

// AddCookieForwarder configures a bridge from a gRPC metadata key to an HTTP
// response cookie. The returned CookieForwarder can be used within your gRPC
// server methods to set the cookie value by calling its SetValue(ctx, val)
// method. That value will then appear as an HTTP cookie named cookieName in the
// final HTTP response.
func (c *Config) AddCookieForwarder(cookieName string, maxAge int, secure, httpOnly bool) *CookieForwarder {
	if c == nil {
		return nil
	}
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
	if c == nil {
		return nil
	}
	grpcKey := grpcMetadataHeaderPrefix + httpHeaderName
	hf := newHeaderForwarder(grpcKey, httpHeaderName)
	c.ForwardedHeaders = append(c.ForwardedHeaders, httpHeaderName)
	c.addGRPCGatewayOptions(
		runtime.WithForwardResponseOption(hf.forwardResponseOption()),
	)
	return hf
}

// WithJWKOption adds auth options.
func (c *Config) AddJWKOptions(opt ...jwk.Option) {
	if c == nil {
		return
	}
	c.extraJWKOptions = append(c.extraJWKOptions, opt...)
}

// AddAuthCookieForwarder adds cookie authentication.
func (c *Config) AddAuthCookieForwarder(cookieName string, maxAge int, secure, httpOnly bool) *CookieForwarder {
	if c == nil {
		return nil
	}
	authForwarder := c.AddCookieForwarder(cookieName, maxAge, secure, httpOnly)
	c.authCookieForwarder = authForwarder
	return authForwarder
}

// AddDepTxCookieForwarder adds dependent transaction cookie..
func (c *Config) AddDepTxCookieForwarder(cookieName string, maxAge int, secure, httpOnly bool) *CookieForwarder {
	if c == nil {
		return nil
	}
	depTxForwarder := c.AddCookieForwarder(cookieName, maxAge, secure, httpOnly)
	c.depTxForwarder = depTxForwarder
	return depTxForwarder
}
