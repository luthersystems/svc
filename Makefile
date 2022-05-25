# Copyright © 2021 Luther Systems, Ltd. All right reserved.

# Makefile
#
# The primary project makefile that should be run from the root directory and is
# able to build and run the entire application.

.DEFAULT_GOAL := default
.PHONY: default
default: all

.PHONY: all
all:
	@

.PHONY: citest
citest: test
	@

GO_TEST_BASE=go test ${GO_TEST_FLAGS}
GO_TEST_TIMEOUT_10=${GO_TEST_BASE} -timeout 10m

.PHONY: go-test
go-test:
	${GO_TEST_TIMEOUT_10} ./...

.PHONY: static-checks
static-checks:
	./scripts/static-checks.sh

.PHONY: test
test: static-checks go-test
	@
