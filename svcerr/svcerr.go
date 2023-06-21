// Copyright © 2021 Luther Systems, Ltd. All right reserved.

/*
Package svcerr is a library to register error handling HTTP middleware and convert various
error types defined in api/ into proper HTTP status codes.
*/
package svcerr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/luthersystems/protos/common"
	"github.com/luthersystems/svc/grpclogging"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/runtime/protoiface"
)

const (
	// TimestampFormat uses RFC3339 for all timestamps.
	TimestampFormat = time.RFC3339
)

// incExceptionMetric records prometheus metrics about a returned exception.
var incExceptionMetric func(*common.Exception)

var _ error = &lutherError{}

// lutherError represents a Luther managed error.
type lutherError struct {
	common.Exception
}

// Error implements error.
func (s *lutherError) Error() string {
	return s.GetDescription()
}

// NewUnexpectedError constructs an unexpected error.
func NewUnexpectedError(message string) *UnexpectedError {
	return &UnexpectedError{
		lutherError{
			*UnexpectedException(context.TODO(), message),
		},
	}
}

// UnexpectedError is a raw Luther expected business logic error.
type UnexpectedError struct {
	lutherError
}

// NewBusinessError constructs a business error.
func NewBusinessError(message string) *BusinessError {
	return &BusinessError{
		lutherError{
			*BusinessException(context.TODO(), message),
		},
	}
}

// BusinessError is a raw Luther expected business logic error.
type BusinessError struct {
	lutherError
}

// NewSecurityError constructs a security error.
func NewSecurityError(message string) *SecurityError {
	return &SecurityError{
		lutherError{
			*SecurityException(context.TODO(), message),
		},
	}
}

// SecurityError is a raw Luther security error.
type SecurityError struct {
	lutherError
}

// NewInfrastructureError constructs a infrastructure error.
func NewInfrastructureError(message string) *InfrastructureError {
	return &InfrastructureError{
		lutherError{
			*InfrastructureException(context.TODO(), message),
		},
	}
}

// InfrastructureError is a raw Luther infrastructure error.
type InfrastructureError struct {
	lutherError
}

// NewServiceError constructs a service error.
func NewServiceError(message string) *ServiceError {
	return &ServiceError{
		lutherError{
			*ServiceException(context.TODO(), message),
		},
	}
}

// ServiceError is a raw Luther service error.
type ServiceError struct {
	lutherError
}

func init() {
	{ // register exception type counts
		exceptionTotal := prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "exception_total",
				Help: "How many exception responses, partitioned by exception type.",
			},
			[]string{"type"},
		)
		incExceptionMetric = func(e *common.Exception) {
			exceptionTotal.WithLabelValues(e.GetType().String()).Inc()
		}
		prometheus.MustRegister(exceptionTotal)
	}
}

// raiser raises exceptions
type raiser interface {
	GetException() *common.Exception
}

func internalError(ctx context.Context) error {
	intStat, intErr := status.New(codes.Internal, "Internal server error").
		WithDetails(UnexpectedException(ctx, "Internal server error"))
	if intErr != nil {
		// This should never throw an error, and indicates a serious problem.
		panic(intErr)
	}
	return intStat.Err()
}

