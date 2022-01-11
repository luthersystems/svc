// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package reqarchive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/luthersystems/svc/midware"
	"github.com/sirupsen/logrus"
)

type s3Backend struct {
	client  *s3.Client
	bucket  string
	prefix  string
	timeout time.Duration
	wg      sync.WaitGroup
	log     func(string) *logrus.Entry
}

func (b *s3Backend) Write(ctx context.Context, reqID string, content []byte) {
	b.wg.Add(1)
	go (func() {
		defer b.wg.Done()
		ctx, done := context.WithTimeout(ctx, b.timeout)
		defer done()
		input := &s3.PutObjectInput{
			Body:   bytes.NewReader(content),
			Bucket: aws.String(b.bucket),
			Key:    aws.String(fmt.Sprintf("%s/%s", b.prefix, reqID)),
		}
		_, err := b.client.PutObject(ctx, input)
		if err != nil {
			b.log(reqID).WithError(err).
				Error("request archiver failed to write request")
		}
	})()
}

func (b *s3Backend) Done() {
	b.wg.Wait()
}

// NewS3Archiver returns a middleware that archives requests to an AWS S3
// bucket.  The request bodies are copied then written to S3 in a separate
// goroutine.  Requests are assumed to have a trace header (AKA request ID)
// implemented as the TraceHeaders middleware.  The ID will be appended to
// prefix to generate the key for the request document.
func NewS3Archiver(region, bucket, prefix string, opts ...Option) (midware.Middleware, error) {
	if prefix == "" {
		return nil, errors.New("NewS3Archiver: requires non-empty prefix")
	}
	cfg := &config{
		timeout:     defaultTimeout,
		traceHeader: midware.DefaultTraceHeader,
		logBase:     logrus.NewEntry(logrus.StandardLogger()),
	}
	for _, opt := range opts {
		opt(cfg)
	}
	a := &archiver{
		logBase:      cfg.logBase,
		ignoredPaths: cfg.ignoredPaths,
		traceHeader:  cfg.traceHeader,
	}
	awsCfg, err := awscfg.LoadDefaultConfig(
		context.TODO(),
		awscfg.WithRegion(region),
	)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(awsCfg)
	backend := &s3Backend{
		client:  client,
		bucket:  bucket,
		prefix:  prefix,
		timeout: cfg.timeout,
		log:     a.logReqID,
	}
	a.backend = backend
	return a, nil
}
