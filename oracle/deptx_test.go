package oracle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"testing"

	hellov1 "github.com/luthersystems/svc/oracle/testservice/gen/go/proto/hello/v1"
	"github.com/luthersystems/svc/txctx"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Example: a "UseDepTx" endpoint (for dependent transaction logic)
func (s *serverImpl) UseDepTx(ctx context.Context, _ *emptypb.Empty) (*hellov1.UseDepTxResponse, error) {
	oldID := txctx.GetTransactionDetails(ctx).TransactionID

	s.nextID++
	newID := fmt.Sprintf("depTx-%d", s.nextID)
	txctx.SetTransactionDetails(ctx, txctx.TransactionDetails{TransactionID: newID})

	log.Printf("UseDepTx called: oldID=%s newID=%s", oldID, newID)

	return &hellov1.UseDepTxResponse{
		OldTxId: oldID,
		NewTxId: newID,
	}, nil
}

func TestOracleDepTx(t *testing.T) {
	// Start the test server using StartGateway
	orc, stop := makeTestOracleServer(t)
	defer stop()

	httpAddr := orc.cfg.ListenAddress
	require.NotEmpty(t, httpAddr)

	// 1) Make a request to UseDepTx endpoint
	client := &http.Client{}
	firstResp, err := client.Post(fmt.Sprintf("http://%s/v1/dep_tx", httpAddr),
		"application/json",
		bytes.NewBufferString(`{}`))

	bodyBytes, _ := io.ReadAll(firstResp.Body)
	log.Printf("Raw HTTP Response Body: %s", string(bodyBytes))

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, firstResp.StatusCode)

	// 2) Parse response
	var out1 hellov1.UseDepTxResponse

	err = protojson.Unmarshal(bodyBytes, &out1)
	require.NoError(t, err)

	t.Logf("First call: old=%s new=%s", out1.GetOldTxId(), out1.GetNewTxId())
	require.Empty(t, out1.GetOldTxId())
	require.NotEmpty(t, out1.GetNewTxId())

	// 3) Check the response cookies
	require.NotEmpty(t, firstResp.Cookies(), "no cookies")
	var depCookie *http.Cookie
	for _, c := range firstResp.Cookies() {
		if c.Name == "dep-tx" {
			depCookie = c
			break
		}
	}
	require.NotNil(t, depCookie, "should have a dep-tx cookie")
	require.Equal(t, out1.GetNewTxId(), depCookie.Value)

	// 4) Second request using the dep-tx cookie
	req2, err := http.NewRequest("POST", fmt.Sprintf("http://%s/v1/dep_tx", httpAddr), bytes.NewBufferString(`{}`))
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
}