func grpcToLutherError(ctx context.Context, log grpclogging.ServiceLogger, err error) error {
	stat, ok := status.FromError(err)
	if !ok {
		// not a grpc error, but possibly a raw luther error.
		var eu *UnexpectedError
		var eb *BusinessError
		var es *SecurityError
		var ei *InfrastructureError
		var ev *ServiceError
		if errors.As(err, &eu) {
			stat = status.New(codes.Unknown, eu.Error())
		} else if errors.As(err, &eb) {
			stat = status.New(codes.InvalidArgument, eb.Error())
		} else if errors.As(err, &es) {
			stat = status.New(codes.PermissionDenied, es.Error())
		} else if errors.As(err, &ei) {
			stat = status.New(codes.Internal, ei.Error())
		} else if errors.As(err, &ev) {
			stat = status.New(codes.Unavailable, ev.Error())
		} else {
			// An unhandled error. A non-grpc wrapped error which we
			// assume has not yet been logged, and for which we must mask.
			// By convention this should not happen, however it can occur
			// if an error is accidently passed up the call stack without
			// transforming it to a gRPC error first. In any case, this
			// error is not conventional and should not be presented to the
			// caller.
			if !errors.Is(err, context.Canceled) {
				// ignore client cancelations of request
				log(ctx).WithError(err).Errorf("unhandled error")
			}
			return internalError(ctx)
		}
	}

	if len(stat.Details()) > 1 {
		// non-conventional error with more than one details
		log(ctx).WithError(err).Errorf("error with len(details)=%d", len(stat.Details()))
		return internalError(ctx)
	}

	if len(stat.Details()) == 1 {
		// case 2: already properly formed error.
		return err
	}

	var pbErr *common.Exception
	switch stat.Code() {
	case codes.OK:
		log(ctx).WithError(err).Errorf("OK status code in error")
		return internalError(ctx)
	case codes.Canceled:
		pbErr = UnexpectedException(ctx, stat.Message())
	case codes.Unknown:
		pbErr = UnexpectedException(ctx, stat.Message())
	case codes.InvalidArgument:
		pbErr = BusinessException(ctx, stat.Message())
	case codes.DeadlineExceeded:
		pbErr = UnexpectedException(ctx, stat.Message())
	case codes.NotFound:
		pbErr = BusinessException(ctx, stat.Message())
	case codes.AlreadyExists:
		pbErr = BusinessException(ctx, stat.Message())
	case codes.PermissionDenied:
		pbErr = SecurityException(ctx, stat.Message())
	case codes.ResourceExhausted:
		pbErr = UnexpectedException(ctx, stat.Message())
	case codes.FailedPrecondition:
		pbErr = BusinessException(ctx, stat.Message())
	case codes.Aborted:
		pbErr = InfrastructureException(ctx, stat.Message())
	case codes.OutOfRange:
		pbErr = BusinessException(ctx, stat.Message())
	case codes.Unimplemented:
		pbErr = ServiceException(ctx, stat.Message())
	case codes.Internal:
		pbErr = InfrastructureException(ctx, stat.Message())
	case codes.Unavailable:
		pbErr = ServiceException(ctx, stat.Message())
	case codes.DataLoss:
		pbErr = InfrastructureException(ctx, stat.Message())
	case codes.Unauthenticated:
		log(ctx).WithError(err).Infof("unauthenticated")
		// OWASP guidelines suggest only returning general error messages in
		// this case.
		pbErr = SecurityException(ctx, "unauthenticated")
	default:
		log(ctx).WithError(err).Errorf("unknown code")
		return internalError(ctx)
	}

	// case 3: coerced gRPC error into gRPC error with common.Exception
	// details.
	statDetails, statErr := stat.WithDetails(pbErr)
	if statErr != nil {
		log(ctx).WithError(statErr).Errorf("exception coercion")
		return internalError(ctx)
	}

	return statDetails.Err()
}

