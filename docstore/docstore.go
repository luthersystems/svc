// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package docstore

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
)

var (
	// ErrRequestNotFound is returned when a request is not found
	ErrRequestNotFound = fmt.Errorf("key not found")
)

// Getter gets documents.
type Getter interface {
	// Get retrieves the document.
	Get(ctx context.Context, key string) ([]byte, error)
}

// Putter stores documents.
type Putter interface {
	// Put stores the document.
	Put(ctx context.Context, key string, body []byte) error
}

// Deleter deletes documents.
type Deleter interface {
	// Put stores the document.
	Delete(ctx context.Context, key string) error
}

// DocStore provides document services.
type DocStore interface {
	Getter
	Putter
	Deleter
}

var validKeyRegexp = regexp.MustCompile(`^[a-zA-Z0-9_./()-]*$`)

// ValidKey returns an error if the key is invalid.
func ValidKey(key string) error {
	if key == "" {
		return fmt.Errorf("missing key")
	}
	if !validKeyRegexp.MatchString(key) {
		return fmt.Errorf("invalid key")
	}
	if key != strings.TrimPrefix(path.Join("/", key), "/") {
		// *IMPORTANT:* we sanitize the key by first turning it into an
		// absolute path.
		// If the key is not the same after sanitization then potential
		// path traversal.
		// Note path.Join calls Clean on the path.
		return fmt.Errorf("invalid path")
	}
	return nil
}
