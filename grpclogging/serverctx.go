// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package grpclogging

import (
	"context"
	"sync"

	"github.com/sirupsen/logrus"
)

// logMetadataCtxKey is a key to store logging data within context.
type logMetadataCtxKey struct{}

// ctxSetLogMetadata adds logging metadata to context.
func ctxSetLogMetadata(ctx context.Context, fields logrus.Fields) context.Context {
	fieldMap := new(sync.Map)
	for key, val := range fields {
		fieldMap.Store(key, val)
	}
	return context.WithValue(ctx, logMetadataCtxKey{}, fieldMap)
}

// ctxGetLogMetadata retrieves logging metadata from context.
func ctxGetLogMetadata(ctx context.Context) *sync.Map {
	val, _ := ctx.Value(logMetadataCtxKey{}).(*sync.Map)
	return val
}

// GetLogrusFields returns stored logging metadata.
func GetLogrusFields(ctx context.Context) logrus.Fields {
	fields := logrus.Fields{}
	fieldMap := ctxGetLogMetadata(ctx)
	if fieldMap == nil {
		return fields
	}
	fieldMap.Range(func(key, val interface{}) bool {
		if keyStr, ok := key.(string); ok {
			fields[keyStr] = val
		}
		return true
	})
	return fields
}

// GetLogrusEntry returns stored logging metadata as a logrus Entry.
func GetLogrusEntry(ctx context.Context, base *logrus.Entry) *logrus.Entry {
	fields := GetLogrusFields(ctx)
	if fields != nil {
		return base.WithFields(fields)
	}
	return base
}

// AddLogrusField adds a log field to the request context for later retrieval.
// This is intended to be used from a handler once `LogrusMethodInterceptor` has
// been used to initialize the context.
func AddLogrusField(ctx context.Context, key, value string) {
	fieldMap := ctxGetLogMetadata(ctx)
	if fieldMap == nil {
		return
	}
	fieldMap.Store(key, value)
}
