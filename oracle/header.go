package oracle

import (
	"context"
	"net/http"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

// HeaderForwarder holds parameters for bridging
// a single gRPC metadata key → one HTTP response header.
type HeaderForwarder struct {
	grpcHeaderKey  string
	httpHeaderName string
}

// newHeaderForwarder is a private constructor function, to ensure uniform usage
func newHeaderForwarder(grpcKey, httpHeaderName string) *HeaderForwarder {
	return &HeaderForwarder{
		grpcHeaderKey:  grpcKey,
		httpHeaderName: httpHeaderName,
	}
}

// SetValue places the given value in the gRPC metadata under the grpcHeaderKey.
// The forwardResponseOption method will later turn it into an HTTP header.
func (hf *HeaderForwarder) SetValue(ctx context.Context, val string) {
	setGRPCHeader(ctx, hf.grpcHeaderKey, val)
}

// GetValue retrieves the header.
func (hf *HeaderForwarder) GetValue(ctx context.Context) (string, error) {
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
