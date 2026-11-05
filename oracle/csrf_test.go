// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package oracle

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luthersystems/lutherauth-sdk-go/jwt"
	hellov1 "github.com/luthersystems/svc/oracle/testservice/gen/go/proto/hello/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
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

// unsignedToken is a JWT with the given payload and no valid signature.
func unsignedToken(payload string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc([]byte(payload)) + ".sig"
}

func TestCSRFDoubleSubmitOK(t *testing.T) {
	orc := newCSRFTestOracle(t, true)
	token := func(c *jwt.Claims) string {
		tok, err := orc.cfg.fakeIDP.MakeFakeIDPAuthToken(c)
		require.NoError(t, err)
		return tok
	}
	withNonce := token(testClaims(testNonce))
	noNonce := token(testClaims(""))
	// The check covers a nonce whatever the token's issuer.
	otherIssuer := jwt.NewClaims("workload", "https://oidc.example.test/team", "lutherapp:svc")
	otherIssuer.Nonce = testNonce
	otherIssuerNonce := token(otherIssuer)

	cookie := func(tok string) string { return "svc_authorization=" + tok }
	for _, tc := range []struct {
		name    string
		method  string
		cookies []string
		header  map[string]string
		ok      bool
	}{
		{"POST, nonce, no header", http.MethodPost, []string{cookie(withNonce)}, nil, false},
		{"POST, nonce, matching header", http.MethodPost, []string{cookie(withNonce)}, map[string]string{CSRFTokenHeader: testNonce}, true},
		{"POST, nonce, wrong header", http.MethodPost, []string{cookie(withNonce)}, map[string]string{CSRFTokenHeader: "ffff" + testNonce[4:]}, false},
		{"POST, nonce, prefix of the nonce", http.MethodPost, []string{cookie(withNonce)}, map[string]string{CSRFTokenHeader: testNonce[:8]}, false},
		{"POST, nonce, empty header", http.MethodPost, []string{cookie(withNonce)}, map[string]string{CSRFTokenHeader: ""}, false},
		{"PUT, nonce, no header", http.MethodPut, []string{cookie(withNonce)}, nil, false},
		{"PATCH, nonce, no header", http.MethodPatch, []string{cookie(withNonce)}, nil, false},
		{"DELETE, nonce, no header", http.MethodDelete, []string{cookie(withNonce)}, nil, false},
		{"GET, nonce, no header", http.MethodGet, []string{cookie(withNonce)}, nil, true},
		{"HEAD, nonce, wrong header", http.MethodHead, []string{cookie(withNonce)}, map[string]string{CSRFTokenHeader: "wrong"}, true},
		{"OPTIONS, nonce, no header", http.MethodOptions, []string{cookie(withNonce)}, nil, true},
		{"POST, no nonce, no header", http.MethodPost, []string{cookie(noNonce)}, nil, true},
		{"POST, other issuer's nonce, no header", http.MethodPost, []string{cookie(otherIssuerNonce)}, nil, false},
		{"POST, cookie name case-insensitive", http.MethodPost, []string{"SVC_AUTHORIZATION=" + withNonce}, nil, false},
		// The oracle reads the token from the cookie even with an
		// Authorization header, so that header does not skip the check.
		{"POST, Authorization beside a nonce cookie", http.MethodPost, []string{cookie(withNonce)}, map[string]string{"Authorization": "Bearer x"}, false},
		{"POST, no auth cookie", http.MethodPost, []string{"other=1"}, nil, true},
		{"POST, no cookie", http.MethodPost, nil, nil, true},
		// GetValue reads only the first Cookie header.
		{"POST, auth cookie in a second Cookie header", http.MethodPost, []string{"other=1", cookie(withNonce)}, nil, true},
		{"POST, junk token", http.MethodPost, []string{cookie("junk")}, nil, true},
		{"POST, nonce not a string", http.MethodPost, []string{cookie(unsignedToken(`{"nonce":1}`))}, nil, true},
		{"POST, later null in another case", http.MethodPost, []string{cookie(unsignedToken(`{"nonce":"n","Nonce":null}`))}, nil, false},
		{"POST, nonce key in another case", http.MethodPost, []string{cookie(unsignedToken(`{"NONCE":"n"}`))}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), tc.method, "/v1/hello", nil)
			for _, c := range tc.cookies {
				r.Header.Add("Cookie", c)
			}
			for k, v := range tc.header {
				r.Header.Set(k, v)
			}
			require.Equal(t, tc.ok, csrfDoubleSubmitOK(r, "svc_authorization"))
		})
	}
}

func TestUnverifiedNonce(t *testing.T) {
	for _, tc := range []struct {
		name  string
		token string
		nonce string
	}{
		{"nonce", unsignedToken(`{"nonce":"n"}`), "n"},
		{"no nonce", unsignedToken(`{"sub":"s"}`), ""},
		{"null nonce", unsignedToken(`{"nonce":null}`), ""},
		{"nonce key in another case", unsignedToken(`{"Nonce":"n"}`), "n"},
		// A null in a later key of another case leaves the nonce as the
		// verifier's struct reads it.
		{"later null in another case", unsignedToken(`{"nonce":"n","Nonce":null}`), "n"},
		{"later value in another case", unsignedToken(`{"nonce":"a","NONCE":"b"}`), "b"},
		{"data after the claims", unsignedToken(`{"nonce":"n"} x`), "n"},
		// The verifier cannot decode these, so it refuses the token.
		{"nonce not a string", unsignedToken(`{"nonce":["n"]}`), ""},
		{"not a JWT", "junk", ""},
		{"bad base64", "a.!!!.c", ""},
		{"bad JSON", unsignedToken(`[`), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.nonce, unverifiedNonce(tc.token))
		})
	}
}

