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
citest: plugin go-test
	@

GO_TEST_BASE=go test ${GO_TEST_FLAGS}
GO_TEST_TIMEOUT_10=${GO_TEST_BASE} -timeout 10m

.PHONY: go-test
go-test:
	${GO_TEST_TIMEOUT_10} ./...

${STATIC_PLUGINS_DUMMY}:
	${MKDIR_P} $(dir $@)
	./scripts/obtain-plugin.sh
	touch $@

${SUBSTRATE_PLUGIN}: ${STATIC_PLUGINS_DUMMY}
	@

