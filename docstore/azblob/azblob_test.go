// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package azblob

import (
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/luthersystems/svc/docstore"
	"github.com/stretchr/testify/require"
)

var (
	runIntegration = flag.Bool("integration", false, "test integration")
)

// TestFunctionalIntegration runs functional tests on azure.
func TestFunctionalIntegration(t *testing.T) {
	if !*runIntegration {
		t.Skip()
	}

	accountName := os.Getenv("AZ_BLOB_ACCOUNT_NAME")
	containerName := os.Getenv("AZ_BLOB_CONTAINER_NAME")
	accountKey := os.Getenv("AZ_BLOB_ACCOUNT_KEY")

	client, err := New("test", accountName, containerName, accountKey)
	require.NoError(t, err)
	testKey := fmt.Sprintf("%s-%s", "test", uuid.New().String())
	data := []byte("test")
	err = client.Put(testKey, data)
	require.NoError(t, err)

	b, err := client.Get(testKey)
	require.NoError(t, err)
	require.Equal(t, b, data)

	_, err = client.Get("fnord-missing")
	require.Error(t, err, docstore.ErrRequestNotFound)
}
