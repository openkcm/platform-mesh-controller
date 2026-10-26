# GCP Cloud KMS as L1: how Krypton authenticates

The customer keeps the L1 root key in their own GCP Cloud KMS. Krypton needs Google credentials to call that key.
This file lists the ways Krypton can authenticate to the customer's GCP project, in three kinds, and for each one
what the customer provides, where it comes from, where Platform Mesh keeps it, how the GCPRootKey looks, and what
the method requires from Platform Mesh. The flow that is the same for every provider is in
[root-key-flow.md](root-key-flow.md).

| Kind | Method | Variant |
|---|---|---|
| [1. Static secret](#1-static-secret) | Service account key | [A. Service account key](#a-service-account-key) |
| [2. Identity provider](#2-identity-provider) | OIDC | [A. Customer's IdP](#a-customers-identity-provider-oidc) |
| [3. Certificate](#3-certificate) | X.509 | [A. Customer's certificate](#a-customers-certificate) |
| | | [B. Platform Mesh PKI](#b-platform-mesh-pki) |

OpenKCM runs nothing in GCP and needs no GCP project of its own. Every GCP resource below is created in the
customer's project by a Terraform file the customer applies. Krypton only calls the public Google endpoints.

Every method is documented here, but not every method is implemented on day one: the implementation takes them one
by one by priority.

The part called "How Krypton authenticates" shows what happens behind Krypton. It explains the method and is an
example only.

## Common rules

- **Before any method:** OpenKCM is enabled and the tenant exists (Step 0 below). The customer has a symmetric
  Cloud KMS key (purpose encrypt/decrypt) in a GCP project with billing on, and knows its resource name:
  `projects/<project>/locations/<location>/keyRings/<ring>/cryptoKeys/<key>`, without a key version. The location
  is part of the name, so the GCPRootKey has no `region` field.
- **Secrets:** all credentials of a method go into one Kubernetes Secret: the secret value and the identifiers that
  belong to it (the service account key file, client ID, certificates). The UI creates it in the customer's kcp
  workspace, in the namespace of the GCPRootKey, after the draft exists, and names it `gcprootkey-<UID>` (see the next
  rule). It labels it with `openkcm.io/root-key-credentials: "true"`. The GCPRootKey only references it (`secretRef`
  with `name` and `namespace`), which the UI sets when it completes the key. For the Platform Mesh PKI cert-manager
  creates the Secret in the controller's cluster instead, under the same name (see 3. B), and the controller writes the
  reference. No secret value is ever written into a GCPRootKey, its status, a log or an event. The entry names are
  fixed per method and listed in each section.
- **Secret name and link:** every method names the Secret `gcprootkey-<UID>`, where UID is the `metadata.uid` of the
  GCPRootKey. The API server assigns it when the draft is created, so the UI reads it from the draft before it creates
  the Secret. The UID is unique across all workspaces, so the name alone identifies the root key. The controller
  accepts only that name (in the GCPRootKey's own namespace, or in the platform's namespace for the Platform Mesh
  PKI); if `secretRef` says anything else, it reads no Secret and shows the reason in the status. So a Secret belongs
  to exactly one GCPRootKey: two root keys never share one, a customer cannot point a root key at another Secret, and
  a root key that is deleted and created again gets a new UID and so a new Secret. A rotation replaces the values in
  the same Secret.
- **Deleting the root key (open):** a Secret named after the UID has no use once the GCPRootKey is gone. For the
  Platform Mesh PKI a finalizer deletes the Secret. For a Secret the UI created in the customer's workspace, it is not
  decided who deletes it: the controller with a finalizer (it then needs the right to delete Secrets there) or the UI.
- **Secret hand-off to Krypton (open):** the target is that nothing unencrypted is stored at Krypton or travels to it,
  planned through a public key taken from Krypton, which does not exist yet. For now the value stays in the
  Kubernetes Secret as is, and how it reaches Krypton is open.
- **Secret changes:** the controller watches the labelled Secrets. When one changes (the customer replaces a value,
  or cert-manager renews a certificate), it finds the root key from the UID in the Secret name and tells Krypton that
  the root key has new credentials (working name, the API is designed by the Krypton team).
- **Provider path:** the full name of the Workload Identity provider: the record in the customer's project that tells
  Google how to check the customer's IdP (2. A) or CA (3). It is a Google name, not a Terraform one: "provider" here
  is not the Terraform provider plugin. The Terraform file creates the provider and prints its name, and the customer
  copies it into the UI as is. The form is
  `//iam.googleapis.com/projects/<project number>/locations/global/workloadIdentityPools/<pool>/providers/<provider>`.
  The UI cannot build it itself: it needs the project number, and the key name has only the project ID [inferred].
  Krypton sends it to Google's token service as the audience of the token exchange, so Google knows which provider
  checks the token. It is not secret and is kept in the spec.
- **CRD change:** the `auth` block is new in the GCPRootKey CRD, so that one kind holds several methods. The
  Platform Mesh PKI variant also adds `status.clientCertificate`.
  Today the CRD is a stub with no reconciler: `spec` has `tenantNameRef`, an optional `keyUri` and `lifecycle`.
- **About the examples:** names are illustrative. Fields under `auth` and `status.clientCertificate` are new;
  `tenantNameRef` and `keyUri` exist in the CRD today. Secret values are written as placeholders.

## What is required from Platform Mesh

| Building block | Needed by | Offered today |
|---|---|---|
| Kubernetes Secret in the customer's kcp workspace | key file, customer's IdP, customer's certificate | yes |
| Permission for the controller to read and watch those Secrets | key file, IdP, customer's certificate | no |
| Hand-off of a Secret to Krypton, not in plain text | every method | no |
| OpenBao PKI with a CA, and cert-manager with an issuer for it, in the controller's cluster | Platform Mesh PKI | no |

The permission is an APIExport permission claim on `secrets`; the APIExport claims none today. Whether the PKI has
one CA per tenant or one shared CA is open.

## Step 0. Tenant creation (the same for every method)

1. The customer enables OpenKCM in the Platform Mesh marketplace. This puts an APIBinding (the link between their
   kcp workspace and the OpenKCM API) into the workspace, and the controller creates a Tenant resource from it.
2. The controller asks Krypton to create the tenant (`CreateTenant`) and stores the returned tenant ID in the Tenant
   annotation. This is the guard: Krypton returns a new ID on every call, so once the annotation is set the
   controller never calls `CreateTenant` again.

## 1. Static secret

The customer gives OpenKCM a long-lived secret. GCP has one method of this kind: the service account key.

### A. Service account key

**What it is:** the customer gives OpenKCM a long-lived JSON key of a service account (a non-human identity in the
customer's project). The service account can only encrypt and decrypt with one KMS key.

**Before anything:**
- These APIs are enabled in the project: `cloudkms.googleapis.com` and `iam.googleapis.com`. If one is off, the
  call fails with `SERVICE_DISABLED`. To enable both:
  `gcloud services enable cloudkms.googleapis.com iam.googleapis.com --project=<project>`
- Creating service account keys is allowed. Organisations created on or after 2024-05-03 block it by default with
  the organisation policy `iam.managed.disableServiceAccountKeyCreation`. The organisation's Organization Policy
  Administrator (`roles/orgpolicy.policyAdmin`) must switch it off for this project. A dedicated project that holds
  only this service account keeps the exception narrow. A project of a personal Google account normally has no
  organisation, so there is nothing to switch off.
- The person who applies the Terraform file can create service accounts and change IAM on the KMS key. Project
  Owner covers both.

**What the customer provides:**

| # | Value | Where it comes from | Secret | Goes into | Step |
|---|---|---|---|---|---|
| 1 | KMS key resource name | the customer's KMS key (console: copy resource name) | no | `spec.keyUri` | 1 |
| 2 | Key file (`key.json`) | `gcloud iam service-accounts keys create` | yes | Secret `serviceAccountKey` | 6 |

Not typed by the customer: `spec.tenantNameRef` and `spec.auth.method: ServiceAccountKey` (set by the UI with the
draft), and `spec.auth.serviceAccountKey.secretRef` (set when the UI creates the Secret `gcprootkey-<UID>` from value
2). The service account email is not entered: it is in the file as `client_email`.

**What the customer does:**
1. In the UI, picks GCP KMS with a service account key and enters the KMS key resource name. No region is needed.
   The UI creates a draft GCPRootKey.
2. The UI gives a Terraform file with the key name already filled in, and the customer downloads it. The customer
   types nothing into it. This method needs nothing from Krypton at this point.
3. In Cloud Shell, or in a terminal logged in to GCP, the customer applies it in their project: `terraform init`,
   then `terraform apply`.
4. Terraform returns the service account email as an output.
5. Terraform does not create the private key, because a key created by Terraform is stored in the Terraform
   state. The customer creates it for the service account Terraform made. In Cloud Shell or a terminal:
   `gcloud iam service-accounts keys create key.json --iam-account=<email>`. In the console: IAM & Admin >
   Service Accounts > the account > Keys > Add key > Create new key > JSON, and `key.json` is downloaded.
   Google shows the key once: it cannot be downloaded again.
6. The customer uploads `key.json` in the UI. That is the only thing entered after the key name. The UI first creates
   the Secret `gcprootkey-<UID>` (the UID of the draft) with the file, then saves the `secretRef` to the GCPRootKey.
   That completes the key and starts registration.

**What is in `key.json`** (Krypton uses these five; the file has a few more that it ignores):
- `type`: must be `service_account`. Other Google credential files (for example `authorized_user` or
  `external_account`) are different things and must be rejected.
- `client_email`: the service account's email. It is the identity Google sees, and it must be the same email that
  the Terraform bindings grant the roles to on the KMS key.
- `private_key_id`: the ID of this key among the service account's keys.
- `private_key`: the RSA private key (PEM). The only secret in the file.
- `token_uri`: where to send the JWT signed with the private key. Google's is `https://oauth2.googleapis.com/token`.

**Who checks the file:** both the UI and the controller check `type` is `service_account` and `token_uri`
is Google's token endpoint. The UI check gives the customer an answer at upload. The controller check is the one
that counts, because the Secret can be created without the UI. The `token_uri` check matters because the file comes
from outside: Krypton would otherwise send a signed JWT to whatever address the file names. A failed
check shows as a condition on the GCPRootKey, and nothing is sent to Krypton.

**What the Terraform file creates** in the customer's project:
- **A service account**, the identity Krypton acts as. It has no roles on the project and no keys.
- **A binding on the KMS key** that gives the service account the role `roles/cloudkms.cryptoKeyEncrypterDecrypter`.
  It can encrypt and decrypt with this key and nothing else. A binding names who, which role and which resource.
- **A binding on the same KMS key** that gives the service account the role `roles/cloudkms.viewer`. It lets
  Krypton read the key's state. The encrypt/decrypt role does not include that read.

Both bindings sit on the key itself, not on the key ring or the project, so no other key is reachable. The output
is the service account email. The file does not enable APIs and does not touch organisation policy: those are the
prerequisites above.

**Where the values are kept:**
- `key.json`: in a Kubernetes Secret in the customer's kcp workspace, created by the UI in step 6, as one entry
  with the whole file. The GCPRootKey holds only the reference.
- KMS key resource name: kept in the GCPRootKey spec.
- No secret value is ever written into the GCPRootKey, its status, a log or an event. The GCPRootKey only
  references the Secret (`secretRef` with `name` and `namespace`).

**Example:**

```yaml
apiVersion: operations.openkcm.io/v1alpha1
kind: GCPRootKey
metadata:
  name: acme-gcp-root
  namespace: default
  uid: 9b4e2d71-6c3a-4e58-a0f2-1d7c8b3e5a96
spec:
  tenantNameRef: acme
  keyUri: projects/acme-prod/locations/europe-west3/keyRings/openkcm/cryptoKeys/l1-root
  auth:
    method: ServiceAccountKey
    serviceAccountKey:
      secretRef:
        name: gcprootkey-9b4e2d71-6c3a-4e58-a0f2-1d7c8b3e5a96
        namespace: default
```

The Secret the UI creates and the GCPRootKey points at:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: gcprootkey-9b4e2d71-6c3a-4e58-a0f2-1d7c8b3e5a96
  namespace: default
type: Opaque
stringData:
  serviceAccountKey: |
    {
      "type": "service_account",
      "project_id": "acme-prod",
      "private_key_id": "<private key ID>",
      "private_key": "<private key, PEM>",
      "client_email": "openkcm-l1@acme-prod.iam.gserviceaccount.com",
      "client_id": "<client ID>",
      "token_uri": "https://oauth2.googleapis.com/token"
    }
```

**What the controller sends:** the method, the tenant ID, the key resource name and the key file (how the file
reaches Krypton is open, see Common rules). Registration, validation and activation then follow, the same as
for every method (see the last section).

**Required from Platform Mesh:** a Kubernetes Secret in the customer's workspace for the key file, and a way to
hand it to Krypton without plain text (open). The target is that nothing unencrypted is stored at Krypton or
travels to it, planned through a public key taken from Krypton, which does not exist yet. For now the file stays in
the Kubernetes Secret as is, and how it reaches Krypton is open. Nothing else.

**How Krypton authenticates (example):** it builds a short-lived JWT (JSON Web Token) naming the service account
email and signs it with the private key. It sends the JWT to the token address from the file. Google checks the
signature against the public half of the key and returns an access token valid for one hour. Krypton sends its
`Encrypt` and `Decrypt` calls with that token. The private key itself is never sent.

**Tenant isolation:** the service account, the bindings and the key belong to the customer, and the bindings name
only this one key. Nothing restricts the context of a call: GCP IAM cannot pin the additional authenticated
data (the context string bound into the ciphertext) of an `Encrypt` or `Decrypt` call. So the separation is one
service account per customer key, and nothing more.
**Downsides:**
- The secret is long-lived. The key never expires on its own, it is stored as a Kubernetes Secret in the customer's
  workspace (and at Krypton), and it passes through the customer's machine when downloaded. Whoever gets the file
  acts as the service account until the key is deleted.
- The customer's organisation may refuse it. The customer needs an exception to
  `iam.managed.disableServiceAccountKeyCreation` from their Organization Policy Administrator, and a security
  team may say no. Two more policies can get in the way: `iam.disableServiceAccountCreation` blocks creating the
  service account, and `iam.serviceAccountKeyExpiryHours` forces keys to expire.
- The customer rotates by hand: create a new key, upload it, delete the old one. A service account holds up to 10
  keys, so old and new overlap. Google recommends rotation at least every 90 days.
- Google disables service account keys it finds in public repositories by default, which stops access without
  warning.
- Google itself calls keys the exception and recommends another method where one is possible.

**Cut-off:** the customer disables the service account, or deletes the key, or removes the bindings. Google names
disabling or deleting the service account as the way to revoke short-lived credentials already issued; deleting the
key alone does not revoke them. IAM changes take effect typically after 2 minutes, potentially 7 or longer.

## 2. Identity provider

An identity provider (IdP) vouches for OpenKCM, and Google trusts that IdP. No Google secret is stored at OpenKCM.
Google calls this Workload Identity Federation. OpenKCM provides no IdP of its own: the IdP is the customer's.

### A. Customer's identity provider (OIDC)

**What it is:** the customer registers OpenKCM as an application in their own IdP (for example Microsoft Entra ID,
Okta or Keycloak) and tells Google to trust tokens from that IdP. OpenKCM provides no IdP of its own.

**How it works:**
1. Krypton logs in to the customer's IdP with the application's client ID and secret and gets a signed token.
2. Krypton sends that token to Google.
3. Google checks it against a provider the customer set up in their project: signed by this IdP, meant for
   OpenKCM, issued to the OpenKCM application.
4. If it passes, Google returns a short-lived access token, and Krypton calls the KMS key with it.

**Before anything:**
- A GCP project with a symmetric Cloud KMS key (Cloud KMS API enabled), and a project for the workload identity
  pool. It can be the same project; Google recommends a separate one.
- In the pool project, four APIs enabled: IAM (holds the pool and the provider), STS (swaps the tokens), Resource
  Manager and Service Account Credentials (Google lists both as required). A missing one fails with a "service
  disabled" error.
- An OIDC IdP the customer controls, publishing its discovery document and public keys at a public address.
- In that IdP, an application for OpenKCM that uses the client credentials grant: the application, not a person,
  logs in with its own client ID and secret and gets a signed token back.
- The rights to create a pool and a provider (Workload Identity Pool Admin or project Owner) and to change
  permissions on the KMS key (Owner or Cloud KMS Admin). If the organisation limits which IdPs are allowed (not by
  default), its policy administrator adds this IdP's issuer.

**What the customer provides:**

| # | Value | Where it comes from | Secret | Goes into | Step |
|---|---|---|---|---|---|
| 1 | KMS key resource name | the KMS key (console: copy resource name) | no | `spec.keyUri` | 2 |
| 2 | Issuer URL | the IdP's discovery document | no | the Terraform file only (kept in the UI form) | 2 |
| 3 | Client ID | the OpenKCM application in the IdP | no | Secret entry `clientId` | 2 |
| 4 | Token audience | the `aud` claim of the application's tokens | no | the Terraform file only (UI form) | 2 |
| 5 | Provider path | printed by `terraform apply` | no | `spec.auth.wifCustomerIdP.providerPath` | 5 |
| 6 | Token endpoint | the IdP's discovery document | no | `spec.auth.wifCustomerIdP.tokenUrl` | 5 |
| 7 | Scope | the IdP, if it needs one (see "About the scope") | no | `spec.auth.wifCustomerIdP.scope` | 5 |
| 8 | Client secret | the OpenKCM application in the IdP | yes | Secret entry `clientSecret` | 5 |

The UI sets the rest: `spec.tenantNameRef`, `spec.auth.method: WIFCustomerIdP` and `spec.auth.wifCustomerIdP.secretRef`
(set when the UI creates the Secret `gcprootkey-<UID>` from values 3 and 8). The client ID is not secret but sits in the
Secret with the client secret, so the credentials are in one place and a rotation is one write. The Terraform file needs
the issuer URL, the client ID and the token audience in step 2, so the customer enters them there and the UI keeps them
in the form: the client ID until it creates the Secret in step 5, the issuer URL and the token audience until the
Terraform file is shown. The draft GCPRootKey holds none of them. The controller sends neither the issuer URL nor the
token audience to Krypton: only Google's provider checks them. The token endpoint is in the discovery document, so the
UI can fill it from the issuer URL.

**About the scope:** the scope is a standard OAuth 2.0 parameter of the token request that Krypton sends to the
customer's IdP. It tells the IdP what the token is for. It is the IdP's scope, not a Google one, and Google never sees
it. The field exists because some IdPs refuse the request without one (Microsoft Entra ID needs the resource
identifier with `/.default`, Okta needs a scope the customer created), while others apply a default, and then the
customer leaves it empty. The customer takes the value from the IdP's documentation for the client credentials grant,
and Krypton sends it as entered.

**What the customer does:**
1. In the IdP, registers the OpenKCM application and notes its client ID and secret, the issuer URL, the token
   endpoint, the scope if the IdP needs one, and the `aud` of a token the application gets.
2. In the UI, picks GCP KMS with an IdP and enters what the Terraform file needs:
    - **KMS key resource name:** the key the permissions are put on.
    - **Issuer URL:** Google fetches the IdP's public keys from it to check the token's signature.
    - **Client ID:** only tokens whose `sub` equals it are accepted. Krypton also logs in to the IdP with it.
    - **Token audience:** only tokens whose `aud` equals it are accepted.

   The UI creates a draft GCPRootKey with the key resource name, and gives a Terraform file with these values
   filled in.
3. In Cloud Shell, or a terminal logged in to GCP, applies the file: `terraform init`, then `terraform apply`. It
   changes the pool project and the KMS key.
4. `terraform apply` prints one value, the **provider path** (see Common rules): the name of the provider it has just
   created, with the project number (not the project ID). Terraform's own attribute lacks the `//iam.googleapis.com/`
   prefix that Google expects, so the file adds it.
5. In the UI, enters what Krypton needs to get tokens:
    - **Provider path:** the value printed in step 4, copied as is.
    - **Token endpoint:** where Krypton asks the IdP for a token.
    - **Scope:** sent in that token request, only if the IdP needs one. Terraform does not use it, so it is entered
      here and not in step 2.
    - **Client secret:** Krypton logs in to the IdP with it and the client ID.

   The UI first creates the Secret `gcprootkey-<UID>` (the UID of the draft) with the client ID and the client secret,
   then saves the provider path, the token endpoint, the scope and the `secretRef` to the GCPRootKey. That completes the
   key and starts registration.

**What the Terraform file creates.** Only settings, nothing secret:
- **A pool:** a container for identities from outside Google. Empty on its own.
- **A provider in the pool:** Google's record of the customer's IdP. It holds four values:
    - **Issuer URI:** the issuer URL. Google fetches the IdP's public keys from it and checks the token's signature.
    - **Allowed audience:** the token audience, for example `openkcm-gcp`. A token with a different `aud` is
      refused. There is no fixed value: the customer sets it in the IdP and enters it in the UI.
    - **Attribute mapping:** the token's `sub` becomes the application's name in the pool. The bindings use that name.
    - **Attribute condition:** `sub` must equal the OpenKCM client ID, so tokens of other applications at the same
      IdP are refused.
- **Two bindings on the KMS key itself** (not the key ring or the project), both for the OpenKCM application's
  name in the pool: one to encrypt and decrypt with this key only, one to read the key's state.

The file creates no service account and no secret, and does not enable APIs, change organisation policy, or create
the KMS key or the IdP application.

**Example:** the OpenKCM application has client ID `0oa1b2c3d4`. Its tokens carry
`iss` = `https://login.acme.example/oauth2/default`, `sub` = `0oa1b2c3d4` and `aud` = `openkcm-gcp`.

| Value | In the IdP | In the GCPRootKey | In Google (the provider) |
|---|---|---|---|
| Client ID `0oa1b2c3d4` | app registration; `sub` of its tokens | the Secret, `clientId` | condition: `sub` equals it |
| Client secret | app registration | the Secret, `clientSecret` | not stored |
| Issuer URL | the IdP's address | not kept (Terraform only) | issuer URI |
| Token audience `openkcm-gcp` | audience setting; `aud` of its tokens | not kept (Terraform only) | allowed audience |

Who uses or checks each value:
- Client ID: Krypton logs in to the IdP with it; Google checks `sub` against it.
- Client secret: Krypton logs in to the IdP with it.
- Issuer URL: Google fetches the IdP's keys from it and checks the signature.
- Token audience: Google checks `aud` against it.

Krypton passes the token to Google unchanged, with the provider path so Google knows which provider to use. Only
Google checks `iss`, `sub` and `aud`; the controller and Krypton check none of them.

**Where the values are kept:** the client ID and the client secret in a Kubernetes Secret in the customer's kcp
workspace (entries `clientId` and `clientSecret`), created by the UI in step 5, which the GCPRootKey only
references. The provider path, the token endpoint and the scope are in the GCPRootKey spec; the issuer URL and the
token audience are not kept, because only the Terraform file needs them. No secret value is ever written into the
GCPRootKey, its status, a log or an event.

**Example:**

```yaml
apiVersion: operations.openkcm.io/v1alpha1
kind: GCPRootKey
metadata:
  name: acme-gcp-root
  namespace: default
  uid: 9b4e2d71-6c3a-4e58-a0f2-1d7c8b3e5a96
spec:
  tenantNameRef: acme
  keyUri: projects/acme-prod/locations/europe-west3/keyRings/openkcm/cryptoKeys/l1-root
  auth:
    method: WIFCustomerIdP
    wifCustomerIdP:
      providerPath: //iam.googleapis.com/projects/123456789012/locations/global/workloadIdentityPools/kms/providers/acme
      tokenUrl: https://login.acme.example/oauth2/default/v1/token
      scope: <scope, if the IdP needs one>
      secretRef:
        name: gcprootkey-9b4e2d71-6c3a-4e58-a0f2-1d7c8b3e5a96
        namespace: default
```

The Secret the UI creates and the GCPRootKey points at:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: gcprootkey-9b4e2d71-6c3a-4e58-a0f2-1d7c8b3e5a96
  namespace: default
type: Opaque
stringData:
  clientId: <client ID>
  clientSecret: <client secret>
```

**What the controller sends:** the method, the tenant ID, the key resource name, the token endpoint, the client
ID, the scope, the provider path and the client secret (how the secret reaches Krypton is open, see Common rules).
The issuer URL and the token audience are only for the Terraform file. Registration, validation and activation then
follow (see the last section).

**Required from Platform Mesh:** a Kubernetes Secret for the client ID and secret, and a way to hand them to Krypton
without plain text (open). The plan is a public key taken from Krypton, which does not exist yet; for now they stay
in the Kubernetes Secret as is.

**How Krypton authenticates (example):** steps 1 to 4 of "How it works". Krypton repeats them when the Google
token expires, and signs nothing itself.

**Tenant isolation:** the IdP, the application, the pool, the provider and the bindings belong to the customer.
Only the OpenKCM application's tokens pass the provider, and only that application has permissions on the key. As
with the service account key, Google cannot restrict the context of an `Encrypt` or `Decrypt` call, so the
separation is one application and one provider per customer key.

**Downsides:** the client secret is long-lived, stored at OpenKCM and rotated by hand. Access depends on the
customer's IdP being up and reachable from the internet. IdPs differ: `sub` is not always the client ID, and some
set `aud` only from the token request, so each IdP needs checking. KMS with a federated token and no service account in
between is supported: Google lists Cloud KMS as GA for federated identities with no known limitations.

**Cut-off:** the customer disables the application in the IdP, removes the bindings, or disables the provider or
the pool. Disabling the pool also blocks Google tokens already issued. Disabling the application or the provider
stops only new ones: a token already issued lives until it expires. Removing the bindings is an IAM change that
applies after IAM propagation (typically 2 minutes, potentially 7 or longer); Google does not say whether it also
cuts tokens already issued.

## 3. Certificate

Krypton proves who it is with an X.509 client certificate. The customer tells Google to trust certificates from
one certificate authority (CA). No Google secret is stored at OpenKCM. The two variants differ in who the CA is.

**How it works (both variants, an example):**
1. Krypton connects to Google's token service and presents its client certificate. The connection itself (mutual
   TLS) proves that Krypton holds the certificate's private key; the private key is never sent.
2. With the certificate, Krypton sends the provider path, so Google knows which provider checks it.
3. Google checks that the certificate was issued by the CA in the provider and carries the expected name.
4. If it passes, Google returns a short-lived access token, and Krypton calls the KMS key with it.

**Before anything (both variants):**
- A GCP project with a symmetric Cloud KMS key (Cloud KMS API enabled), and a project for the workload identity
  pool, with billing on. It can be the same project; Google recommends a separate one.
- In the pool project, the same four APIs as for the identity provider: IAM, STS, Resource Manager and Service
  Account Credentials. A missing one fails with a "service disabled" error.
- The rights to create a pool and a provider (Workload Identity Pool Admin or project Owner) and to change
  permissions on the KMS key (Owner or Cloud KMS Admin).

**What the Terraform file creates (both variants).** Only settings, nothing secret:
- **A pool:** a container for identities from outside Google. Empty on its own.
- **A provider in the pool**, of the certificate type. It holds three values:
    - **Trust store:** the CA certificate. Google accepts only client certificates issued by it. It holds up to three
      trust anchors, so an old and a new CA can overlap.
    - **Attribute mapping:** the certificate's common name (CN) becomes Krypton's name in the pool. The bindings use
      that name.
    - **Attribute condition:** the CN must equal the expected name, so a certificate from the same CA with any other
      name is refused.
- **Two bindings on the KMS key itself** (not the key ring or the project), both for that name in the pool: one to
  encrypt and decrypt with this key only, one to read the key's state.

`terraform apply` prints one value, the provider path (see Common rules): the name of the provider it has just
created, with the project number (not the project ID). Terraform's own attribute lacks the `//iam.googleapis.com/`
prefix that Google expects, so the file adds it. The file does not enable APIs or create the KMS key.

**Downsides (both variants):** Google documents no revocation check (CRL or OCSP) for these certificates, so none
should be assumed. To stop one certificate, the customer changes the trust store, the condition or the bindings, so
short-lived certificates are the practical way to limit a leak. Google accepts client certificates that live at most
390 days. As for the identity provider, Google lists Cloud KMS as GA for federated identities with no known
limitations, so no service account is needed in between.

### A. Customer's certificate

**Who the CA is:** the customer's own PKI. The customer issues a client certificate for OpenKCM and gives OpenKCM
the certificate and its private key.

**Before anything:** besides the points above, a private CA in the customer's PKI that can issue the client
certificate (RSA 2048 to 4096 bits, or ECDSA P-256 or P-384). Google warns against trusting a public CA, because
anyone can get a certificate from one. The CA certificate the customer enters becomes the trust anchor, so it
should be the root of the chain: Google does not say whether an intermediate can be the anchor.

**What the customer provides:**

| # | Value | Where it comes from | Secret | Goes into | Step |
|---|---|---|---|---|---|
| 1 | KMS key resource name | the KMS key (console: copy resource name) | no | `spec.keyUri` | 2 |
| 2 | CA certificate (PEM) | the customer's PKI | no | Secret entry `ca.crt`, and the Terraform file | 2 |
| 3 | Subject CN of the client certificate | the customer's PKI | no | the Terraform file only (UI form) | 2 |
| 4 | Provider path | printed by `terraform apply` | no | `spec.auth.wifX509.providerPath` | 5 |
| 5 | Client certificate (PEM) | the customer's PKI | no | Secret entry `tls.crt` | 5 |
| 6 | Client private key (PEM) | the customer's PKI | yes | Secret entry `tls.key` | 5 |

The UI sets the rest: `spec.tenantNameRef`, `spec.auth.method: WIFX509`, `spec.auth.wifX509.certificateSource: Customer`
and `spec.auth.wifX509.secretRef` (set when the UI creates the Secret `gcprootkey-<UID>` from values 2, 5 and 6). The
three certificate values sit in one Secret of type `kubernetes.io/tls`, the standard Kubernetes format for a certificate
with its key; the CA certificate is its `ca.crt` entry. Only the key is secret. The Terraform file needs the CA
certificate and the subject CN in step 2, so the customer gives them there and the UI keeps them in the form: the CA
certificate until it creates the Secret in step 5, the subject CN until the Terraform file is shown. The draft
GCPRootKey holds neither. The subject CN is not kept: it is already in the client certificate. If the client certificate
is issued by an intermediate CA, value 5 (`tls.crt`) holds the client certificate first, then the intermediate
certificates.

**What the customer does:**
1. In their PKI, issues a client certificate for OpenKCM, valid at most 390 days, and keeps the CA certificate,
   the client certificate and its private key.
2. In the UI, picks GCP KMS with a certificate (own CA) and enters what the Terraform file needs:
    - **KMS key resource name:** the key the permissions are put on.
    - **CA certificate:** Google accepts only client certificates issued by this CA.
    - **Subject CN:** only a certificate with this name is accepted.

   The UI creates a draft GCPRootKey with the key resource name, and gives a Terraform file with these values
   filled in.
3. In Cloud Shell, or a terminal logged in to GCP, applies the file: `terraform init`, then `terraform apply`. It
   changes the pool project and the KMS key.
4. Terraform returns the provider path.
5. In the UI, enters what Krypton needs:
    - **Provider path:** the value from step 4.
    - **Client certificate:** Krypton presents it to Google.
    - **Client private key:** Krypton proves with it that the certificate is its own. It is never sent to Google.

   The UI first creates the Secret `gcprootkey-<UID>` (the UID of the draft) with the client certificate, the private
   key and the CA certificate, then saves the provider path and the `secretRef` to the GCPRootKey. That completes the
   key and starts registration.

**Where the values are kept:** the client certificate, the client private key and the CA certificate in one
Kubernetes Secret of type `kubernetes.io/tls` in the customer's kcp workspace (entries `tls.crt`, `tls.key` and
`ca.crt`), created by the UI in step 5, which the GCPRootKey only references. The provider path is public and kept in
the GCPRootKey spec. The subject CN is not kept: the UI uses it only to fill the Terraform file, and it is also in
the client certificate. No secret value is ever written into the GCPRootKey, its status, a log or an event.

**Example:**

```yaml
apiVersion: operations.openkcm.io/v1alpha1
kind: GCPRootKey
metadata:
  name: acme-gcp-root
  namespace: default
  uid: 9b4e2d71-6c3a-4e58-a0f2-1d7c8b3e5a96
spec:
  tenantNameRef: acme
  keyUri: projects/acme-prod/locations/europe-west3/keyRings/openkcm/cryptoKeys/l1-root
  auth:
    method: WIFX509
    wifX509:
      certificateSource: Customer
      providerPath: //iam.googleapis.com/projects/123456789012/locations/global/workloadIdentityPools/kms/providers/acme
      secretRef:
        name: gcprootkey-9b4e2d71-6c3a-4e58-a0f2-1d7c8b3e5a96
        namespace: default
```

The Secret the UI creates and the GCPRootKey points at:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: gcprootkey-9b4e2d71-6c3a-4e58-a0f2-1d7c8b3e5a96
  namespace: default
type: kubernetes.io/tls
stringData:
  tls.crt: <client certificate, PEM>
  tls.key: <client private key, PEM>
  ca.crt: <CA certificate, PEM>
```

**What the controller sends:** the method, the tenant ID, the key resource name, the provider path, the client
certificate and the client private key (how the key reaches Krypton is open, see Common rules). The CA certificate
stays in the Secret. Registration, validation and activation then follow (see the last section).

**Required from Platform Mesh:** a Kubernetes Secret for the client certificate, the private key and the CA
certificate, and a way to hand the key to Krypton without plain text (open). The plan is a public key taken from
Krypton, which does not exist yet; for now the private key stays in the Kubernetes Secret as is. No PKI on our
side.

**How Krypton authenticates (example):** steps 1 to 4 of "How it works", with the customer's certificate and
private key. Krypton repeats them when the Google token expires.

**Tenant isolation:** the CA, the certificate, the pool, the provider and the bindings belong to the customer. The
provider trusts only the customer's CA and only the expected name, and the bindings name only that identity.
**Advantage:** no PKI is needed on the Platform Mesh side, and the customer keeps the certificate under control in
their own PKI.
**Downside:** a long-lived private key is stored at OpenKCM. The customer renews the client certificate at least
every 390 days and enters the new certificate and key in the UI; otherwise access stops.
**Cut-off:** the customer removes the bindings, removes the CA from the trust store, or disables the provider or
the pool. Disabling the pool also blocks Google tokens already issued. Removing the CA or disabling the provider
stops only new ones: a token already issued lives until it expires. Removing the bindings is an IAM change that
applies after IAM propagation (typically 2 minutes, potentially 7 or longer); Google does not say whether it also
cuts tokens already issued.

### B. Platform Mesh PKI

**Who the CA is:** the Platform Mesh PKI (OpenBao). OpenKCM runs no CA of its own. The customer does not provide the
certificate: Platform Mesh generates it. The controller creates a cert-manager `Certificate` whose issuer is the
Platform Mesh PKI, and cert-manager writes the short-lived certificate and its key into a Secret of type
`kubernetes.io/tls`, renewing it before it expires. The `Certificate` and the Secret live in the cluster where the
controller runs (a normal Kubernetes cluster, where cert-manager can run; a kcp workspace has no workloads), in a
namespace owned by the platform. The customer never sees or touches them. The GCPRootKey points at the Secret with
`secretRef` in the spec, as in 3. A, but the controller writes it, not the UI: the customer gives no secret, so the
UI creates no Secret. The name is the same as in every other method, derived from the UID of the GCPRootKey
(`gcprootkey-<UID>`), which the API server assigns when the draft is created and which is unique across all
workspaces. The controller creates the `Certificate`
and the Secret under that name and, when the Secret exists, writes the reference into the spec. The customer can edit
the spec, so on every reconcile the controller accepts only the expected name and namespace; if the field says
anything else, it reads no Secret and shows the reason in the status. This way the controller can watch the Secret
and follow every renewal. Krypton cannot fetch a
certificate from Platform Mesh itself, so the controller reads the Secret and sends the certificate to Krypton. The
customer gets the CA's public certificate from the GCPRootKey status, and the Terraform file puts it into Google.

**What the customer provides:**

| # | Value | Where it comes from | Secret | Goes into | Step |
|---|---|---|---|---|---|
| 1 | KMS key resource name | the KMS key (console: copy resource name) | no | `spec.keyUri` | 1 |
| 2 | Provider path | printed by `terraform apply` | no | `spec.auth.wifX509.providerPath` | 7 |

The UI sets the rest: `spec.tenantNameRef`, `spec.auth.method: WIFX509`,
`spec.auth.wifX509.certificateSource: PlatformMesh`. The controller sets `spec.auth.wifX509.secretRef` (the name and
namespace of the Secret that cert-manager creates in the controller's cluster) once the Secret exists. The customer
gives no secret and does not choose this reference.

**What the customer does:**
1. In the UI, picks GCP KMS with a certificate (Platform Mesh PKI) and enters the KMS key resource name. The UI
   creates a draft GCPRootKey.
2. The controller creates the cert-manager `Certificate` for this root key (see the example below). When cert-manager
   has created the Secret, the controller writes `spec.auth.wifX509.secretRef` (a patch that touches only this field)
   and sets `CertificateReady: True`. While the certificate is not issued, the status shows a condition (for example
   `CertificateReady: False`) with the reason, such as the Platform Mesh PKI being unreachable.
3. The controller writes the CA data into the GCPRootKey status (`status.clientCertificate`). It has two values:
    - **CA certificate:** the CA. Google will accept only certificates issued by it.
    - **Subject CN:** the name on Krypton's certificates for this tenant. Only it is accepted.

   The CA certificate comes from the generated Secret (`ca.crt`), and the subject CN from the `Certificate`. This
   method needs nothing from Krypton at this point.
4. The UI gives a Terraform file with the key name and the CA data filled in. The customer types nothing into it.
5. In Cloud Shell, or a terminal logged in to GCP, applies the file: `terraform init`, then `terraform apply`. It
   changes the pool project and the KMS key.
6. Terraform returns the provider path.
7. In the UI, enters the provider path. The GCPRootKey is complete. When the certificate is issued, the controller
   registers the root key (see the last section).

**Where the values are kept:** the customer provides nothing secret. The certificate and key are in the Secret that
cert-manager creates in the controller's cluster; the CA data is in the GCPRootKey status; the key name, the
provider path and the Secret reference are in the spec.

**Example:**

```yaml
apiVersion: operations.openkcm.io/v1alpha1
kind: GCPRootKey
metadata:
  name: acme-gcp-root
  namespace: default
  uid: 9b4e2d71-6c3a-4e58-a0f2-1d7c8b3e5a96
spec:
  tenantNameRef: acme
  keyUri: projects/acme-prod/locations/europe-west3/keyRings/openkcm/cryptoKeys/l1-root
  auth:
    method: WIFX509
    wifX509:
      certificateSource: PlatformMesh
      providerPath: //iam.googleapis.com/projects/123456789012/locations/global/workloadIdentityPools/kms/providers/acme
      secretRef:
        name: gcprootkey-9b4e2d71-6c3a-4e58-a0f2-1d7c8b3e5a96
        namespace: openkcm-platform
status:
  conditions:
  - type: CertificateReady
    status: "True"
  clientCertificate:
    caCertificatePem: |
      -----BEGIN CERTIFICATE-----
      MIIB...
      -----END CERTIFICATE-----
    subjectCn: openkcm-3f2a9c1e
```

The `Certificate` the controller creates for this root key, in the cluster where it runs:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: gcprootkey-9b4e2d71-6c3a-4e58-a0f2-1d7c8b3e5a96
  namespace: openkcm-platform
spec:
  secretName: gcprootkey-9b4e2d71-6c3a-4e58-a0f2-1d7c8b3e5a96
  commonName: openkcm-3f2a9c1e
  usages:
  - digital signature
  - client auth
  duration: 24h
  renewBefore: 8h
  issuerRef:
    group: cert-manager.io
    kind: Issuer
    name: tenant-3f2a9c1e
  secretTemplate:
    labels:
      openkcm.io/root-key-credentials: "true"
```

- The names derive from the UID of the GCPRootKey (`metadata.uid`), which the API server assigns: two workspaces can
  both have an `acme-gcp-root`, but never the same UID.
- `commonName` is the subject CN that the provider's attribute condition checks. The customer does not choose it.
- `usages` lists the key usages of a TLS client certificate, and cert-manager's list replaces its defaults.
- `duration` is far below the 390 days that Google accepts.
- `secretTemplate` puts the label on the Secret. The controller watches Secrets by this label, not every Secret. It maps
  a changed Secret to its root key through the UID in the Secret name, so the label carries no root key identity.
- The issuer is one per tenant if the CA is per tenant.
- Names, namespace, label key and lifetimes are illustrative; the platform chooses them.
- `ca.crt` in the Secret and `caCertificatePem` in `status.clientCertificate` are the same CA certificate. The status
  copy lets the UI fill the Terraform file without reading the Secret that holds the private key.
- When the GCPRootKey is deleted, a finalizer on it deletes the `Certificate` and the Secret first. An
  ownerReference cannot span the kcp workspace and the controller's cluster.

The Secret cert-manager creates from it:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: gcprootkey-9b4e2d71-6c3a-4e58-a0f2-1d7c8b3e5a96
  namespace: openkcm-platform
  labels:
    openkcm.io/root-key-credentials: "true"
type: kubernetes.io/tls
stringData:
  tls.crt: <client certificate, PEM>
  tls.key: <client private key, PEM>
  ca.crt: <CA certificate, PEM>
```

**What the controller sends:** the method, the tenant ID, the key resource name, the provider path, the client
certificate and the client private key from the Secret (how the key reaches Krypton is open, see Common rules). When
cert-manager renews the certificate, the controller sends the new one (see Secret changes). Registration,
validation and activation then follow (see the last section).

**Required from Platform Mesh:** the OpenBao PKI with a CA (per tenant or shared, open), cert-manager with an issuer
for that PKI in the cluster where the controller runs, and a way to hand the private key to Krypton without plain
text (open). Neither the PKI nor cert-manager is offered on Platform Mesh today. The customer gives no secret.

**How Krypton authenticates (example):** it uses the certificate and private key that the controller sent, then
follows steps 1 to 4 of "How it works".

**Tenant isolation:** the provider trusts only the CA in its trust store and only this tenant's name, and the
bindings name only that identity. Platform Mesh sets the name in the `Certificate`; the customer cannot choose it and
cannot reach the `Certificate` or its Secret. Whether the CA is per tenant is open (see below).
**Advantage:** no secret from the customer and no long-lived credential: the certificates are short-lived and
renewed automatically. The customer enters only the provider path.
**Downside:** depends on the PKI and cert-manager in Platform Mesh, and every renewal must reach Krypton through the
controller. The customer must put the new CA into the provider before the not-after date, otherwise Krypton loses
access to the key. The trust store holds up to three trust anchors, so old and new can overlap.
**Cut-off:** as in variant A.

**Required for this method:**
- The `Certificate` and its Secret live in the cluster where the controller runs, not in the customer's workspace, so
  the customer never holds Krypton's private key. The controller then needs a client for its own cluster and the
  right to manage `certificates` and `secrets` there.
- `spec.auth.wifX509.secretRef` points at that Secret (name and namespace in the controller's cluster), and the
  controller follows it to see every renewal. The controller writes the reference itself, once the Secret exists, with
  a patch that touches only this field, so the UI's completion write is not overwritten. It accepts only the expected
  name and namespace (derived from the UID) and sets
  `CertificateReady: False` with the reason for any other value, so a customer cannot point it at another Secret
  in the platform's cluster.

**Open (Platform Mesh side), to settle before this variant is built:**
- Whether the cluster where the controller runs has cert-manager with a reachable issuer for the Platform Mesh PKI.
- Whether the controller may update the spec of the root key (not only its status) through the APIExport. It writes
  `secretRef` there.
- One CA per tenant, or one shared CA with a subject CN per tenant. With a shared CA, tenant isolation rests on the
  CN condition in the provider alone.

## Quick comparison

For every method the customer also enters the KMS key resource name.

| Kind | Variant | Customer enters | Secret from the customer |
|---|---|---|---|
| 1. Static secret | A. Service account key | the key file | yes, private key |
| 2. Identity provider | A. Customer's IdP | issuer, client ID/secret, audience, provider path, token URL, scope | yes |
| 3. Certificate | A. Customer's certificate | CA, subject CN, provider path, certificate, private key | yes |
| | B. Platform Mesh PKI | provider path | no |

| Kind | Variant | Customer renews |
|---|---|---|
| 1. Static secret | A. Service account key | the key |
| 2. Identity provider | A. Customer's IdP (OIDC) | the client secret |
| 3. Certificate | A. Customer's certificate | the client certificate, at least every 390 days |
| | B. Platform Mesh PKI | the CA in the provider, before it expires |

A changed Secret reaches Krypton through the controller (see Secret changes). In the Platform Mesh PKI variant
cert-manager renews the client certificate itself.

## Registration, validation and activation (the same for every method)

This starts when the GCPRootKey is complete: every field the method needs is set. For the Platform Mesh PKI the
certificate must also be issued.

1. The controller calls Krypton to register the root key with the method and its values (working name, the API is
   designed by the Krypton team). What it sends is described in each section under "What the controller sends".
2. Krypton answers at once with the key in the pre-activation state. The check continues in the background.
3. The controller polls Krypton every 5 seconds while the key is in pre-activation. The root key shows pending.
4. Krypton gets Google credentials. This is the only part that depends on the method, see "How Krypton
   authenticates" in each section.
5. Krypton checks that the key is usable on KMS: it reads the key's state, then runs `Encrypt` and `Decrypt`.
6. Krypton records the result: passed, or failed with a reason.
7. If the check passed, the controller calls `ActivateKey`. A key cannot go back to pre-activation, so this happens
   only after a passed check. If the check failed, the controller does not activate the key and writes the reason
   into the GCPRootKey status.
8. The controller sets the GCPRootKey status to Ready and the key to Active, and the UI shows it.

From registration on, when a Secret of the GCPRootKey changes, the controller sends the new values to Krypton (see
Common rules, Secret changes).
