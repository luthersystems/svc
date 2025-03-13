package oracle

import (
	"context"
	"errors"
	"net/http"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

// headerContextKey is used as a private type to avoid key collisions.
type headerContextKey struct{}

// HeaderForwarder holds parameters for bridging
// a single gRPC metadata key → one HTTP response header.
type HeaderForwarder struct {
	grpcHeaderKey  string
	httpHeaderName string
}

// newHeaderForwarder is a private constructor function, to ensure uniform usage.
func newHeaderForwarder(grpcKey, httpHeaderName string) *HeaderForwarder {
	return &HeaderForwarder{
		grpcHeaderKey:  grpcKey,
		httpHeaderName: httpHeaderName,
	}
}

// SetValue places the given value in the gRPC metadata under the grpcHeaderKey
// and also caches it in the context for "read your own write" behavior.
func (hf *HeaderForwarder) SetValue(ctx context.Context, val string) context.Context {
	if hf == nil {
		return ctx
	}
	setGRPCHeader(ctx, hf.grpcHeaderKey, val)
	return context.WithValue(ctx, headerContextKey{}, val)
}

// GetValue retrieves the header.
// It first returns the cached value (if set) to support "read your own writes",
// and falls back to reading the incoming header otherwise.
func (hf *HeaderForwarder) GetValue(ctx context.Context) (string, error) {
	if hf == nil {
		return "", errors.New("nil header forwarder")
	}

	if val, ok := ctx.Value(headerContextKey{}).(string); ok && val != "" {
		return val, nil
	}

	return GetIncomingHeader(ctx, hf.httpHeaderName), nil
}

// forwardResponseOption returns a gRPC-Gateway ForwardResponseOption that reads
// the hf.grpcHeaderKey in metadata and writes the hf.httpHeaderName header in
// the HTTP response.
func (hf *HeaderForwarder) forwardResponseOption() func(ctx context.Context, w http.ResponseWriter, resp proto.Message) error {
	return func(ctx context.Context, w http.ResponseWriter, _ proto.Message) error {
		value := getGRPCHeader(ctx, hf.grpcHeaderKey)
		if value == "" {
			return nil
		}
		w.Header().Set(hf.httpHeaderName, value)
		return nil
	}
}

// GetIncomingHeader returns the first value of a specific metadata key from
// the incoming gRPC context, or an empty string if not found.
func GetIncomingHeader(ctx context.Context, key string) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get(key)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}
