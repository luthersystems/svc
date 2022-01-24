// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package reqarchive

import (
	"time"

	"github.com/sirupsen/logrus"
)

// Option represents an Archiver configuration option
type Option func(*config)

type config struct {
	logBase      *logrus.Entry
	ignoredPaths map[string]bool
	timeout      time.Duration
	traceHeader  string
}

// WithLogBase sets a base logrus Entry for logging of errors.
func WithLogBase(logBase *logrus.Entry) Option {
	return func(cfg *config) {
		cfg.logBase = logBase
	}
}

// WithIgnoredPath sets a URL path that will skipped by the archiver.  It can be
// called more than once.
func WithIgnoredPath(path string) Option {
	return func(cfg *config) {
		if cfg.ignoredPaths == nil {
			cfg.ignoredPaths = make(map[string]bool, 1)
		}
		cfg.ignoredPaths[path] = true
	}
}

// WithTimeout sets the timeout for archival goroutines.  Defaults to 1 minute.
func WithTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		cfg.timeout = timeout
	}
}

// WithTraceHeader overrides the default trace header.
func WithTraceHeader(header string) Option {
	return func(cfg *config) {
		cfg.traceHeader = header
	}
}
