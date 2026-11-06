// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package oracle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	common "buf.build/gen/go/luthersystems/protos/protocolbuffers/go/common/v1"
	healthcheck "buf.build/gen/go/luthersystems/protos/protocolbuffers/go/healthcheck/v1"
	"github.com/luthersystems/lutherauth-sdk-go/jwt"
	"github.com/stretchr/testify/require"
)

// The health check write (luthersystems/substrate#1037) is off unless a
// service sets an authorizer. These tests pin that the oracle asks the
// gateway for the write only for an allowed caller, sends the caller's
// token with it, and reports the gateway's write row.

// writeGateway stands in for the gateway's health_check endpoint. It
// records each request's write parameter and bearer, and adds a write row
// with status writeStatus when asked to write. Each row has the four string
// fields the SDK's parser requires, as the gateway's rows do.
type writeGateway struct {
	writeStatus string
	mu          sync.Mutex
	reqs        []writeGatewayRequest
}

type writeGatewayRequest struct {
	write, auth string
}

func (g *writeGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req := writeGatewayRequest{write: r.URL.Query().Get("write"), auth: r.Header.Get("Authorization")}
	g.mu.Lock()
	g.reqs = append(g.reqs, req)
	g.mu.Unlock()
	type report struct {
		Timestamp      string `json:"timestamp"`
		Status         string `json:"status"`
		ServiceName    string `json:"service_name"`
		ServiceVersion string `json:"service_version"`
	}
	now := time.Now().Format(timestampFormat)
	reports := []report{{Timestamp: now, Status: "UP", ServiceName: "phylum", ServiceVersion: "v1"}}
	if req.write == "true" {
		reports = append(reports, report{Timestamp: now, Status: g.writeStatus, ServiceName: "write", ServiceVersion: "v1"})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"reports": reports}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (g *writeGateway) requests() []writeGatewayRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]writeGatewayRequest(nil), g.reqs...)
}

// newWriteOracle builds an oracle whose phylum is a writeGateway, with the
// fake IDP and an auth cookie, and with authorize as its write authorizer
// (nil: the write is off).
func newWriteOracle(t *testing.T, writeStatus string, authorize func(context.Context) error) (*Oracle, *writeGateway) {
	t.Helper()
	gw := &writeGateway{writeStatus: writeStatus}
	srv := httptest.NewServer(gw)
	t.Cleanup(srv.Close)
	cfg := DefaultConfig()
	cfg.GatewayEndpoint = srv.URL
	cfg.AddAuthCookieForwarder("svc_authorization", 300, false, true)
	_, err := cfg.AddFakeIDP(t)
	require.NoError(t, err)
	if authorize != nil {
		cfg.SetHealthCheckWriteAuthorizer(authorize)
	}
	orc, err := newOracle(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = orc.tracer.Shutdown(context.Background()) })
	return orc, gw
}

func reportStatus(resp *healthcheck.GetHealthCheckResponse, service string) string {
	for _, r := range resp.GetReports() {
		if r.GetServiceName() == service {
			return r.GetStatus()
		}
	}
	return ""
}

func writeRequest(httpOnly bool) *healthcheck.GetHealthCheckRequest {
	req := &healthcheck.GetHealthCheckRequest{}
	req.SetWrite(true)
	req.SetHttpOnly(httpOnly)
	return req
}

// allowSubject allows only a caller whose token subject is sub.
func allowSubject(orc **Oracle, sub string) func(context.Context) error {
	return func(ctx context.Context) error {
		c, err := (*orc).GetClaims(ctx)
		if err != nil {
			return err
		}
		if c.Subject != sub {
			return errors.New("not allowed")
		}
		return nil
	}
}

func TestHealthCheckWritePlainCheckAsksNoWrite(t *testing.T) {
	orc, gw := newWriteOracle(t, "UP", func(context.Context) error { return nil })
	resp, err := orc.GetHealthCheck(MakeTestContext(t), &healthcheck.GetHealthCheckRequest{})
	require.NoError(t, err)
	require.Nil(t, resp.GetException())
	require.Equal(t, []writeGatewayRequest{{}}, gw.requests(), "no write and no token on a plain check")
	require.Empty(t, reportStatus(resp, "write"))
}

