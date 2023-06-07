// Copyright © 2021 Luther Systems, Ltd. All right reserved.
package azblob

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/luthersystems/svc/docstore"
	"github.com/stretchr/testify/require"
)

const (
	reqTimeout = 30 * time.Second
)

var (
	runIntegration = flag.Bool("integration", false, "test integration")
)

// TestFunctionalIntegration runs functional tests on azure.
// export AZ_BLOB_ACCOUNT_NAME="***"
// export AZ_BLOB_CONTAINER_NAME="***"
// export AZ_BLOB_ACCOUNT_KEY=$(az storage account keys list --account-name $AZ_BLOB_ACCOUNT_NAME --query '[0].value' | jq -r)
func TestFunctionalIntegration(t *testing.T) {
	if !*runIntegration {
		t.Skip()
	}

	accountName := os.Getenv("AZ_BLOB_ACCOUNT_NAME")
	containerName := os.Getenv("AZ_BLOB_CONTAINER_NAME")
	accountKey := os.Getenv("AZ_BLOB_ACCOUNT_KEY")

	store, err := New("test", accountName, containerName, accountKey)
	require.NoError(t, err)

	do(t, store)
}

// TestFunctionalCertificateIntegration runs functional tests on azure.
// export AZ_BLOB_ACCOUNT_NAME="***"
// export AZ_BLOB_CONTAINER_NAME="***"
// export AZ_BLOB_CERT_PATH="***.p12"
// export AZ_BLOB_CERT_PASSWORD=""
// export AZ_BLOB_CERT_CLIENT_ID="***"
// export AZ_BLOB_CERT_TENANT_ID="***"
func TestFunctionalCertificateIntegration(t *testing.T) {
	if !*runIntegration {
		t.Skip()
	}

	accountName := os.Getenv("AZ_BLOB_ACCOUNT_NAME")
	containerName := os.Getenv("AZ_BLOB_CONTAINER_NAME")
	certPath := os.Getenv("AZ_BLOB_CERT_PATH")
	certPassword := os.Getenv("AZ_BLOB_CERT_PASSWORD")
	clientID := os.Getenv("AZ_BLOB_CERT_CLIENT_ID")
	tenantID := os.Getenv("AZ_BLOB_CERT_TENANT_ID")

	store, err := NewFromCertificate("test", accountName, containerName, certPath, certPassword, clientID, tenantID)
	require.NoError(t, err)

	do(t, store)
}

func do(t *testing.T, store *Store) {
	var err error
	testKey := fmt.Sprintf("%s-%s", "test", uuid.New().String())
	data := []byte("test")
	bg := context.Background()
	ctx, done := context.WithTimeout(bg, reqTimeout)
	defer done()
	err = store.Put(ctx, testKey, data)
	require.NoError(t, err)

	ctx, done = context.WithTimeout(bg, reqTimeout)
	defer done()
	b, err := store.Get(ctx, testKey)
	require.NoError(t, err)
	require.Equal(t, b, data)

	ctx, done = context.WithTimeout(bg, reqTimeout)
	defer done()
	err = store.Delete(ctx, testKey)
	require.NoError(t, err)

	ctx, done = context.WithTimeout(bg, reqTimeout)
	defer done()
	_, err = store.Get(ctx, "fnord-missing")
	require.Error(t, err, docstore.ErrRequestNotFound)

	ctx, done = context.WithTimeout(bg, reqTimeout)
	defer done()
	_, err = store.Get(ctx, "public-009e2eb9-0e36-45b3-9697-f3903f96344f.jpeg")
	require.NoError(t, err)
}
