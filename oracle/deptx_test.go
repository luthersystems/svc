package oracle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	hellov1 "github.com/luthersystems/svc/oracle/testservice/gen/go/proto/hello/v1"
	"github.com/luthersystems/svc/txctx"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Example: a "UseDepTx" endpoint (for dependent transaction logic)
func (s *serverImpl) UseDepTx(ctx context.Context, _ *emptypb.Empty) (*hellov1.UseDepTxResponse, error) {
	oldID := txctx.GetTransactionDetails(ctx).TransactionID

	s.nextID++
	newID := fmt.Sprintf("depTx-%d", s.nextID)
	txctx.SetTransactionDetails(ctx, txctx.TransactionDetails{TransactionID: newID})

	return &hellov1.UseDepTxResponse{
		OldTxId: oldID,
		NewTxId: newID,
	}, nil
}

func TestOracleCombined(t *testing.T) {
	// 1) Build an oracle Config that triggers dependent Tx cookies, etc.
	cfg := &Config{
		DependentTxCookie: "dep-tx", // We'll produce a cookie named "dep-tx"
		EmulateCC:         true,
		Verbose:           testing.Verbose(),
	}
	// Create a cookie forwarder + header forwarder, which add WithForwardResponseOption to cfg.gatewayOpts
	cookieFwd := cfg.AddCookieForwarder("testCookie", 3600, false, true)
	headerFwd := cfg.AddHeaderForwarder("X-Custom-Header")

	// 2) Create our server that references these forwarders
	s := &serverImpl{
		cookieFwd: cookieFwd,
		headerFwd: headerFwd,
	}

	// 3) Spin up an in-process gRPC server
	grpcServer := grpc.NewServer()
	hellov1.RegisterHelloServiceServer(grpcServer, s)

	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go grpcServer.Serve(grpcLis)

	// 4) Dial from the gateway
	ctx := context.Background()
	conn, err := grpc.DialContext(ctx, grpcLis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	// 5) Build a runtime.ServeMux with the cfg.gatewayOpts (which includes the
	//    forwardResponseOption for the "dep-tx" cookie).
	gwMux := runtime.NewServeMux(cfg.gatewayOpts...)
	err = hellov1.RegisterHelloServiceHandler(ctx, gwMux, conn)
	require.NoError(t, err)

	gwLis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	gwSrv := &http.Server{Handler: gwMux}
	go gwSrv.Serve(gwLis)

	// 6) Test the "UseDepTx" endpoint:
	client := &http.Client{}
	firstResp, err := client.Post(fmt.Sprintf("http://%s/v1/dep_tx", gwLis.Addr().String()),
		"application/json",
		bytes.NewBufferString(`{}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, firstResp.StatusCode)

	var out1 hellov1.UseDepTxResponse
	err = json.NewDecoder(firstResp.Body).Decode(&out1)
	require.NoError(t, err)
	firstResp.Body.Close()
	t.Logf("First call: old=%s new=%s", out1.GetOldTxId(), out1.GetNewTxId())
	require.Empty(t, out1.GetOldTxId())
	require.NotEmpty(t, out1.GetNewTxId())

	// Confirm a "dep-tx" cookie was returned
	var depCookie *http.Cookie
	for _, c := range firstResp.Cookies() {
		if c.Name == "dep-tx" {
			depCookie = c
			break
		}
	}
	require.NotNil(t, depCookie, "should have a dep-tx cookie")
	require.Equal(t, out1.GetNewTxId(), depCookie.Value)

	// 7) Second call re-sends that cookie
	req2, err := http.NewRequest("POST", fmt.Sprintf("http://%s/v1/dep_tx", gwLis.Addr().String()), bytes.NewBufferString(`{}`))
	require.NoError(t, err)
	req2.AddCookie(depCookie)
	req2.Header.Set("Content-Type", "application/json")

	secondResp, err := client.Do(req2)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, secondResp.StatusCode)

	var out2 hellov1.UseDepTxResponse
	err = json.NewDecoder(secondResp.Body).Decode(&out2)
	require.NoError(t, err)
	secondResp.Body.Close()
	t.Logf("Second call: old=%s new=%s", out2.GetOldTxId(), out2.GetNewTxId())
	require.Equal(t, out1.GetNewTxId(), out2.GetOldTxId(), "the oldTx should match the first call's newTx")
	require.NotEmpty(t, out2.GetNewTxId())
	require.NotEqual(t, out2.GetOldTxId(), out2.GetNewTxId())

	// 8) Clean up
	grpcServer.Stop()
	_ = gwSrv.Close()
}
