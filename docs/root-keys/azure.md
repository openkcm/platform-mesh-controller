# Azure Key Vault as L1: how Krypton authenticates

The customer keeps the L1 root key in their own Azure Key Vault. Krypton needs a Microsoft Entra ID access token to
call that key. This file lists the ways Krypton can authenticate to the customer's Azure tenant, in two kinds, and
for each one what the customer provides, where it comes from, where Platform Mesh keeps it, how the AzureRootKey
looks, and what the method requires from Platform Mesh. The flow that is the same for every provider is in
[root-key-flow.md](root-key-flow.md).

| Kind | Method | Variant |
|---|---|---|
| [1. Static secret](#1-static-secret) | Client secret | [A. Client secret](#a-client-secret) |
| [2. Identity provider](#2-identity-provider) | Federation | [A. Customer's IdP](#a-customers-identity-provider) |

Static secret: the customer hands over a client secret. Identity provider: Entra trusts a token from an outside
issuer, so Azure holds no secret.

Every method is documented here, but not every method is implemented on day one: the implementation takes them one
by one by priority. The methods that send a secret to Krypton (the client secret and identity provider A) also
wait for the secret hand-off to Krypton (see Common rules).

OpenKCM runs nothing in Azure and needs no Azure subscription or Entra tenant of its own. Every Azure resource below
is created in the customer's tenant by a template the customer deploys. Krypton only calls the public Entra and Key
Vault endpoints.

The parts called "How Krypton authenticates" and steps 2 to 4 of the registration section show what happens behind
Krypton. They explain each method and are examples only: that part is designed by the Krypton team.

## Azure words used below

- **Microsoft Entra ID:** Microsoft's login service for Azure. Key Vault accepts no passwords or API keys, only Entra
  tokens, so every caller must be an identity in Entra. Each customer has their own **Entra tenant** (directory)
  with a tenant ID. It is not the OpenKCM Tenant.
- **App registration:** a login account for a program instead of a person. Krypton cannot log in as a human, so the
  customer creates this account in their Entra directory. It has a client ID (like a username) and holds a client
  secret (like a password). Krypton sends both to Entra and gets a token back; Key Vault accepts only calls that
  carry such a token.
- **Service principal** (the portal calls it an "enterprise application"): the same account as the directory sees
  it when permissions are given. The app registration says who the program is; roles are assigned to the service
  principal, not to the registration. The portal creates it together with the registration; the CLI and Graph create
  it in a separate step.
- **Role assignment:** which role the service principal has on which scope (the vault or a key). The token proves
  who Krypton is; the role decides what it may do.

## Common rules

- **Before any method:** OpenKCM is enabled and the tenant exists (step 0). The customer has an RSA key (2048 to
  4096 bits) in a Key Vault that uses the "Azure role-based access control" permission model, and knows its key
  identifier: portal > Keys > the key > a version > "Key Identifier",
  `https://<vault>.vault.azure.net/keys/<key name>/<key version>`. New vaults use that model by default. On an older
  vault that uses access policies, switching voids every existing policy, so the customer checks first.
- **Key version:** wrap and unwrap URLs carry the version, and a key wrapped under one version cannot be unwrapped
  under another. So `keyVersion` is required, and after the key is Active the vault URL, key name, key version and
  method do not change: a different version is a different root key.
- **Secrets:** all credentials of a method go into one Kubernetes Secret: the secret value and the identifiers that
  belong to it (client ID). The UI creates it in the customer's kcp workspace, in the namespace of the AzureRootKey,
  after the draft exists, and names it `azurerootkey-<UID>` (see the next rule). It labels it with
  `openkcm.io/root-key-credentials: "true"`. The AzureRootKey only references it (`secretRef` with `name` and
  `namespace`), which the UI sets when it completes the key. No secret value is ever written into an AzureRootKey, its
  status, a log or an event. The entry names are fixed per method and listed in each section.
- **Secret name and link:** every method names the Secret `azurerootkey-<UID>`, where UID is the `metadata.uid` of the
  AzureRootKey. The API server assigns it when the draft is created, so the UI reads it from the draft before it creates
  the Secret. The UID is unique across all workspaces, so the name alone identifies the root key. The controller accepts
  only that name, in the AzureRootKey's own namespace; if `secretRef` says anything else, it reads no Secret and shows
  the reason in the status. So a Secret belongs to exactly one AzureRootKey: two root keys never share one, a customer
  cannot point a root key at another Secret, and a root key that is deleted and created again gets a new UID and so a
  new Secret. A rotation replaces the values in the same Secret.
- **Deleting the root key (open):** a Secret named after the UID has no use once the AzureRootKey is gone. It is not
  decided who deletes it: the controller with a finalizer (it then needs the right to delete Secrets there) or the UI.
- **Secret changes:** the controller watches the labelled Secrets. When one changes (the customer replaces a value), it
  finds the root key from the UID in the Secret name and tells Krypton that the root key has new credentials (working
  name, the API is designed by the Krypton team).
- **Secret hand-off to Krypton (open):** the target is that nothing unencrypted is stored at Krypton or travels to
  it, planned through a public key taken from Krypton. That endpoint does not exist and is not agreed. For now the
  value stays in the Kubernetes Secret as is, and how it reaches Krypton is open.
- **CRD change:** the `auth` block is new, so that one kind holds several methods. It replaces the required
  `federatedIdentity{tenantId, clientId}`, and `keyVersion` becomes required (it was optional, and empty meant
  "latest"), so existing AzureRootKeys are migrated to the new shape. `auth.tenantId` is shared by all methods and
  each method has its own block; for the client secret the client ID is not in the spec but in the Secret. For the
  customer's identity provider the Secret holds the identity provider's client ID and secret, and the spec holds the
  Azure app's client ID. `tenantNameRef`, `vaultUrl`, `keyName`, `keyVersion` and `lifecycle` stay.
- **Vault firewall:** if the vault restricts network access, OpenKCM's addresses must be on its allow list (public
  IPv4 only). A vault open to private endpoints only cannot be reached. Open: stable egress addresses.

## What is required from Platform Mesh

| Building block | Needed by | Offered today |
|---|---|---|
| Kubernetes Secret in the customer's kcp workspace | both methods | yes |
| Permission for the controller to read and watch those Secrets | both methods | no |
| Hand-off of that Secret to Krypton, not in plain text | both methods | no |

The permission is an APIExport permission claim on `secrets`; the APIExport claims none today.

## Step 0. Tenant creation (the same for every method)

1. The customer enables OpenKCM in the Platform Mesh marketplace. This puts an APIBinding (the link between their
   kcp workspace and the OpenKCM API) into the workspace.
2. The controller sees the APIBinding and creates a Tenant resource in the workspace.
3. The controller asks Krypton to create the tenant (`CreateTenant`) and stores the returned tenant ID in the Tenant
   annotation. This is the guard: Krypton returns a new ID on every call, so once the annotation is set the
   controller never calls `CreateTenant` again.

## 1. Static secret

The customer gives OpenKCM a long-lived secret. Azure has one method of this kind: the client secret.

### A. Client secret

**What it is:** the customer gives OpenKCM the client secret of an app registration in their tenant. The app can
only wrap and unwrap with the customer's vault.

**Before anything:**
- The customer's tenant allows client secrets. An administrator can block them by policy; creating the secret then
  fails with "Client secrets are blocked by a tenant-wide policy", and an Application Administrator or Cloud
  Application Administrator must lift it for this app. By default nothing is blocked.
- The person who deploys can create an app registration with its service principal (at least Cloud Application
  Administrator, or an ordinary user if the tenant lets users register applications) and can assign roles on the
  vault (Owner or User Access Administrator).
- A terminal logged in to Azure with the Azure CLI. Cloud Shell has it. The portal route below needs no tools.

**What the customer provides:**

| # | Value | Where it comes from | Secret | Goes into | Step |
|---|---|---|---|---|---|
| 1 | Key identifier URL | the Key Vault key (see Common rules) | no | `spec.vaultUrl`, `keyName`, `keyVersion` | 1 |
| 2 | Entra tenant ID | output `tenantId` of the template | no | `spec.auth.tenantId` | 6 |
| 3 | Client ID | output `clientId` of the template | no | Secret entry `clientId` | 6 |
| 4 | Client secret value | added to the app in step 5, shown once | yes | Secret entry `clientSecret` | 6 |

Not typed by the customer: `spec.tenantNameRef` and `spec.auth.method: ClientSecret` (set by the UI with the draft), and
`spec.auth.clientSecret.secretRef` (set when the UI creates the Secret `azurerootkey-<UID>` from values 3 and 4). The UI
splits the key identifier URL into the three spec fields. The client ID is not secret but sits in the Secret with the
client secret, so the credentials are in one place and a rotation is one write.

**What the customer does:**
1. In the UI, picks Azure Key Vault with a client secret and pastes the key identifier URL. The customer copies it
   in the Azure portal: the vault > Keys > the key > one version > Key Identifier. It must be the URL of a
   version, because a key wrapped under one version cannot be unwrapped under another. The UI creates a draft
   AzureRootKey from it. This method needs nothing from Krypton.
2. The UI gives a Bicep file already filled in with the vault name and an app name. Bicep is Azure's template
   language: a text file that lists the objects to create, and Azure creates them when the file is deployed. The
   customer types nothing into it.
3. In Cloud Shell or a terminal logged in to Azure, the customer deploys the file into the vault's resource group
   with an Azure CLI deploy command. Running it again changes nothing.
4. When the deployment finishes it returns two outputs, values the file declares so that the customer can read them:
    - `tenantId`: the ID of the customer's Entra tenant. Krypton asks this tenant for its token.
    - `clientId`: the ID of the app registration the file created. It names the identity Krypton logs in as.

   Neither is secret. The customer copies both into the UI in step 6.
5. The customer adds a client secret to the app, in the portal (App registrations > the app > Certificates &
   secrets) or with the Azure CLI. Entra shows it once: the customer copies the **Value** column, not the Secret ID.
   In the CLI the command must use `--append`; by default it deletes the app's old secrets, which would cut off
   OpenKCM until it gets the new one. The secret lasts at most 24 months; Microsoft recommends under 12.
6. The customer enters the tenant ID, the client ID and the secret in the UI. The UI first creates the Secret
   `azurerootkey-<UID>` (the UID of the draft) with the client ID and the secret, then saves the tenant ID and
   `secretRef` to the AzureRootKey. That completes the key and starts registration.

**What the Bicep file creates** in the customer's tenant and vault:
- **An app registration**, the program's ID card: a name, a client ID, and a place for its client secret. It is
  limited to the customer's Entra tenant and asks for no other permissions.
- **A service principal**, the program's account in that tenant, linked to the registration by the client ID. Entra
  signs in the account and roles are given to it, not to the ID card. They are two objects because one app can serve
  many tenants, with one account per tenant; here there is one tenant, so one of each.
- **A role assignment** on the vault: gives the service principal `Key Vault Crypto Service Encryption User`, its
  only permission.
- It does not create the key or the vault: they must already exist.

**The role** `Key Vault Crypto Service Encryption User` is built into Azure. It allows only wrap and unwrap with a key
(and reading its public part). It cannot create, change or delete keys, and cannot read secrets or certificates. The
file gives it on the whole vault, as Microsoft recommends, so the app can use every key in that vault; the L1 key
should therefore sit in a vault of its own. Giving it on the single key also works, but Microsoft discourages it.

**Other ways to create the same three objects:**
- **Portal.** App registrations > New registration, this directory only, no redirect address; copy the client ID and
  tenant ID. The portal creates the service principal with it. Then Key Vault > Access control (IAM) > Add role
  assignment > the role > the app.
- **CLI.** `az ad app create`, `az ad sp create --id <client ID>`, `az role assignment create`. Or the single
  `az ad sp create-for-rbac --role ... --scopes <vault resource ID>`, which also creates the secret and prints it.
- **ARM template or Terraform.** The Graph resource types exist for Bicep; this research found no ARM JSON
  equivalent for the app registration, and did not check Terraform (a secret created there would sit in its state).

**Where the values are kept:**
- Client ID and client secret: in a Kubernetes Secret in the customer's kcp workspace, created by the UI in step 6.
  The AzureRootKey holds only the reference.
- Everything else: in the AzureRootKey spec.

**Example**:

The AzureRootKey:

```yaml
apiVersion: operations.openkcm.io/v1alpha1
kind: AzureRootKey
metadata:
  name: acme-azure-root
  namespace: default
  uid: 2e8f5a13-d7b6-4c90-b4a1-6f3d9c0e7b25
spec:
  tenantNameRef: acme
  vaultUrl: https://acme-prod-kv.vault.azure.net
  keyName: openkcm-l1
  keyVersion: 0698c2156c1a4e1da5b6bab6f6422fd6
  auth:
    method: ClientSecret
    tenantId: 8a1f2c3d-4b5e-4f60-9a7b-8c9d0e1f2a3b
    clientSecret:
      secretRef:
        name: azurerootkey-2e8f5a13-d7b6-4c90-b4a1-6f3d9c0e7b25
        namespace: default
```

The Secret the UI creates and the AzureRootKey points at:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: azurerootkey-2e8f5a13-d7b6-4c90-b4a1-6f3d9c0e7b25
  namespace: default
type: Opaque
stringData:
  clientId: "<client ID>"
  clientSecret: "<client secret value>"
```

**What the controller sends:** the method, the tenant ID, the vault URL, the key name, the key version, the Entra
tenant ID, the client ID and the client secret (how the secret reaches Krypton is open, see Common rules).
Registration, validation and activation then follow (see the registration section).

**Required from Platform Mesh:** a Kubernetes Secret in the customer's workspace for the client ID and secret, and a
way to hand it to Krypton without plain text (open).

**How Krypton authenticates (example):** it sends the client ID and the client secret to the customer's Entra tenant
and asks for a token for Key Vault. Entra checks them against the app registration and returns an access token valid
for about one hour. Krypton then sends its wrap and unwrap requests to the vault with that token. Key Vault checks the
token, then the role. The secret goes only to Entra, never to Key Vault.

**Tenant isolation:** the app registration, the role and the vault belong to the customer. A wrap or unwrap call
cannot be pinned to a context as on AWS, so the separation is one app registration per customer root key, and at
vault scope a vault that holds only this key.
**Advantage:** works with any Azure tenant and needs nothing on the Platform Mesh side except the Secret.
**Downsides:**
- The secret is long-lived and stored at OpenKCM. Microsoft says client secrets should not be used in production and
  recommends federated credentials (identity provider A) instead.
- The customer's tenant may block client secrets by policy.
- The secret expires after at most 24 months. The customer renews it by hand: add a new secret with `--append`,
  update the Secret in the UI, delete the old one. After the expiry date Key Vault calls fail, and OpenKCM learns of
  it only when a call fails.
- At vault scope the role reaches every key in the vault.

**Cut-off:** the customer deletes the secret, disables the service principal, or removes the role assignment (role
changes take up to 10 minutes). The strongest cut is disabling the key in Key Vault, which stops all use. A key with
a passed expiry date still unwraps, so an expiry date is no cut-off. A token already issued lives up to about one
hour.

## 2. Identity provider

Entra is told to trust tokens from an outside issuer: the customer's own OpenID Connect (OIDC) identity provider.
Azure holds no secret in this kind. OpenKCM provides no identity provider.

### A. Customer's identity provider

**What it is:** the customer's identity provider (for example Okta, Keycloak or another Entra tenant) gives Krypton a
signed token, and Entra exchanges it for an Entra token. The trust is a **federated credential** on the app
registration: an issuer, a subject (who the token is about) and an audience. Entra downloads the issuer's public keys
from the issuer address to check the token's signature.

**Before anything:**
- The customer has an OIDC identity provider that signs its tokens with RS256 and whose issuer address is reachable
  from the public internet, because Entra must fetch the keys. A provider reachable only inside the customer's
  network does not work.
- In it, an application for OpenKCM that may get tokens with the client credentials grant, set so that its tokens
  carry the audience `api://AzureADTokenExchange`, the value Microsoft prescribes. The customer notes the issuer
  address, the token endpoint, the client ID and the client secret.
- The person who deploys can create an app registration with its service principal, add a federated credential to it
  (as the app's owner, or as Application Administrator or Cloud Application Administrator) and assign roles on the
  vault (Owner or User Access Administrator).
- A terminal logged in to Azure with the Azure CLI. Cloud Shell has it. The portal route below needs no tools.

**What the customer provides:**

| # | Value | Where it comes from | Secret | Goes into | Step |
|---|---|---|---|---|---|
| 1 | Key identifier URL | the Key Vault key (see Common rules) | no | `spec.vaultUrl`, `keyName`, `keyVersion` | 2 |
| 2 | Issuer address | the customer's identity provider | no | the Bicep file only (kept in the UI form) | 2 |
| 3 | Identity provider client ID | the application registered for OpenKCM | no | Secret entry `clientId` | 2 |
| 4 | Entra tenant ID | output `tenantId` of the template | no | `spec.auth.tenantId` | 5 |
| 5 | Entra client ID | output `clientId` of the template | no | `spec.auth.federatedIdp.appClientId` | 5 |
| 6 | Token endpoint | the customer's identity provider | no | `spec.auth.federatedIdp.tokenEndpoint` | 5 |
| 7 | Identity provider client secret | the application registered for OpenKCM | yes | Secret entry `clientSecret` | 5 |
| 8 | Scope | the identity provider, if it needs one (see "About the scope") | no | `spec.auth.federatedIdp.scope` | 5 |

Not typed by the customer: `spec.tenantNameRef` and `spec.auth.method: FederatedIdP` (set by the UI with the draft), and
`spec.auth.federatedIdp.secretRef` (set when the UI creates the Secret `azurerootkey-<UID>` from values 3 and 7). There
are two client IDs: the identity provider's (value 3, in the Secret) and the Azure app's (value 5, in the spec). The
identity provider's client ID is not secret but sits in the Secret with its client secret, so the credentials are in one
place and a rotation is one write. The Bicep file needs the issuer address and the identity provider's client ID in step
3, so the customer enters them in step 2 and the UI keeps them in the form: the client ID until it creates the Secret in
step 5, the issuer address until the Bicep file is shown. The draft AzureRootKey holds neither. The controller does not
send the issuer address to Krypton: only the federated credential in Azure needs it, and Krypton uses the token
endpoint.

**About the scope:** the scope is a standard OAuth 2.0 parameter of the token request that Krypton sends to the
customer's identity provider (not to Entra). It tells the provider what the token is for, and Azure never sees it. The
field exists because some providers refuse the request without one (Microsoft Entra ID as the customer's provider
needs the resource identifier with `/.default`, Okta needs a scope the customer created), while others apply a
default, and then the customer leaves it empty. The customer takes the value from the provider's documentation for
the client credentials grant, and Krypton sends it as entered.

**What the customer does:**
1. In their identity provider, registers an application for OpenKCM as described above, and notes the issuer address,
   the token endpoint, the client ID, the client secret and the scope if the identity provider needs one.
2. In the UI, picks Azure Key Vault with OIDC (own identity provider) and enters the key identifier URL, the issuer
   address and the identity provider's client ID. The UI creates a draft AzureRootKey with the key identifier parts.
   This method needs nothing from Krypton.
3. The UI gives a Bicep file (Azure's template language: a text file that lists the objects to create) already filled
   in with the vault name, an app name, the issuer address and the client ID as the subject. In Cloud Shell or a
   terminal logged in to Azure, the customer deploys it into the vault's resource group with an Azure CLI deploy
   command. Running it again changes nothing.
4. When the deployment finishes it returns two outputs, values the file declares so that the customer can read them:
    - `tenantId`: the ID of the customer's Entra tenant. Krypton exchanges the token at this tenant.
    - `clientId`: the ID of the Azure app registration the file created (not the identity provider's application). It
      names the identity Krypton acts as.

   Neither is secret. The customer copies both into the UI in step 5.
5. The customer enters the Entra tenant ID, the Entra client ID, the token endpoint, the scope (if any) and the
   client secret in the UI. The UI first creates the Secret `azurerootkey-<UID>` (the UID of the draft) with the
   identity provider's client ID and client secret, then saves the tenant ID, the Entra client ID, the token endpoint,
   the scope and `secretRef` to the AzureRootKey. That completes the key and starts registration.

**To verify per identity provider:** the file uses the client ID as the subject. The `sub` claim of a
client-credentials token is not always the client ID (some providers put another identifier there). Entra compares
issuer, subject and audience exactly, case-sensitive and without wildcards, and a wrong value is not reported when the
federated credential is created: the token exchange just fails later. The subject must be the value the provider
really puts into the token.

**What the Bicep file creates** in the customer's tenant and vault:
- **An app registration**, the program's ID card: a name and a client ID, limited to the customer's Entra tenant.
- **A service principal**, the program's account in that tenant, linked to the registration by the client ID. Entra
  signs in the account and roles are given to it, not to the ID card. Together they are the identity Krypton acts as
  after the exchange.
- **A federated credential** on the app registration: the trust itself. It holds the issuer address, the subject and
  the audience, and Entra accepts a token from the identity provider only when all three match.
- **A role assignment** on the vault: gives the service principal the built-in role `Key Vault Crypto Service
  Encryption User`. It allows only wrap and unwrap with a key (and reading its public part), and cannot create,
  change or delete keys or read secrets and certificates. It is given on the whole vault, as Microsoft recommends, so
  the L1 key should sit in a vault of its own.
- It creates no client secret, so nothing in Azure holds a credential. It does not create the key or the vault: they
  must already exist.

**Other ways to create the same objects:** in the portal, App registrations > New registration (this directory
only), then Certificates & secrets > Federated credentials > Add credential, scenario "Other issuer" (issuer,
subject identifier, audience, name), then Key Vault > Access control (IAM) > Add role assignment > the role >
the app. With the CLI, the federated credential is `az ad app federated-credential create`.

**Where the values are kept:**
- Identity provider client ID and client secret: in a Kubernetes Secret in the customer's kcp workspace, created by
  the UI in step 5. The AzureRootKey holds only the reference.
- Issuer address: from the customer's identity provider, public. Not kept in the AzureRootKey: the UI uses it only to
  fill the Bicep file.
- Everything else: in the AzureRootKey spec.

**Example:**

```yaml
apiVersion: operations.openkcm.io/v1alpha1
kind: AzureRootKey
metadata:
  name: acme-azure-root
  namespace: default
  uid: 2e8f5a13-d7b6-4c90-b4a1-6f3d9c0e7b25
spec:
  tenantNameRef: acme
  vaultUrl: https://acme-prod-kv.vault.azure.net
  keyName: openkcm-l1
  keyVersion: 0698c2156c1a4e1da5b6bab6f6422fd6
  auth:
    method: FederatedIdP
    tenantId: 8a1f2c3d-4b5e-4f60-9a7b-8c9d0e1f2a3b
    federatedIdp:
      tokenEndpoint: https://idp.example.com/acme/oauth2/token
      scope: <scope, if the IdP needs one>
      appClientId: 5d4c3b2a-1f0e-4d9c-8b7a-6f5e4d3c2b1a
      secretRef:
        name: azurerootkey-2e8f5a13-d7b6-4c90-b4a1-6f3d9c0e7b25
        namespace: default
```

The Secret the UI creates and the AzureRootKey points at:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: azurerootkey-2e8f5a13-d7b6-4c90-b4a1-6f3d9c0e7b25
  namespace: default
type: Opaque
stringData:
  clientId: "<identity provider client ID>"
  clientSecret: "<identity provider client secret>"
```

**What the controller sends:** the method, the tenant ID, the vault URL, the key name, the key version, the Entra tenant
ID, the Entra client ID, the token endpoint, the scope, and the identity provider's client ID and client secret (how the
secret reaches Krypton is open, see Common rules). Registration, validation and activation then follow (see the
registration section).

**Required from Platform Mesh:** a Kubernetes Secret in the customer's workspace for the identity provider's client ID
and secret, and a way to hand it to Krypton without plain text (open). Nothing else; the identity provider is the
customer's.

**How Krypton authenticates (example):** it logs in to the customer's identity provider with the client ID and
client secret and gets a signed token. It sends that token to the customer's Entra tenant together with the Azure
app's client ID. Entra checks the token against the federated credential and the issuer's public keys, and returns an
access token valid for about one hour. Krypton then sends its wrap and unwrap requests to the vault with that token.
The secret goes only to the identity provider, never to Azure.

**Tenant isolation:** the identity provider, the app registration, the federated credential, the role and the vault
belong to the customer. A wrap or unwrap call cannot be pinned to a context as on AWS, so the separation is one app
registration per customer root key, and at vault scope a vault that holds only this key.
**Advantage:** nothing in Azure to expire or rotate, and the customer controls access in their own identity provider
and can cut it off there.
**Downsides:**
- A long-lived identity provider secret is stored at OpenKCM and the customer rotates it by hand: one secret is
  swapped for another.
- Access depends on the identity provider being up and reachable from the internet.
- A new federated credential can take several minutes to work. Until then Entra refuses the exchange with
  `AADSTS70021`.
- Providers differ: `sub` is not always the client ID.

**Cut-off:** the customer disables the application in their identity provider, deletes the federated credential, or
removes the role assignment (role changes take up to 10 minutes). The strongest cut is disabling the key in Key
Vault. A token already issued lives up to about one hour.

## Quick comparison

For every method the customer also enters the key identifier URL.

| Kind | Variant | Customer enters | Secret from the customer |
|---|---|---|---|
| 1. Static secret | A. Client secret | Entra tenant ID, client ID, client secret | yes, client secret |
| 2. Identity provider | A. Customer's IdP | issuer, IdP client ID/secret, Entra tenant/app ID, token URL, scope | yes |

| Kind | Variant | Customer renews |
|---|---|---|
| 1. Static secret | A. Client secret | the client secret, at most every 24 months |
| 2. Identity provider | A. Customer's identity provider | the identity provider's client secret |

A changed Secret reaches Krypton through the controller (see Secret changes).

## Registration, validation and activation (the same for every method)

This starts when the AzureRootKey is complete: every field the method needs is set.

1. The controller calls Krypton to register the root key with the method and its values (working name, the API is
   designed by the Krypton team) and polls every 5 seconds while the key is in pre-activation. The root key shows
   pending.
2. Krypton gets an Entra access token. This is the only part that depends on the method.
3. Krypton checks the key in Key Vault: it reads the key (enabled, RSA, wrap and unwrap allowed), then wraps a test
   value, unwraps it and compares.
4. Krypton records the result: passed, or failed with a reason (Entra refused the credentials: no retries; Entra or Key
   Vault unavailable: retry; check failed).
5. If the check passed, the controller calls `ActivateKey`. A key cannot go back to pre-activation, so this happens
   only after a passed check. If it failed, the controller writes the reason into the AzureRootKey status.
6. The controller sets the AzureRootKey status to Ready and the key to Active, and the UI shows it.

From registration on, when a Secret of the AzureRootKey changes, the controller sends the new values to Krypton (see
Common rules, Secret changes).
