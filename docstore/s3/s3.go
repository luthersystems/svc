// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/luthersystems/svc/docstore"
)

type missingRetryer struct {
	client.DefaultRetryer
}

var _ docstore.DocStore = &Store{}

func (retryer missingRetryer) ShouldRetry(req *request.Request) bool {
	if req.HTTPResponse.StatusCode == 404 {
		return true
	}
	return retryer.DefaultRetryer.ShouldRetry(req)
}

// New returns a new Store configured for the specified bucket and prefix.
func New(region string, bucket string, prefix string) (*Store, error) {
	sess, err := session.NewSession(&aws.Config{Region: aws.String(region)})
	if err != nil {
		return nil, err
	}
	svc := s3.New(sess)
	return &Store{bucket, prefix, svc}, nil
}

// NewWithSession returns a new Store configured for the specified session.
func NewWithSession(sess *session.Session, bucket string, prefix string) (*Store, error) {
	svc := s3.New(sess)
	return &Store{bucket, prefix, svc}, nil
}

// Store is an S3 implementation of a DocStore.
type Store struct {
	bucket string
	prefix string
	svc    *s3.S3
}

// Put writes bytes to an S3 object.
func (a *Store) Put(ctx context.Context, key string, body []byte) error {
	err := docstore.ValidKey(key)
	if err != nil {
		return err
	}

	input := &s3.PutObjectInput{
		Body:   aws.ReadSeekCloser(bytes.NewReader(body)),
		Bucket: aws.String(a.bucket),
		Key:    aws.String(fmt.Sprintf("%s/%s", a.prefix, key)),
	}

	request, _ := a.svc.PutObjectRequest(input)
	request.Retryer = client.DefaultRetryer{NumMaxRetries: 5}
	request.SetContext(ctx)
	err = request.Send()
	if err != nil {
		return fmt.Errorf("s3 put: %w", err)
	}

	return nil
}

// Get reads bytes stored in an S3 document.
func (a *Store) Get(ctx context.Context, key string) ([]byte, error) {
	err := docstore.ValidKey(key)
	if err != nil {
		return nil, err
	}
	input := &s3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(fmt.Sprintf("%s/%s", a.prefix, key)),
	}
	request, result := a.svc.GetObjectRequest(input)
	// retry requests that aren't in S3 for about 1 second to avoid issues
	// when rapidly writing and reading requests
	request.Retryer = missingRetryer{client.DefaultRetryer{NumMaxRetries: 5}}
	request.SetContext(ctx)
	err = request.Send()
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case s3.ErrCodeNoSuchKey:
				return nil, docstore.ErrRequestNotFound
			}
		}
		return nil, fmt.Errorf("s3 get: %w", err)
	}
	body, err := ioutil.ReadAll(result.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read result body: %w", err)
	}
	return body, nil
}

// GetStreaming streams an S3 document's bytes into the supplied
// http.ResponseWriter
func (a *Store) GetStreaming(key string, w http.ResponseWriter) error {
	input := &s3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(fmt.Sprintf("%s/%s", a.prefix, key)),
	}
	request, result := a.svc.GetObjectRequest(input)
	// retry requests that aren't in S3 for about 1 second to avoid issues
	// when rapidly writing and reading requests
	request.Retryer = missingRetryer{client.DefaultRetryer{NumMaxRetries: 5}}
	if err := request.Send(); err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case s3.ErrCodeNoSuchKey:
				return docstore.ErrRequestNotFound
			}
		}
		return fmt.Errorf("s3 get: %w", err)
	}
	w.Header().Set("Connection", "close")
	w.Header().Set("Content-Type", *(result.ContentType))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", *(result.ContentLength)))
	defer result.Body.Close()
	_, err := io.Copy(w, result.Body)
	if err != nil {
		return fmt.Errorf("s3 get: %w", err)
	}
	return nil
}

// Delete removes an object from the S3 bucket.
func (a *Store) Delete(key string) error {
	err := docstore.ValidKey(key)
	if err != nil {
		return err
	}

	input := &s3.DeleteObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(fmt.Sprintf("%s/%s", a.prefix, key)),
	}
	_, err = a.svc.DeleteObject(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case s3.ErrCodeNoSuchKey:
				return docstore.ErrRequestNotFound
			}
		}
		return fmt.Errorf("s3 delete: %w", err)
	}
	return nil
}
