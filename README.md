# openkcm-controller

[![REUSE status](https://api.reuse.software/badge/github.com/openkcm/openkcm-controller)](https://api.reuse.software/info/github.com/openkcm/openkcm-controller)

Kubernetes controller that connects Platform Mesh to OpenKCM Krypton.

## About this project

Platform Mesh exposes accounts and namespaces as workspaces in a kcp control
plane. This controller watches OpenKCM custom resources in those workspaces and
reconciles them against the Krypton key management service.

It manages ten resource types under the `operations.openkcm.io` API group:

| Resource | Purpose |
|---|---|
| `Tenant` | tenant registration, derived from the workspace path |
| `DomainKey` | L2, one per tenant namespace |
| `ServiceKey` | L3 |
| `DataEncryptionKey` | L4 |
| `AWSRootKey`, `AzureRootKey`, `GCPRootKey`, `HSMRootKey`, `OpenBaoRootKey`, `VaultRootKey` | L1, one per external key store |

The controller runs against a kcp virtual workspace using
[multicluster-runtime](https://github.com/kcp-dev/multicluster-provider), so a
single deployment serves every account workspace.

> **Note:** the Krypton client is not implemented yet. By default the controller
> serves an in-process mock on `127.0.0.1:9443` and reconciles against it. Point
> `openkcmAPI.url` at a real endpoint once the gRPC client lands.

## Charts

| Chart | What it installs |
|---|---|
| `charts/operator` | the controller deployment, RBAC and CRDs |
| `charts/pm-integration` | kcp metadata: `APIExport`, `APIResourceSchema`, `ProviderMetadata`, `ContentConfiguration`. No workloads |

The microfrontend served to the Platform Mesh portal stays in the Showroom
repository and is contributed to there; only the controller moved here.
`pm-integration` references it by URL only.

## Requirements and Setup

### Prerequisites

- go 1.25.3
- kubectl 1.11.3+
- a kcp control plane with the Platform Mesh API surface
- helm 3

### Build and test

```sh
make build
make test
make lint
```

`make test` needs envtest binaries:

```sh
make envtest
```

### Install

```sh
helm install pm-integration charts/pm-integration --kubeconfig <kcp-kubeconfig>
helm install operator charts/operator -n openkcm-controller-system --create-namespace
```

The controller reads its kcp credentials from the secret named in
`kcpKubeconfig.secretName`.

### Regenerate manifests

```sh
make manifests
```

This regenerates CRDs into `config/crd/bases` and copies them into
`charts/operator/crds`. Keep both in sync; the chart CRDs are what actually gets
installed.

## Support, Feedback, Contributing

This project is open to feature requests, suggestions and bug reports via
[GitHub issues](https://github.com/openkcm/openkcm-controller/issues).
Contribution and feedback are encouraged and always welcome. For more
information about how to contribute, see our
[Contribution Guidelines](CONTRIBUTING.md).

## Security / Disclosure

If you find any bug that may be a security problem, please follow our
instructions [in our security policy](https://github.com/openkcm/openkcm-controller/security/policy)
on how to report it. Please do not create GitHub issues for security-related
doubts or problems.

## Code of Conduct

We as members, contributors, and leaders pledge to make participation in our
community a harassment-free experience for everyone. By participating in this
project, you agree to abide by its [Code of Conduct](https://github.com/SAP/.github/blob/main/CODE_OF_CONDUCT.md)
at all times.

## Licensing

Copyright 2026 SAP SE or an SAP affiliate company and OpenKCM contributors.
Please see our [LICENSE](LICENSE) for copyright and license information.
Detailed information including third-party components and their
licensing/copyright information is available
[via the REUSE tool](https://api.reuse.software/info/github.com/openkcm/openkcm-controller).

---

<p align="center"><img alt="Bundesministerium für Wirtschaft und Energie (BMWE)-EU funding logo" src="https://apeirora.eu/assets/img/BMWK-EU.png" width="400"/></p>
