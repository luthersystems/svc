// Copyright © 2021 Luther Systems, Ltd. All right reserved.

/*
HTTP interceptors for grpc-middleware for logging.
*/
package grpclogging

import (
	"context"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

// ServiceLogger defines a function that returns a logrus.Entry from a
// context.Context
type ServiceLogger = func(ctx context.Context) *logrus.Entry

// ReqID gets a request ID from the supplied context's log fields, if present
func ReqID(ctx context.Context) string {
	fields := GetLogrusFields(ctx)
	if fields["req_id"] != nil {
		rID, _ := fields["req_id"].(string)
		return rID
	}
	return ""
}

// LogrusMethodInterceptor returns a middleware that associates logrus.Fields
// with a handler's context.Context, accessible through func GetLogrusEntry(),
// and automatically logs method metadata.
func LogrusMethodInterceptor(base *logrus.Entry, t Timer, now Time) grpc.UnaryServerInterceptor {
	// Middleware to log details about method calls.
	return newGRPCMethodLogInterceptor(base, t, now)
}