// AppErrorUnaryInterceptor intercepts all gRPC responses right before they're
// returned to the caller, and processes errors. Errors come in several
// different types, all of which are coerced into a gRPC Status error, with
// a details field set to a single element array with an element of type
// common.ExceptionResponse.
//
// By convention, the application should only return errors that fall into the
// following handled cases:
//
//   1) a response without an error has body with a populated `exception` field.
//      We inspect the exception object and construct a grpc error with the
//      appropriate status code and include the original exception proto message
//      in the gRPC error `details` field.
//
//   2) A response without a response body and with a gRPC error, where the
//      gRPC error has a `details` field populated containing a single element
//      of type common.Exception.
//
//   3) A response without a response body and with a gRPC error, where the
//      gRPC error does not have the `details` field populated.
//
// All other cases are a convention failure and indicate a bug in the error
// handling logic itself, which must be made conventional. Non-conventional
// errors must not be displayed to the user, as they indicate a bug that
// contains information not explicilty treated as presentable to the caller.
// Non-conventional errors are replaced with a generic "Internal server error"
// error, and must log the original error so that we can debug and remove them.
//
func AppErrorUnaryInterceptor(log grpclogging.ServiceLogger) func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Defer to the method's handler and save the results to pass through
		// for the interceptor's caller.
		resp, err := handler(ctx, req)

		// crack open response and see if it had an exception
		r, ok := resp.(raiser)
		if !ok {
			// We expect all response to have an optional "exception" field.
			log(ctx).WithError(err).Errorf("message response has wrong type")
			return nil, internalError(ctx)
		}

		if r.GetException() == nil && err == nil {
			// happy path, no errors
			return resp, nil
		}

		if r.GetException() != nil && err != nil {
			// by convention we never return both a response and an error
			log(ctx).WithError(err).Errorf("error and non-nil response object violates convention")
			return nil, internalError(ctx)
		}

		if r.GetException() != nil && err == nil {
			// coerce luther error into grpc/luther error
			var code codes.Code
			except := r.GetException()
			switch except.GetType() {
			case common.Exception_INVALID_TYPE:
				log(ctx).Errorf("exception missing type")
				code = codes.Internal // 500
			case common.Exception_BUSINESS:
				//code = codes.FailedPrecondition // Docs say this maps  to 400, but it maps to 412 unforuntately.
				// Unfortunately we use InvalidArgument, which is not really
				// correct, but does properly map to status 400.
				code = codes.InvalidArgument
			case common.Exception_SERVICE_NOT_AVAILABLE:
				code = codes.Unavailable // 503
			case common.Exception_INFRASTRUCTURE:
				code = codes.DataLoss // 500
			case common.Exception_UNEXPECTED:
				code = codes.Unknown // 500
			case common.Exception_SECURITY_VIOLATION:
				code = codes.PermissionDenied // 403
			default:
				log(ctx).Errorf("unknown exception type")
				code = codes.Internal // 500
			}
			var details proto.Message
			// HTTP 400 can contain payload
			if except.GetType() == common.Exception_BUSINESS && resp != nil {
				details = resp.(proto.Message)
			} else {
				details = except
			}
			msg, ok := details.(protoiface.MessageV1)
			if !ok {
				log(ctx).Errorf("wrong message type: %T", details)
				return nil, internalError(ctx)
			}
			stat, err := status.New(code, except.GetDescription()).WithDetails(msg)
			if err == nil {
				// case 1: we coerced a response with an exception into a proper
				// gRPC error.
				return nil, stat.Err()
			}
			// an error in the error handling :(
			log(ctx).WithError(err).Errorf("cannot create error status")
			return nil, internalError(ctx)
		}

		if r.GetException() == nil && err != nil {
			// coerce grpc error into grpc/luther error
			return nil, grpcToLutherError(ctx, log, err)
		}

		// this should never happen given the logic above.
		log(ctx).WithError(err).Errorf("impossible case")
		return nil, internalError(ctx)
	}
}

// HTTPErrorHandler is an interface for intercepting errors.
type HTTPErrorHandler = func(context.Context, *runtime.ServeMux, runtime.Marshaler, http.ResponseWriter, *http.Request, error)

