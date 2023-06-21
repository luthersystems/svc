package svcerr

import (
	"context"
	"fmt"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRawError(t *testing.T) {
	entry := logrus.NewEntry(logrus.New())
	log := func(ctx context.Context) *logrus.Entry {
		return entry
	}
	ctx := context.Background()

	t.Run("internal", func(t *testing.T) {
		err := grpcToLutherError(ctx, log, fmt.Errorf("unknown error"))
		stat, ok := status.FromError(err)
		require.True(t, ok, "expected ok status")
		require.Equal(t, stat.Code(), codes.Internal)
		require.Len(t, stat.Details(), 1)
	})

	t.Run("internal (canceled)", func(t *testing.T) {
		err := grpcToLutherError(ctx, log, status.Error(codes.Canceled, context.Canceled.Error()))
		stat, ok := status.FromError(err)
		require.True(t, ok, "expected ok status")
		require.Equal(t, stat.Code(), codes.Canceled)
		require.Len(t, stat.Details(), 1)
	})

	t.Run("unexpected", func(t *testing.T) {
		err := fmt.Errorf("error: %w", NewUnexpectedError("unexpected"))
		require.Equal(t, "error: unexpected", err.Error())
		err = grpcToLutherError(ctx, log, err)
		stat, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, stat.Code(), codes.Unknown)
		require.Len(t, stat.Details(), 1)
	})

	t.Run("business", func(t *testing.T) {
		err := fmt.Errorf("error: %w", NewBusinessError("business"))
		require.Equal(t, "error: business", err.Error())
		err = grpcToLutherError(ctx, log, err)
		stat, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, stat.Code(), codes.InvalidArgument)
		require.Len(t, stat.Details(), 1)
	})

	t.Run("security", func(t *testing.T) {
		err := fmt.Errorf("error: %w", NewSecurityError("security"))
		require.Equal(t, "error: security", err.Error())
		err = grpcToLutherError(ctx, log, err)
		stat, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, stat.Code(), codes.PermissionDenied)
		require.Len(t, stat.Details(), 1)
	})

	t.Run("infrastructure", func(t *testing.T) {
		err := fmt.Errorf("error: %w", NewInfrastructureError("infrastructure"))
		require.Equal(t, "error: infrastructure", err.Error())
		err = grpcToLutherError(ctx, log, err)
		stat, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, stat.Code(), codes.Internal)
		require.Len(t, stat.Details(), 1)
	})

	t.Run("service", func(t *testing.T) {
		err := fmt.Errorf("error: %w", NewServiceError("service"))
		require.Equal(t, "error: service", err.Error())
		err = grpcToLutherError(ctx, log, err)
		stat, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, stat.Code(), codes.Unavailable)
		require.Len(t, stat.Details(), 1)
	})

}
