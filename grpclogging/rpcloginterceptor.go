// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package grpclogging

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func isHealthCheck(method string) bool {
	lowerCaseMethod := strings.ToLower(method)
	return strings.Contains(lowerCaseMethod, "healthcheck")
}

// newGRPCMethodLogInterceptor returns a grpc.UnaryServerInterceptor that logs
// the grpc method being handled and its duration. A debug message is printed
// at the beginning of a handler's execution and its duration is logged at the
// end
func newGRPCMethodLogInterceptor(base *logrus.Entry, t Timer, lutherTime Time) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		var nowFn func() time.Time
		if lutherTime != nil {
			nowFn = lutherTime.Now
		}
		// The start time includes setup for and logging
		stopTimer := t.StartTimer(nowFn)

		reqID := uuid.New().String()
		md, ok := metadata.FromIncomingContext(ctx)
		if ok {
			mdID := md["x-request-id"]
			if len(mdID) > 0 {
				reqID = mdID[0]
			}
		}
		ctx = newContextWithFields(ctx, logrus.Fields{
			"rpc_method": info.FullMethod,
			"req_id":     reqID,
		})
		GetLogrusEntry(ctx, base).Debug("RPC method begin")

		span := trace.SpanFromContext(ctx)
		span.SetAttributes(attribute.String("app.request.id", reqID))

		// Defer to the method's handler and save the results to pass through
		// for the interceptor's caller.
		resp, err := handler(ctx, req)

		// Create a logrus.Entry with additional (and potentially modified)
		// fields to describe the completed RPC.
		mLog := GetLogrusEntry(ctx, base)
		if err != nil {
			mLog = mLog.WithError(err)
		}
		// Compute call duration as late as possible to give the most accurate
		// representation of the call duration (excluding network
		// transmission).
		dur := stopTimer()
		mLog = mLog.WithField("rpc_dur", dur)

		if isHealthCheck(info.FullMethod) {
			mLog.Debug("RPC method called")
		} else {
			mLog.Info("RPC method called")
		}

		return resp, err
	}
}
