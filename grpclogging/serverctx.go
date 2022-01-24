// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package grpclogging

import (
	"context"
	"sync"

	"github.com/sirupsen/logrus"
)

// logMetadataCtxKey is a key to store logging data within context.
type logMetadataCtxKey struct{}

// NewContext returns a new context initialized with logging metadata.
func NewContext(ctx context.Context) context.Context {
	fieldMap := new(sync.Map)
	return context.WithValue(ctx, logMetadataCtxKey{}, fieldMap)
}

func newContextWithFields(ctx context.Context, fields logrus.Fields) context.Context {
	newCtx := NewContext(ctx)
	AddLogrusFields(newCtx, fields)
	return newCtx
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

// AddLogrusField adds a log field to the supplied context for later retrieval.
// The context must have been previously initialized with log metadata via
// `LogrusMethodInterceptor` or `NewContext`.
func AddLogrusField(ctx context.Context, key, value string) {
	fieldMap := ctxGetLogMetadata(ctx)
	if fieldMap == nil {
		return
	}
	fieldMap.Store(key, value)
}

// AddLogrusField adds log fields to the supplied context for later retrieval.
// The context must have been previously initialized with log metadata via
// `LogrusMethodInterceptor` or `NewContext`.
func AddLogrusFields(ctx context.Context, fields logrus.Fields) {
	fieldMap := ctxGetLogMetadata(ctx)
	if fieldMap == nil {
		return
	}
	for key, val := range fields {
		fieldMap.Store(key, val)
	}
}
