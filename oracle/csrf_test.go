// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package oracle

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/luthersystems/lutherauth-sdk-go/jwt"
	hellov1 "github.com/luthersystems/svc/oracle/testservice/gen/go/proto/hello/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

const testNonce = "0123456789abcdef0123456789abcdef"

func newCSRFTestOracle(t *testing.T, checkCSRF bool) *Oracle {
	t.Helper()
	cfg := newTestConfig(t)
	cfg.CheckCSRFToken = checkCSRF
	orc, closeFn := NewTestOracle(t, cfg)
	t.Cleanup(closeFn)
	return orc
}

func testClaims(nonce string) *jwt.Claims {
	c := jwt.NewClaims("sam@luther.systems", "luther:auth:svc-local", "lutherapp:svc")
	c.Nonce = nonce
	return c
}

// cookieContext is the context the gateway gives the gRPC server for a
// browser request: the auth token in the Cookie header and, when csrf is
// non-nil, the X-CSRF-Token header.
func cookieContext(t *testing.T, token string, csrf *string) context.Context {
	t.Helper()
	md := metadata.Pairs("cookie", "svc_authorization="+token)
	if csrf != nil {
		md.Append(CSRFTokenHeader, *csrf)
	}
	return metadata.NewIncomingContext(MakeTestContext(t), md)
}

func ptr(s string) *string { return &s }

func TestCSRFCheck(t *testing.T) {
	on := newCSRFTestOracle(t, true)
	off := newCSRFTestOracle(t, false)

	tests := []struct {
		name     string
		orc      *Oracle
		nonce    string
		header   *string
		wantCode codes.Code
	}{
		{"on, nonce, no header", on, testNonce, nil, codes.Unauthenticated},
		{"on, nonce, matching header", on, testNonce, ptr(testNonce), codes.OK},
		{"on, nonce, wrong header", on, testNonce, ptr("ffff" + testNonce[4:]), codes.Unauthenticated},
		{"on, nonce, empty header", on, testNonce, ptr(""), codes.Unauthenticated},
		{"on, no nonce, no header", on, "", nil, codes.OK},
		{"on, no nonce, any header", on, "", ptr("anything"), codes.OK},
		{"off, nonce, no header", off, testNonce, nil, codes.OK},
		{"off, nonce, wrong header", off, testNonce, ptr("wrong"), codes.OK},
		{"off, no nonce, no header", off, "", nil, codes.OK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token, err := tc.orc.cfg.fakeIDP.MakeFakeIDPAuthToken(testClaims(tc.nonce))
			require.NoError(t, err)
			got, err := tc.orc.GetClaims(cookieContext(t, token, tc.header))
			require.Equal(t, tc.wantCode, status.Code(err), "error: %v", err)
			if tc.wantCode == codes.OK {
				require.Equal(t, "sam@luther.systems", got.Subject)
			}
		})
	}
}

func TestCSRFForwardedHeader(t *testing.T) {
	on := newCSRFTestOracle(t, true)
	off := newCSRFTestOracle(t, false)

	_, ok := on.incomingHeaderMatcher("X-Csrf-Token")
	require.True(t, ok, "check on: the gateway forwards the CSRF header")
	_, ok = off.incomingHeaderMatcher("X-Csrf-Token")
	require.False(t, ok, "check off: the gateway does not forward the CSRF header")
	require.Len(t, on.gatewayForwardedHeaders(), len(off.gatewayForwardedHeaders())+1)
}

func TestMakeTestAuthContextSendsCSRFHeader(t *testing.T) {
	on := newCSRFTestOracle(t, true)
	ctx := on.MakeTestAuthContext(t, testClaims(testNonce))
	claims, err := on.GetClaims(ctx)
	require.NoError(t, err)
	require.Equal(t, testNonce, claims.Nonce)
}

// claimsHelloServer answers Ping with the result of the oracle's claims check.
type claimsHelloServer struct {
	hellov1.UnimplementedHelloServiceServer
	orc *Oracle
}

func (s *claimsHelloServer) Ping(ctx context.Context, _ *emptypb.Empty) (*emptypb.Empty, error) {
	if _, err := s.orc.GetClaims(ctx); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// TestCSRFCheckThroughGateway sends HTTP requests through the oracle's
// gateway mux, so it checks that the header reaches the claims check.
func TestCSRFCheckThroughGateway(t *testing.T) {
	for _, checkCSRF := range []bool{true, false} {
		orc := newCSRFTestOracle(t, checkCSRF)

		grpcServer := grpc.NewServer()
		hellov1.RegisterHelloServiceServer(grpcServer, &claimsHelloServer{orc: orc})
		grpcLis, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)
		go func() { _ = grpcServer.Serve(grpcLis) }()
		t.Cleanup(grpcServer.Stop)

		conn, err := grpc.NewClient(grpcLis.Addr().String(),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })

		mux := orc.grpcGatewayMux()
		require.NoError(t, hellov1.RegisterHelloServiceHandler(t.Context(), mux, conn))
		gwLis, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)
		gwSrv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go func() { _ = gwSrv.Serve(gwLis) }()
		t.Cleanup(func() { _ = gwSrv.Close() })

		ping := func(t *testing.T, nonce string, csrf *string) int {
			t.Helper()
			token, err := orc.cfg.fakeIDP.MakeFakeIDPAuthToken(testClaims(nonce))
			require.NoError(t, err)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
				"http://"+gwLis.Addr().String()+"/v1/ping", nil)
			require.NoError(t, err)
			req.Header.Set("Cookie", "svc_authorization="+token)
			if csrf != nil {
				req.Header.Set(CSRFTokenHeader, *csrf)
			}
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			return resp.StatusCode
		}

		if checkCSRF {
			t.Run("on", func(t *testing.T) {
				require.Equal(t, http.StatusUnauthorized, ping(t, testNonce, nil))
				require.Equal(t, http.StatusOK, ping(t, testNonce, ptr(testNonce)))
				require.Equal(t, http.StatusUnauthorized, ping(t, testNonce, ptr("wrong")))
				require.Equal(t, http.StatusOK, ping(t, "", nil))
			})
		} else {
			t.Run("off", func(t *testing.T) {
				require.Equal(t, http.StatusOK, ping(t, testNonce, nil))
				require.Equal(t, http.StatusOK, ping(t, testNonce, ptr("wrong")))
				require.Equal(t, http.StatusOK, ping(t, "", nil))
			})
		}
	}
}

// TestCSRFCheckAnyIssuer pins the CheckCSRFToken doc: the SDK checks a
// token with a nonce whatever its issuer.
func TestCSRFCheckAnyIssuer(t *testing.T) {
	on := newCSRFTestOracle(t, true)
	c := jwt.NewClaims("workload", "https://oidc.example.test/team", "lutherapp:svc")
	c.Nonce = testNonce
	token, err := on.cfg.fakeIDP.MakeFakeIDPAuthToken(c)
	require.NoError(t, err)
	_, err = on.GetClaims(cookieContext(t, token, nil))
	require.Equal(t, codes.Unauthenticated, status.Code(err), "error: %v", err)
	_, err = on.GetClaims(cookieContext(t, token, ptr(testNonce)))
	require.NoError(t, err)
}