// ErrIntercept intercepts error messages generated by the REST/JSON HTTP
// server. This includes errors already processed by AppErrorUnaryInterceptor,
// as well as errors generated by other endpoints.  This is the very last
// chance to process the error before it is presented to the caller!
func ErrIntercept(log grpclogging.ServiceLogger, handlers ...HTTPErrorHandler) HTTPErrorHandler {
	return func(ctx context.Context, mux *runtime.ServeMux, marshaler runtime.Marshaler, w http.ResponseWriter, r *http.Request, err error) {
		for _, handler := range handlers {
			handler(ctx, mux, marshaler, w, r, err)
		}
		w.Header().Set("Content-Type", marshaler.ContentType(nil))
		err = grpcToLutherError(ctx, log, err)
		stat, ok := status.FromError(err)
		if !ok || len(stat.Details()) != 1 {
			log(ctx).WithError(err).Errorf("unexpected error type, len(details)=%d", len(stat.Details()))
			w.WriteHeader(runtime.HTTPStatusFromCode(http.StatusInternalServerError))
			pbErr := &common.ExceptionResponse{
				Exception: UnexpectedException(ctx, "Internal server error"),
			}
			b, err := marshaler.Marshal(pbErr)
			if err != nil {
				log(ctx).WithError(err).Errorf("marshal unexpected error")
				b = []byte(cannedExceptionJSON(ctx))
			}
			_, err = w.Write(b)
			if err != nil {
				log(ctx).WithError(err).Errorf("write")
			}
			incExceptionMetric(pbErr.GetException())
			return
		}
		detail := stat.Details()[0]
		w.WriteHeader(runtime.HTTPStatusFromCode(stat.Code()))
		pbDetail, ok := detail.(*common.Exception)
		if !ok {
			// Propagate payload for non-exception detail
			b, err := marshaler.Marshal(detail)
			if err != nil {
				log(ctx).WithError(err).Errorf("marshal detail error")
				b = []byte(cannedExceptionJSON(ctx))
			}
			_, err = w.Write(b)
			if err != nil {
				log(ctx).WithError(err).Errorf("write")
			}
			return
		}
		pbErr := &common.ExceptionResponse{
			Exception: pbDetail,
		}
		b, err := marshaler.Marshal(pbErr)
		if err != nil {
			log(ctx).WithError(err).Errorf("marshal detail error")
			b = []byte(cannedExceptionJSON(ctx))
		}
		incExceptionMetric(pbErr.GetException())
		_, err = w.Write(b)
		if err != nil {
			log(ctx).WithError(err).Errorf("write")
		}
	}
}

// cannedExceptionJSON returns a hardcoded json string for an exception object.
// This is a fall back in extreme cases where we cannot marshal the exception
// object.
func cannedExceptionJSON(ctx context.Context) string {
	return fmt.Sprintf(`
{
    "exception": {
        "id": "%s",
        "type": "UNEXPECTED",
        "timestamp": "%s",
        "description": "Internal server error"
    }
}
 `, grpclogging.ReqID(ctx), time.Now().Format(TimestampFormat))
}

// UnexpectedException creates a protobuf unexpected exception.
func UnexpectedException(ctx context.Context, msg string) *common.Exception {
	return &common.Exception{
		Id:          grpclogging.ReqID(ctx),
		Type:        common.Exception_UNEXPECTED,
		Timestamp:   time.Now().Format(TimestampFormat),
		Description: msg,
	}
}

// BusinessException creates a protobuf business exception.
func BusinessException(ctx context.Context, msg string) *common.Exception {
	return &common.Exception{
		Id:          grpclogging.ReqID(ctx),
		Type:        common.Exception_BUSINESS,
		Timestamp:   time.Now().Format(TimestampFormat),
		Description: msg,
	}
}

// SecurityException creates a protobuf security exception.
func SecurityException(ctx context.Context, msg string) *common.Exception {
	return &common.Exception{
		Id:          grpclogging.ReqID(ctx),
		Type:        common.Exception_SECURITY_VIOLATION,
		Timestamp:   time.Now().Format(TimestampFormat),
		Description: msg,
	}
}

// InfrastructureException creates a protobuf infrastructure exception.
func InfrastructureException(ctx context.Context, msg string) *common.Exception {
	return &common.Exception{
		Id:          grpclogging.ReqID(ctx),
		Type:        common.Exception_INFRASTRUCTURE,
		Timestamp:   time.Now().Format(TimestampFormat),
		Description: msg,
	}
}

// ServiceException creates a protobuf service exception.
func ServiceException(ctx context.Context, msg string) *common.Exception {
	return &common.Exception{
		Id:          grpclogging.ReqID(ctx),
		Type:        common.Exception_SERVICE_NOT_AVAILABLE,
		Timestamp:   time.Now().Format(TimestampFormat),
		Description: msg,
	}
}
