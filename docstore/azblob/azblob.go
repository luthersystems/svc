// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package azblob

import (
	"bytes"
	"context"
	"fmt"
	"net/url"

	"github.com/Azure/azure-storage-blob-go/azblob"
	"github.com/luthersystems/svc/docstore"
)

var _ docstore.DocStore = &Store{}

// New creates an Azure-backed docstore
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
	credential, err := azblob.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		return nil, err
	}
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

// Store is an Azure implementation of a DocStore.
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
