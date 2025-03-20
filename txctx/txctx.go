package txctx

import (
	"context"
)

type key struct{}
type value struct {
	txDetails   TransactionDetails
	authDetails AuthDetails
}

// TransactionDetails captures transaction execution details.
type TransactionDetails struct {
	TransactionID  string
	CommitBlockNum uint64
	MaxSimBlockNum uint64
}

// AuthDetails captures authentication details.
type AuthDetails struct {
	AuthToken string
}

// Contex constructs a context for storing svc data.
func Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, key{}, &value{})
}

// SetTransactionDetails sets the transaction details in a context value that has been initialized
// using Context.
func SetTransactionDetails(ctx context.Context, details TransactionDetails) {
	if val, ok := ctx.Value(key{}).(*value); ok {
		val.txDetails = details
	}
}

// GetTransactionDetails gets the transaction details from a context value if present
func GetTransactionDetails(ctx context.Context) TransactionDetails {
	if val, ok := ctx.Value(key{}).(*value); ok {
		return val.txDetails
	}
	return TransactionDetails{}
}

func SetAuthDetails(ctx context.Context, details AuthDetails) {
	if val, ok := ctx.Value(key{}).(*value); ok {
		val.authDetails = details
	}
}

func GetAuthDetails(ctx context.Context) AuthDetails {
	if val, ok := ctx.Value(key{}).(*value); ok {
		return val.authDetails
	}
	return AuthDetails{}
}
