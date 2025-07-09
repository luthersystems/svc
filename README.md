# svc

A modular Go service framework for building robust, observable, and maintainable middleware systems—**designed to work seamlessly with the [Luther Platform](https://luthersystems.com).**

<https://pkg.go.dev/github.com/luthersystems/svc>

---

## Overview

This project provides a comprehensive set of building blocks for middleware services, combining:

- **Pluggable storage (Azure Blob, S3)**
- **gRPC logging/interceptors**
- **Email/mailer support**
- **Middleware and transaction context**
- **Tracing & observability**
- **Service orchestration ("oracle" pattern)**
- **Static asset serving**
- **Request archiving**

All modules are designed for composability and extensibility, making it easy to build custom microservices that integrate natively with the Luther Platform and its broader ecosystem.

---

## Features

- **Document Storage**: Azure Blob & S3 storage integrations (`docstore/`)
- **Templating**: Handlebars engine in Go (`libhandlebars/`)
- **Observability**: gRPC logging, OpenTracing, and middleware hooks (`grpclogging/`, `opttrace/`, `midware/`)
- **Email Delivery**: Pluggable email sender (`mailer/`)
- **Request Archiving**: Archive requests to S3, configurable options (`reqarchive/`)
- **Static Assets**: Serve and manage static content (`static/`)
- **Service Logic**: Oracle service with protobuf APIs, configuration, HTTP middleware (`oracle/`)
- **Error Handling**: Centralized error definitions (`svcerr/`)
- **Transaction Context**: Easy-to-use transaction-scoped context (`txctx/`)
- **Scripts**: Helper scripts for automation (`scripts/`)

---

## Directory Structure

```
svc/
├── docstore/       # Azure/S3 storage backends
├── grpclogging/    # gRPC logging/interceptor
├── libhandlebars/  # Handlebars templating (Go)
├── logmon/         # Log monitoring
├── mailer/         # Email sending
├── midware/        # Middleware interfaces
├── opttrace/       # Tracing/observability
├── oracle/         # Core business logic, HTTP, protobufs, phylum scripts
├── protos/         # Proto definitions
├── reqarchive/     # Request archiving, S3 integration
├── scripts/        # Utility scripts
├── static/         # Static asset serving
├── svc.go          # Main service entrypoint
├── svcerr/         # Error definitions
├── txctx/          # Transaction context
└── ...             # Build/config files
```

---

## Designed for the Luther Platform

**svc** is the recommended service foundation for building connectors, orchestration layers, and integrations within the Luther Platform and its ecosystem. All major components, APIs, and patterns are built to maximize interoperability and reliability for platform deployments.

---

## Getting Started

### Prerequisites

- Go 1.20+
- Make
- (Optional) Docker for building/testing in containers

### Build

```bash
make build
```

### Test

```bash
make test
```

### Run

```bash
go run svc.go
```

---

## Usage

Each component can be used independently or as part of a complete middleware service.
See the example configs and submodule READMEs (e.g., `libhandlebars/README.md`, `reqarchive/README.md`, `static/README.md`) for more details.

---

## Contributing

1. Fork this repo
2. Create a feature branch
3. Submit a pull request

---

## Maintainers

- [Luther Systems](https://github.com/luthersystems)
