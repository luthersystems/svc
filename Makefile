# Copyright © 2021 Luther Systems, Ltd. All right reserved.

# Makefile
#
# The primary project makefile that should be run from the root directory and is
# able to build and run the entire application.
#
PROJECT_REL_DIR=.
include ${PROJECT_REL_DIR}/common.mk
BUILD_IMAGE_PROJECT_DIR=/go/src/${PROJECT_PATH}

SUBSTRATEHCP_FILE ?= ${PWD}/${SUBSTRATE_PLUGIN_PLATFORM_TARGETED}

export SUBSTRATEHCP_FILE

.DEFAULT_GOAL := default
.PHONY: default
default: all

.PHONY: all
all:
	@

all: plugin
.PHONY: plugin plugin-linux plugin-darwin
plugin: ${SUBSTRATE_PLUGIN}

plugin-linux: ${SUBSTRATE_PLUGIN_LINUX}

plugin-darwin: ${SUBSTRATE_PLUGIN_DARWIN}

.PHONY: citest
citest: plugin go-test go-test-json-v1
	@

GO_TEST_BASE=go test ${GO_TEST_FLAGS}
GO_TEST_TIMEOUT_10=${GO_TEST_BASE} -timeout 10m

.PHONY: go-test
go-test:
	${GO_TEST_TIMEOUT_10} ./...

# libhandlebars copies encoding/json's v1 engine; Go 1.27 defaults to v2
# (luthersystems/svc#147). Its tests compare with encoding/json only on v1,
# so run them on v1 too.
.PHONY: go-test-json-v1
go-test-json-v1:
	GOEXPERIMENT=nojsonv2 ${GO_TEST_TIMEOUT_10} ./libhandlebars/...

# Lint as CI does (golangci-lint v2.15.0, the pin of the shared Quality workflow in .github/workflows/quality.yml).
# `config verify` checks .golangci.yml against golangci-lint's schema first:
# `run` accepts a misspelled key and silently ignores it.
.PHONY: static-checks
static-checks:
	golangci-lint config verify
	golangci-lint run ./...

${STATIC_PLUGINS_DUMMY}:
	${MKDIR_P} $(dir $@)
	./scripts/obtain-plugin.sh
	touch $@

${SUBSTRATE_PLUGIN}: ${STATIC_PLUGINS_DUMMY}
	@

