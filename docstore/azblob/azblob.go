// Copyright © 2021 Luther Systems, Ltd. All right reserved.
package azblob

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net/url"
	"time"

	"github.com/Azure/azure-storage-blob-go/azblob"
	"github.com/Azure/go-autorest/autorest/adal"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/luthersystems/svc/docstore"
	"golang.org/x/crypto/pkcs12"
)

const (
	azureStorageResourceName = "https://storage.azure.com/"
)

var _ docstore.DocStore = &Store{}

func decodePkcs12(pkcs []byte, password string) (*x509.Certificate, *rsa.PrivateKey, error) {
	privateKey, certificate, err := pkcs12.Decode(pkcs, password)
	if err != nil {
		return nil, nil, err
	}

	rsaPrivateKey, isRsaKey := privateKey.(*rsa.PrivateKey)
	if !isRsaKey {
		return nil, nil, fmt.Errorf("PKCS#12 certificate must contain an RSA private key")
	}

	return certificate, rsaPrivateKey, nil
}

// NewFromCertificate generates a Store using an Azure certificate.
func NewFromCertificate(prefix, accountName, containerName, path, password, clientID, tenantID string) (*Store, error) {
	oauthConfig, err := adal.NewOAuthConfig(azure.PublicCloud.ActiveDirectoryEndpoint, tenantID)
	if err != nil {
		return nil, fmt.Errorf("oauth config: %w", err)
	}

	certData, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read the certificate file (%s): %w", path, err)
	}

	certificate, rsaPrivateKey, err := decodePkcs12(certData, password)
	if err != nil {
		return nil, fmt.Errorf("failed to decode pkcs12 certificate: %w", err)
	}

	spt, err := adal.NewServicePrincipalTokenFromCertificate(*oauthConfig, clientID, certificate, rsaPrivateKey, azureStorageResourceName)

	// obtain a fresh token
	err = spt.Refresh()
	if err != nil {
		return nil, fmt.Errorf("refresh: %w", err)
	}

	credential := azblob.NewTokenCredential(spt.Token().AccessToken, func(tc azblob.TokenCredential) time.Duration {
		err := spt.Refresh()
		if err != nil {
			// something went wrong, prevent the refresher from being triggered again
			return 0
		}

		tc.SetToken(spt.Token().AccessToken)

		// get the next token slightly before the current one expires
		return time.Until(spt.Token().Expires()) - 10*time.Second
	})

	p := azblob.NewPipeline(credential, azblob.PipelineOptions{})

	URL, err := url.Parse(fmt.Sprintf("https://%s.blob.core.windows.net/%s", accountName, containerName))
	if err != nil {
		return nil, err
	}

	containerURL := azblob.NewContainerURL(*URL, p)
	return &Store{
		prefix:       prefix,
		containerURL: containerURL,
	}, nil
}

// New constructs a storage blob from an access key.
func New(prefix, accountName, containerName, accountKey string) (*Store, error) {
	if len(prefix) == 0 {
		return nil, fmt.Errorf("missing prefix")
	}
	if len(accountName) == 0 {
		return nil, fmt.Errorf("missing account name")
	}
	if len(containerName) == 0 {
		return nil, fmt.Errorf("missing container name")
	}
	// accountkey?
	credential, err := azblob.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		return nil, err
	}
	p := azblob.NewPipeline(credential, azblob.PipelineOptions{})

	URL, err := url.Parse(fmt.Sprintf("https://%s.blob.core.windows.net/%s", accountName, containerName))
	if err != nil {
		return nil, err
	}

	// pipeline to make requests.
	containerURL := azblob.NewContainerURL(*URL, p)
	return &Store{
		prefix:       prefix,
		containerURL: containerURL,
	}, nil
}

// Store objects to azure.
type Store struct {
	prefix       string
	containerURL azblob.ContainerURL
}

func getBufFromBlob(ctx context.Context, blobURL azblob.BlockBlobURL) ([]byte, error) {
	_, err := blobURL.GetProperties(ctx, azblob.BlobAccessConditions{}, azblob.ClientProvidedKeyOptions{})
	if err != nil {
		serr, ok := err.(azblob.StorageError)
		if ok && serr.Response().StatusCode == 404 {
			return nil, docstore.ErrRequestNotFound
		}
		return nil, err
	}

	downloadResponse, err := blobURL.Download(ctx,
		0,
		azblob.CountToEnd,
		azblob.BlobAccessConditions{},
		false,
		azblob.ClientProvidedKeyOptions{})

	if err != nil {
		return nil, err
	}

	bodyStream := downloadResponse.Body(azblob.RetryReaderOptions{MaxRetryRequests: 3})

	downloadedData := bytes.Buffer{}
	_, err = downloadedData.ReadFrom(bodyStream)
	if err != nil {
		return nil, err
	}

	return downloadedData.Bytes(), nil
}

// Get reads bytes from azure blob.
func (s *Store) Get(key string) ([]byte, error) {
	ctx := context.Background()
	blobURL := s.containerURL.NewBlockBlobURL(fmt.Sprintf("%s/%s", s.prefix, key))
	b, err := getBufFromBlob(ctx, blobURL)
	if err != nil {
		return nil, fmt.Errorf("az get: %w", err)
	}

	return b, nil
}

func putBufToBlob(ctx context.Context, blobURL azblob.BlockBlobURL, blob []byte) error {
	_, err := azblob.UploadStreamToBlockBlob(ctx,
		bytes.NewReader(blob),
		blobURL,
		azblob.UploadStreamToBlockBlobOptions{})
	if err != nil {
		return err
	}

	return nil
}

// Put writes bytes to azure blob.
func (s *Store) Put(key string, body []byte) error {
	ctx := context.Background()
	blobURL := s.containerURL.NewBlockBlobURL(fmt.Sprintf("%s/%s", s.prefix, key))
	err := putBufToBlob(ctx, blobURL, body)
	if err != nil {
		return fmt.Errorf("az put: %w", err)
	}

	return nil
}
