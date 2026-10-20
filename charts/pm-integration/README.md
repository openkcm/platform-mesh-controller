# pm-integration Helm chart

The chart makes OpenKCM a provider in [Platform Mesh](https://platform-mesh.io/). It contains no
workloads: install it into the OpenKCM provider workspace of the kcp control plane, with that
workspace's kubeconfig.

| Resource | Purpose |
| --- | --- |
| APIExport `operations.openkcm.io` | The OpenKCM resources (Tenant, DomainKey, ServiceKey, the root keys, DataEncryptionKey) that accounts bind from the marketplace |
| APIResourceSchemas `v<chart version>-v1alpha1.<resource>.operations.openkcm.io` | The schemas of the APIExport, generated from the controller's CRDs |
| ProviderMetadata `operations.openkcm.io` | The marketplace entry (`branding`) |
| ContentConfigurations `openkcm`, `openkcm-namespace` | Where the portal loads the OpenKCM microfrontend's navigation from (`publicScheme`, `publicHost`, `metadata`) |
| ClusterRole `apiexport-bind`, ClusterRoleBinding `anonymous-view` | Allow the portal to bind the APIExport for an account, as for every Platform Mesh provider |
| ClusterRole and ClusterRoleBinding `syncagent-resources-access` | Optional (`rbac.enabled`): access to the OpenKCM resources for a sync agent |

The controller that serves the APIExport is deployed with the
[platform-mesh-controller](../platform-mesh-controller) chart.

## Install

```sh
helm install pm-integration oci://ghcr.io/openkcm/charts/pm-integration \
  --version 0.11.0 --kubeconfig openkcm-provider.kubeconfig \
  --set publicScheme=https --set publicHost=openkcm-ui.portal.example.org
```

`publicHost` is the host that serves the microfrontend's content files (`/pm-content.json`,
`/pm-content-namespace.json`). The release notes warn while it is `localhost` or the scheme is
`http`.

## Versions and schemas

kcp APIResourceSchemas cannot change, so their names contain the chart version. Every chart
version therefore creates new schemas and points the APIExport to them; Helm deletes the
schemas of the previous version during the upgrade. Increase the chart version with every
change to the CRDs, and regenerate the schemas:

```sh
make manifests
python3 hack/sync-api-resource-schemas.py
```

The helm tests (`make helm-test`) fail when the schemas and the CRDs differ.

## Values

| Value | Default | Description |
| --- | --- | --- |
| `publicScheme` | `http` | Scheme of the content URLs; `https` outside a local portal |
| `publicHost` | `localhost` | Host of the microfrontend's content files |
| `metadata.contentPath` / `metadata.namespaceContentPath` | `/pm-content.json` / `/pm-content-namespace.json` | Paths of the content files |
| `branding.marketplace.title` / `description` | `OpenKCM`, … | The marketplace entry |
| `rbac.enabled` | `false` | RBAC for a sync agent |
| `global.commonLabels` | `{}` | Labels of every resource |

The resource names are fixed: the portal and the controller find them by name.

## Uninstall

```sh
helm uninstall pm-integration --kubeconfig openkcm-provider.kubeconfig
```

Accounts that bound the APIExport lose access to the OpenKCM resources.
