# platform-mesh-controller Helm chart

The chart deploys the controller that integrates OpenKCM into
[Platform Mesh](https://platform-mesh.io/). The controller watches the account workspaces of a
kcp control plane through the `operations.openkcm.io` APIExport, creates a Tenant for each
account that binds the APIExport from the marketplace, and reconciles the key resources of the
accounts (Tenant, DomainKey, ServiceKey, the root keys for AWS, Azure and OpenBao,
DataEncryptionKey) against OpenKCM.

The APIExport, its schemas and the portal content come from the
[pm-integration](../pm-integration) chart, installed into the kcp provider workspace.

> [!WARNING]
> The controller is in development. By default every reconciler talks to an in-pod mock of
> the OpenKCM API. Set `krypton.grpcAddr` to bind the Tenant, DomainKey, ServiceKey and
> DataEncryptionKey reconcilers to Krypton.

[values.yaml](values.yaml) documents every value and the controller's flags.

## What the chart creates

| Resource | Purpose |
| --- | --- |
| Deployment | The controller pod (container `manager`) |
| CRDs | The `operations.openkcm.io` CRDs (directory `crds`, installed by Helm on the first installation only) |
| Service | `<full name>-metrics`: metrics (8080, plain HTTP) and health endpoints (8081) |
| ServiceAccount | Identity of the pod. Its token is mounted: the mock API keeps its state in a ConfigMap |
| ClusterRole and ClusterRoleBinding | Optional (`global.rbac.create`, on by default): the permissions of the controller's RBAC markers |
| Role and RoleBinding | Optional (`global.rbac.create`): leases and ConfigMaps in the release namespace |
| NetworkPolicy | Optional (`networkPolicy.enabled`) |
| PodDisruptionBudget | Optional (`podDisruptionBudget.enabled`) |
| ServiceMonitor | Optional (`prometheus.servicemonitor.enabled`) |

## Prerequisites

- Kubernetes 1.22 or later, Helm 3.
- A kcp control plane with Platform Mesh, and the pm-integration chart installed into the
  OpenKCM provider workspace.
- A kubeconfig for that provider workspace, with a user that may read the
  APIExportEndpointSlice and the virtual workspaces of the APIExport.

## Install

Create the Secret with the kcp kubeconfig, under the key `kubeconfig`:

```sh
kubectl create namespace platform-mesh-controller-system
kubectl -n platform-mesh-controller-system create secret generic pm-kubeconfig \
  --from-file=kubeconfig=openkcm-provider.kubeconfig
```

Install the chart, here with Krypton:

```sh
helm install platform-mesh-controller oci://ghcr.io/openkcm/charts/platform-mesh-controller \
  --version 0.11.0 -n platform-mesh-controller-system \
  --set krypton.grpcAddr=krypton-root.services.svc:8080
```

The release notes show the connections and warn about values that leave reconcilers on the mock
API or block node drains. Check the controller:

```sh
kubectl -n platform-mesh-controller-system logs deployment/platform-mesh-controller
```

## Important values

| Value | Default | Description |
| --- | --- | --- |
| `image.tag` / `image.digest` | `v0.0.3` | The controller release to deploy |
| `kcpKubeconfig.secretName` | `pm-kubeconfig` | Secret with the kcp kubeconfig (key `kubeconfig`). Required |
| `operations.apiExportEndpointSliceName` | `operations.openkcm.io` | The APIExportEndpointSlice of the APIExport |
| `tenantNamespace` | `default` | Namespace of the Tenant and keys in each account workspace |
| `krypton.grpcAddr` | `""` | Krypton's gRPC API; empty: the mock API |
| `openkcmAPI.url` | the in-pod mock API | OpenKCM API of the root key reconcilers |
| `extraArgs` | `[]` | Other flags, for example `--default-tenant-region` or `--zap-devel=false` |
| `replicaCount` | `1` | Keep 1: the chart does not enable leader election |

## Connections

| From the controller to | Configured by | Transport |
| --- | --- | --- |
| kcp | `kcpKubeconfig.secretName` | As in the kubeconfig |
| Krypton | `krypton.grpcAddr` | Plaintext gRPC: inside a service mesh with mutual TLS |
| OpenKCM API | `openkcmAPI.url` | HTTP(S) as in the URL |
| Kubernetes API of the hosting cluster | ServiceAccount token | The mock API's state ConfigMap |

With `networkPolicy.enabled`, add the port of Krypton (8080) to `networkPolicy.egress`.

## Security defaults

| Setting | Default |
| --- | --- |
| Pod | User and group 65532 (non-root), `RuntimeDefault` seccomp profile, no Service environment variables |
| Container | No privilege escalation, all capabilities dropped, read-only root filesystem, writable `/tmp` |
| Mock API | Listens on `127.0.0.1` only |
| Metrics | Plain HTTP inside the cluster; `--metrics-secure` in `extraArgs` serves them over HTTPS with authentication |

The controller works on kcp with the credentials of the kubeconfig. In the hosting cluster its
ServiceAccount uses only the Role (the mock API's ConfigMap). Keep the kubeconfig's user limited
to the provider workspace.

## Upgrading from 0.10.x

| Change | Action |
| --- | --- |
| The liveness probe is enabled | Set `livenessProbe.enabled: false` for the old behavior |
| The pod runs explicitly as user and group 65532, the user of the release image | None |
| A writable `/tmp` volume and ephemeral-storage requests and limits | Override `tmpVolume` and `resources` if needed |
| The release notes list the connections and warnings | None |

Resource names and selector labels are unchanged. Helm does not upgrade CRDs: apply the CRDs
of a new release with `kubectl apply -f charts/platform-mesh-controller/crds/`.

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| Pod stays in `ContainerCreating` with `secret "pm-kubeconfig" not found` | The Secret of `kcpKubeconfig.secretName` does not exist |
| Log `Failed to load KCP kubeconfig` | The Secret has no key `kubeconfig` |
| Log `Failed to create operations APIExport provider` | The kubeconfig does not point to the provider workspace, or `operations.apiExportEndpointSliceName` is wrong |
| Log `Failed to connect to Krypton` | `krypton.grpcAddr` is not a valid `host:port` |
| Keys stay in the mock after a restart of the pod | Expected without `krypton.grpcAddr`; the mock keeps its state in the ConfigMap `openkcm-mock-state` |

## Uninstall

```sh
helm uninstall platform-mesh-controller -n platform-mesh-controller-system
```

Helm keeps the CRDs; delete them with `kubectl delete -f charts/platform-mesh-controller/crds/`
only when no account uses OpenKCM any more.