func TestHealthCheckWriteOffByDefault(t *testing.T) {
	orc, gw := newWriteOracle(t, "UP", nil)
	resp, err := orc.GetHealthCheck(MakeTestContext(t), writeRequest(false))
	require.NoError(t, err)
	require.Equal(t, common.Exception_SECURITY_VIOLATION, resp.GetException().GetType())
	require.Contains(t, resp.GetException().GetDescription(), "off on this oracle")
	require.Empty(t, resp.GetReports(), "a refusal reports nothing")
	require.Empty(t, gw.requests(), "a refused write never reaches the gateway")
}

func TestHealthCheckWriteNeedsGatewayCheck(t *testing.T) {
	orc, gw := newWriteOracle(t, "UP", func(context.Context) error { return nil })
	resp, err := orc.GetHealthCheck(MakeTestContext(t), writeRequest(true))
	require.NoError(t, err)
	require.Equal(t, common.Exception_BUSINESS, resp.GetException().GetType())
	require.Empty(t, gw.requests())
}

func TestHealthCheckWriteAuthorizer(t *testing.T) {
	var orc *Oracle
	orc, gw := newWriteOracle(t, "UP", allowSubject(&orc, "luther-service:healthcheck"))

	// No claims: refused.
	resp, err := orc.GetHealthCheck(MakeTestContext(t), writeRequest(false))
	require.NoError(t, err)
	require.Equal(t, common.Exception_SECURITY_VIOLATION, resp.GetException().GetType())

	// Another subject: refused.
	other := jwt.NewClaims("someone@example.com", "luther:auth:svc-local", "lutherapp:svc")
	resp, err = orc.GetHealthCheck(orc.MakeTestAuthContext(t, other), writeRequest(false))
	require.NoError(t, err)
	require.Equal(t, common.Exception_SECURITY_VIOLATION, resp.GetException().GetType())
	require.Empty(t, gw.requests(), "a refused write never reaches the gateway")

	// The allowed subject: the gateway gets the write and the caller's token.
	allowed := jwt.NewClaims("luther-service:healthcheck", "luther:auth:svc-local", "lutherapp:svc")
	ctx := orc.MakeTestAuthContext(t, allowed)
	c, err := orc.GetClaims(ctx)
	require.NoError(t, err)
	resp, err = orc.GetHealthCheck(ctx, writeRequest(false))
	require.NoError(t, err)
	require.Nil(t, resp.GetException())
	require.Equal(t, "UP", reportStatus(resp, "write"))
	require.Equal(t, []writeGatewayRequest{{write: "true", auth: "Bearer " + c.Token()}}, gw.requests())
}

func TestHealthCheckWriteDownReported(t *testing.T) {
	orc, _ := newWriteOracle(t, "DOWN", func(context.Context) error { return nil })
	resp, err := orc.GetHealthCheck(MakeTestContext(t), writeRequest(false))
	require.NoError(t, err)
	require.Nil(t, resp.GetException())
	require.Equal(t, "DOWN", reportStatus(resp, "write"))
}

// TestHealthCheckWriteHTTP pins the HTTP status of the oracle's own
// /v1/health_check route: 403 for a refused write, 400 for a bad one, 503
// for a DOWN write, and 200 for an UP one.
func TestHealthCheckWriteHTTP(t *testing.T) {
	get := func(orc *Oracle, query string) int {
		w := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, healthCheckPath+query, nil)
		orc.healthCheckHandler().ServeHTTP(w, r)
		return w.Code
	}
	off, _ := newWriteOracle(t, "UP", nil)
	require.Equal(t, http.StatusOK, get(off, ""))
	require.Equal(t, http.StatusForbidden, get(off, "?write=true"))

	on, gw := newWriteOracle(t, "UP", func(context.Context) error { return nil })
	require.Equal(t, http.StatusBadRequest, get(on, "?write=true&http_only=true"))
	require.Equal(t, http.StatusOK, get(on, "?write=true"))
	require.Equal(t, []writeGatewayRequest{{write: "true"}}, gw.requests())

	down, _ := newWriteOracle(t, "DOWN", func(context.Context) error { return nil })
	require.Equal(t, http.StatusServiceUnavailable, get(down, "?write=true"))
}
