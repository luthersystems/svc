# Copyright © 2025 Luther Systems, Ltd. All right reserved.

SUBSTRATE_VERSION=v3.0.1-SNAPSHOT.16-3df834e3

# A golang module proxy server can greatly help speed up docker builds but the
# official proxy at https://proxy.golang.org only works for public modules.
# When your application needs private go module dependencies consider running a
# local athens-proxy server with an ssh/http configuration which can access
# private source repositories, otherwise set GOPRIVATE (or GONOPROXY and
# GONOSUMDB) if private modules are needed.  Though be aware that GOPRIVATE
# requires credentials (e.g. for github ssh) be available during builds which
# complicates things considerably.
# 		https://docs.gomods.io/
# 		https://golang.org/ref/mod#private-modules
GOPROXY ?= https://proxy.golang.org
GOPRIVATE ?=
GONOPROXY ?= ${GOPRIVATE}
GONOSUMDB ?= ${GOPRIVATE}