// claimsHelloServer answers with the result of the oracle's claims check.
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

func (s *claimsHelloServer) SayHello(ctx context.Context, _ *hellov1.HelloRequest) (*hellov1.HelloResponse, error) {
	if _, err := s.orc.GetClaims(ctx); err != nil {
		return nil, err
	}
	return &hellov1.HelloResponse{}, nil
}

// TestCSRFCheckThroughGateway sends HTTP requests through the oracle's
// gateway handler, where the check is made.
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

		mux, handler := orc.grpcGateway(nil, nil)
		require.NoError(t, hellov1.RegisterHelloServiceHandler(t.Context(), mux, conn))
		// A route a service adds to the mux itself is checked too.
		require.NoError(t, mux.HandlePath(http.MethodPost, "/v1/raw", func(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
			w.WriteHeader(http.StatusOK)
		}))
		gwLis, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)
		gwSrv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
		go func() { _ = gwSrv.Serve(gwLis) }()
		t.Cleanup(func() { _ = gwSrv.Close() })

		call := func(t *testing.T, method, path, nonce string, csrf *string) int {
			t.Helper()
			token, err := orc.cfg.fakeIDP.MakeFakeIDPAuthToken(testClaims(nonce))
			require.NoError(t, err)
			body := ""
			if method == http.MethodPost {
				body = "{}"
			}
			req, err := http.NewRequestWithContext(t.Context(), method,
				"http://"+gwLis.Addr().String()+path, strings.NewReader(body))
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
		get, post := http.MethodGet, http.MethodPost
		ptr := func(s string) *string { return &s }

		if checkCSRF {
			t.Run("on", func(t *testing.T) {
				require.Equal(t, http.StatusForbidden, call(t, post, "/v1/hello", testNonce, nil))
				require.Equal(t, http.StatusOK, call(t, post, "/v1/hello", testNonce, ptr(testNonce)))
				require.Equal(t, http.StatusForbidden, call(t, post, "/v1/hello", testNonce, ptr("wrong")))
				require.Equal(t, http.StatusOK, call(t, post, "/v1/hello", "", nil))
				require.Equal(t, http.StatusForbidden, call(t, post, "/v1/raw", testNonce, nil))
				require.Equal(t, http.StatusOK, call(t, post, "/v1/raw", testNonce, ptr(testNonce)))
				// A safe method is not checked.
				require.Equal(t, http.StatusOK, call(t, get, "/v1/ping", testNonce, nil))
				require.Equal(t, http.StatusOK, call(t, get, "/v1/ping", testNonce, ptr("wrong")))
			})
		} else {
			t.Run("off", func(t *testing.T) {
				require.Equal(t, http.StatusOK, call(t, post, "/v1/hello", testNonce, nil))
				require.Equal(t, http.StatusOK, call(t, post, "/v1/hello", testNonce, ptr("wrong")))
				require.Equal(t, http.StatusOK, call(t, post, "/v1/raw", testNonce, nil))
				require.Equal(t, http.StatusOK, call(t, get, "/v1/ping", testNonce, nil))
			})
		}
	}
}

// TestCSRFCheckNotInClaims checks that GetClaims makes no CSRF check: a
// gRPC call that does not come through the gateway is not a browser request.
func TestCSRFCheckNotInClaims(t *testing.T) {
	orc := newCSRFTestOracle(t, true)
	token, err := orc.cfg.fakeIDP.MakeFakeIDPAuthToken(testClaims(testNonce))
	require.NoError(t, err)
	ctx := metadata.NewIncomingContext(MakeTestContext(t), metadata.Pairs("cookie", "svc_authorization="+token))
	_, err = orc.GetClaims(ctx)
	require.NoError(t, err)
	_, err = orc.GetClaims(orc.MakeTestAuthContext(t, testClaims(testNonce)))
	require.NoError(t, err)
}

// TestIncomingHeaderMatcher checks the forwarded header set newOracle builds.
func TestIncomingHeaderMatcher(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.AddHeaderForwarder("X-My-Header")
	cfg.CheckCSRFToken = true
	orc, closeFn := NewTestOracle(t, cfg)
	t.Cleanup(closeFn)

	for _, h := range []string{"Cookie", "cookie", "X-Request-Id", "User-Agent", "X-My-Header", "x-my-header", "Referer"} {
		got, ok := orc.incomingHeaderMatcher(h)
		require.True(t, ok, h)
		require.Equal(t, h, got)
	}
	// The CSRF header is checked on the gateway, so it is not forwarded.
	for _, h := range []string{"Authorization", "X-Other", "Grpc-Metadata-Foo", CSRFTokenHeader} {
		_, ok := orc.incomingHeaderMatcher(h)
		require.False(t, ok, h)
	}
	require.Len(t, orc.forwardedHeaders, len(orc.gatewayForwardedHeaders()))
}

func TestCSRFCheckNeedsAuthCookie(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CheckCSRFToken = true
	require.ErrorContains(t, cfg.Valid(), "auth cookie")
	_, err := NewOracle(cfg)
	require.ErrorContains(t, err, "auth cookie", "newOracle refuses it too")

	cfg.AddAuthCookieForwarder("svc_authorization", 300, false, true)
	require.NoError(t, cfg.Valid())

	off := DefaultConfig()
	require.NoError(t, off.Valid(), "off needs no cookie")
}
