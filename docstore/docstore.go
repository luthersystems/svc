// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package docstore

import (
	"fmt"
)

var (
	// ErrRequestNotFound is returned when a request is not found
	ErrRequestNotFound = fmt.Errorf("key not found")
)

// Getter gets documents.
type Getter interface {
	// Get retrieves the document.
	Get(key string) ([]byte, error)
}

// Putter stores documents.
type Putter interface {
	// Put stores the document.
	Put(key string, body []byte) error
}

// DocStore provides document services.
type DocStore interface {
	Getter
	Putter
}
