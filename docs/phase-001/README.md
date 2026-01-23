# Phase 001: Research & Design

This phase covers the initial research, spike investigations, and architectural design for connect-plugin-go.

## Overview

connect-plugin-go combines HashiCorp go-plugin's interface-oriented design with Connect RPC's HTTP transport to enable plugins running as remote services (sidecars, containers, separate hosts).

## Initial Analysis

Foundation research documents:

1. **[go-plugin Analysis](01-go-plugin-analysis.md)** - Deep dive into HashiCorp go-plugin architecture
2. **[Connect RPC Analysis](02-connect-rpc-analysis.md)** - Analysis of Connect RPC features and design
3. **[Project Thesis](03-project-thesis.md)** - Vision, architecture, and design decisions

## Research Spikes

Detailed investigations of specific technical areas:

### Core Patterns
- **[KOR-fzki: go-plugin Handshake & Lifecycle](spike-fzki-go-plugin-handshake.md)** - Plugin negotiation and lifecycle management
- **[KOR-lfks: go-plugin GRPCBroker Bidirectional](spike-lfks-go-plugin-grpcbroker.md)** - Host→plugin callback patterns
- **[KOR-yosi: Connect Protocol Internals](spike-yosi-connect-protocol-internals.md)** - Wire format, envelopes, headers, error codes
- **[KOR-bpyd: Connect Interceptors & Middleware](spike-bpyd-connect-interceptors.md)** - Interceptor patterns for cross-cutting concerns

### Integration Patterns
- **[KOR-adry: Capability-Based Dynamic Dispatch](spike-adry-capability-dispatch.md)** - OCap model for host services
- **[KOR-cldj: fx Integration Patterns](spike-cldj-fx-integration.md)** - Dependency injection with uber-go/fx
- **[KOR-neyu: Code Generation Architecture](spike-neyu-codegen-architecture.md)** - Plugin proxy generation tool

### Operational Concerns
- **[KOR-gdru: Authentication & Authorization](spike-gdru-authentication-authorization.md)** - Multi-layer auth (mTLS, tokens, OIDC)
- **[KOR-munj: Service Discovery Patterns](spike-munj-service-discovery.md)** - DNS, Kubernetes, scheme-based resolution
- **[KOR-ejeu: Health Checking Patterns](spike-ejeu-health-checking.md)** - gRPC health protocol, monitoring, probes

## Design Documents

Architectural specifications (see `design/` directory):

- [Core Plugin Interface & Type System](design/core-plugin-interface.md) - TBD
- [Handshake Protocol for Network](design/handshake-protocol.md) - TBD
- [Client Configuration & Connection Model](design/client-configuration.md) - TBD
- [Server Configuration & Lifecycle](design/server-configuration.md) - TBD
- [Streaming Adapter Patterns](design/streaming-adapters.md) - TBD
- [Bidirectional Broker Architecture](design/bidirectional-broker.md) - TBD
- [fx Integration Layer](design/fx-integration.md) - TBD
- [Plugin Criticality & Failure Modes](design/failure-modes.md) - TBD

## Key Findings

### From Research

1. **Protocol Choice**: Connect protocol over gRPC/gRPC-Web for HTTP/1.1 compatibility and browser support
2. **Handshake**: Network-based negotiation (vs go-plugin's stdout/stdin)
3. **Bidirectional**: Capability-based broker (vs go-plugin's GRPCBroker)
4. **Discovery**: Pluggable (static, DNS, Kubernetes)
5. **Health**: gRPC health protocol compatibility
6. **Auth**: Multi-layer (transport + application tokens)
7. **Codegen**: protoc-gen-connect-plugin for typed proxies

### Architecture Principles

1. **Interface parity**: Same Go interfaces whether plugin is local or remote
2. **Pluggable discovery**: Abstract endpoint resolution
3. **Resilience**: Retry, circuit breakers, health checks built-in
4. **Security**: Flexible auth (mTLS, JWT, OIDC)
5. **Observability**: Interceptors for metrics, tracing, logging
6. **Kubernetes-native**: First-class sidecar and service mesh support

## Next Steps

1. Complete design documents (KOR-gfuh, KOR-mbgw, KOR-qjhn, etc.)
2. Define protobuf schemas for core services
3. Implement MVP (Phase 002):
   - Core plugin types and interfaces
   - Handshake service
   - Static discovery
   - Basic health checking
   - Simple example plugin
