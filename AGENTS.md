# platform-mesh-controller

OpenKCM provider integration for Platform Mesh Showroom.

## Build
- `make manifests` -- generate CRD manifests
- `make generate` -- generate deepcopy methods
- `make build` -- build the controller binary
- `make docker-build` -- build container image
- `make test` -- run unit tests

## Architecture
- Controller uses multicluster-runtime (no sync agent)
- Watches KCP workspaces via APIExport virtual workspaces
- Reconciles Tenant, DomainKey, ServiceKey against OpenKCM API
- Mock OpenKCM API server runs in the same pod

## CRDs
- `Tenant` (operations.openkcm.io/v1alpha1) -- org level
- `DomainKey` (operations.openkcm.io/v1alpha1) -- account level
- `ServiceKey` (operations.openkcm.io/v1alpha1) -- account level
