// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/luthersystems/svc/docstore"
	"github.com/sirupsen/logrus"
)

var _ docstore.DocStore = &Store{}

// maxAttempts preserves the legacy DefaultRetryer{NumMaxRetries: 5}
// behaviour (1 initial attempt + 5 retries).
const maxAttempts = 6

// missingRetryer retries GetObject calls that fail with NoSuchKey for
// roughly a second, to avoid issues when rapidly writing and reading
// objects (the legacy custom retryer's 404 behaviour).
func missingRetryer() aws.Retryer {
	return retry.AddWithErrorCodes(
		retry.AddWithMaxAttempts(retry.NewStandard(), maxAttempts),
		(&types.NoSuchKey{}).ErrorCode(),
	)
}

func standardRetryer() aws.Retryer {
	return retry.AddWithMaxAttempts(retry.NewStandard(), maxAttempts)
}

// New returns a new Store configured for the specified bucket and prefix.
func New(region string, bucket string, prefix string) (*Store, error) {
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return NewFromConfig(cfg, bucket, prefix), nil
}

// NewFromConfig returns a new Store using the supplied aws-sdk-go-v2
// configuration. It replaces the legacy NewWithSession constructor, which
// took an aws-sdk-go (v1) *session.Session.
func NewFromConfig(cfg aws.Config, bucket string, prefix string) *Store {
	return &Store{bucket, prefix, s3.NewFromConfig(cfg)}
}

// Store is an S3 implementation of a DocStore.
type Store struct {
	bucket string
	prefix string
	svc    *s3.Client
}

// Put writes bytes to an S3 object.
func (a *Store) Put(ctx context.Context, key string, body []byte) error {
	err := docstore.ValidKey(key)
	if err != nil {
		return err
	}

	input := &s3.PutObjectInput{
		Body:   bytes.NewReader(body),
		Bucket: aws.String(a.bucket),
		Key:    aws.String(fmt.Sprintf("%s/%s", a.prefix, key)),
	}

	_, err = a.svc.PutObject(ctx, input, func(o *s3.Options) {
		o.Retryer = standardRetryer()
	})
	if err != nil {
		return fmt.Errorf("s3 put: %w", err)
	}

	return nil
}

func (a *Store) getObject(ctx context.Context, key string) (*s3.GetObjectOutput, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(fmt.Sprintf("%s/%s", a.prefix, key)),
	}
	// retry requests that aren't in S3 for about 1 second to avoid issues
	// when rapidly writing and reading requests
	result, err := a.svc.GetObject(ctx, input, func(o *s3.Options) {
		o.Retryer = missingRetryer()
	})
	if err != nil {
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return nil, docstore.ErrRequestNotFound
		}
		return nil, fmt.Errorf("s3 get: %w", err)
	}
	return result, nil
}

// Get reads bytes stored in an S3 document.
func (a *Store) Get(ctx context.Context, key string) ([]byte, error) {
	err := docstore.ValidKey(key)
	if err != nil {
		return nil, err
	}
	result, err := a.getObject(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := result.Body.Close(); err != nil {
			logrus.WithError(err).Warn("get: close")
		}
	}()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read result body: %w", err)
	}
	return body, nil
}

// GetStreaming streams an S3 document's bytes into the supplied
// http.ResponseWriter
func (a *Store) GetStreaming(key string, w http.ResponseWriter) error {
	result, err := a.getObject(context.Background(), key)
	if err != nil {
		return err
	}
	w.Header().Set("Connection", "close")
	if result.ContentType != nil {
		w.Header().Set("Content-Type", *result.ContentType)
	}
	if result.ContentLength != nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", *result.ContentLength))
	}
	defer func() {
		if err := result.Body.Close(); err != nil {
			logrus.WithError(err).Warn("get streaming: close")
		}
	}()
	_, err = io.Copy(w, result.Body)
	if err != nil {
		return fmt.Errorf("s3 get: %w", err)
	}
	return nil
}

// Delete removes an object from the S3 bucket.
func (a *Store) Delete(ctx context.Context, key string) error {
	err := docstore.ValidKey(key)
	if err != nil {
		return err
	}

	input := &s3.DeleteObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(fmt.Sprintf("%s/%s", a.prefix, key)),
	}

	_, err = a.svc.DeleteObject(ctx, input)
	if err != nil {
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return docstore.ErrRequestNotFound
		}
		return fmt.Errorf("s3 delete: %w", err)
	}
	return nil
}
