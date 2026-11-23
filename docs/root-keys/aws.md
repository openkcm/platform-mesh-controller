# AWS KMS as L1: how Krypton authenticates

The customer keeps the L1 root key in their own AWS KMS. Krypton needs AWS credentials to call that key. This file
lists the ways Krypton can authenticate to the customer's AWS account, in three kinds, and for each one what the
customer provides, where it comes from, where Platform Mesh keeps it, how the AWSRootKey looks, and what the method
requires from Platform Mesh. The flow that is the same for every provider is in
[root-key-flow.md](root-key-flow.md).

| Kind | Method | Variant |
|---|---|---|
| [1. Static secret](#1-static-secret) | Access key | [A. Access key](#a-access-key) |
| [2. Identity provider](#2-identity-provider) | OIDC | [A. Customer's IdP](#a-customers-identity-provider) |
| | SAML | [B. Customer's SAML identity provider](#b-customers-saml-identity-provider) (after OIDC) |
| [3. Certificate](#3-certificate) | Roles Anywhere | [A. Customer's certificate](#a-customers-certificate) |
| | | [B. Platform Mesh PKI](#b-platform-mesh-pki) |

Static secret: the customer hands over an access key (ID and secret). Identity provider: Krypton shows a token
from an identity provider. Certificate: Krypton shows a certificate.

Every method is documented here, but not every method is implemented on day one: the implementation takes them one
by one by priority. The methods that send a secret to Krypton (the access key, identity providers A and B, and
certificates A and B) also wait for the secret hand-off to Krypton (see Common rules). Certificate B needs
cert-manager with the Platform Mesh PKI.

OpenKCM runs nothing in AWS and needs no AWS account of its own. Every AWS resource below is created in the
customer's account by a template the customer deploys. Krypton only calls the public AWS endpoints of the
customer's region.

The parts called "How Krypton authenticates" and steps 4-6 of the last section show what happens behind Krypton.
They explain each method and are examples only: that part is designed by the Krypton team.

## Common rules

- **Before any method:** OpenKCM is enabled and the tenant exists (step 0). The customer has a symmetric KMS key in
  their AWS account and knows its ARN and region.
- **Tenant ID:** the ID Krypton returns from `CreateTenant`, stored in the Tenant annotation (step 0).
- **Encryption context:** one entry, key `tenant`, value the tenant ID (`tenant=<tenant ID>`). It is not secret. For
  every method the template allows `kms:Encrypt` and `kms:Decrypt` only with this context, and Krypton sends it on
  every KMS call. The registration check depends on it (see the last section).
- **Encryption context in the status:** for every method, when the draft AWSRootKey appears, the controller asks
  Krypton for the encryption context and writes it into the AWSRootKey status (`status.encryptionContext`, shown as
  `tenant: <tenant ID>`). It is reference information that comes from Krypton. The UI fills the template only from the
  status and from what the customer typed in the form.
- **Template values first:** the role in every template names the customer's key, so the UI asks for the KMS key
  ARN and region, and any other value the template needs, before it shows the template.
- **Secrets:** all credentials of a method go into one Kubernetes Secret: the secret value and the identifiers that
  belong to it (access key ID, client ID, user name, certificates). The UI creates it in the customer's kcp workspace,
  in the namespace of the AWSRootKey, after the draft exists, and names it `awsrootkey-<UID>` (see the next rule). It
  labels it with `openkcm.io/root-key-credentials: "true"`. The AWSRootKey only references it (`secretRef` with `name`
  and `namespace`), which the UI sets when it completes the key. For the Platform Mesh PKI cert-manager creates the
  Secret instead, in the same namespace and under the same name (see 3. B), and the controller writes the reference.
  No secret value is ever written into an AWSRootKey, its status, a log or an event. The entry names are fixed per
  method and listed in each section.
- **Secret name and link:** every method names the Secret `awsrootkey-<UID>`, where UID is the `metadata.uid` of the
  AWSRootKey. The API server assigns it when the draft is created, so the UI reads it from the draft before it creates
  the Secret. The UID is unique across all workspaces, so the name alone identifies the root key. The controller accepts
  only that name, in the AWSRootKey's own namespace; if `secretRef` says anything else, it reads no Secret and shows the
  reason in the status. So a Secret belongs to exactly one AWSRootKey: two root keys never share one, a customer cannot
  point a root key at another Secret, and a root key that is deleted and created again gets a new UID and so a new
  Secret. A rotation replaces the values in the same Secret.
- **Deleting the root key (open):** a Secret named after the UID has no use once the AWSRootKey is gone. It is not
  decided who deletes it (and, for the Platform Mesh PKI, the `Certificate`): the controller with a finalizer (it then
  needs the right to delete them) or the UI.
- **Secret hand-off to Krypton (open):** the target is that nothing unencrypted is stored at Krypton or travels to it.
  The plan is that the controller encrypts the Secret with a public key taken from Krypton; that endpoint does not
  exist yet and is not agreed with the Krypton team. For now the value stays in the Kubernetes Secret as is, and how
  it reaches Krypton is open.
- **Secret changes:** the controller watches the labelled Secrets. When one changes (the customer replaces a value,
  or cert-manager renews a certificate), it finds the root key from the UID in the Secret name and tells Krypton that
  the root key has new credentials (working name, the API is designed by the Krypton team).
- **After the key is Active** the KMS key ARN, region and method do not change: a different key is a different
  root key.
- **CRD change:** the `auth` block and `status.encryptionContext` are new in the AWSRootKey CRD, so that one kind holds
  several methods. Today the CRD has `spec.rolesAnywhere` with three required ARNs, so a draft without ARNs is
  rejected. In the new CRD that block moves to `spec.auth.rolesAnywhere`; `tenantNameRef`, `region`, `keyUri`,
  `endpointUrl` and `lifecycle` stay as they are.
- **About the examples:** each YAML shows the AWSRootKey after the customer has completed it. The kind is
  namespaced; the UI creates it in the customer's workspace, `default` here. Names, ARNs, the account number and
  addresses are illustrative. `endpointUrl` is optional and `lifecycle` defaults to Active, so neither is shown.
  Fields under `auth` and `status.encryptionContext` are new; `tenantNameRef`, `region`, `keyUri` and the three
  Roles Anywhere ARNs exist in the CRD today. Secret values are written as placeholders.

## What is required from Platform Mesh

| Building block | Needed by | Offered today |
|---|---|---|
| Kubernetes Secret in the customer's kcp workspace | access key, IdPs, customer's certificate | yes |
| Permission for the controller to read and watch those Secrets | access key, IdPs, customer's certificate | no |
| Hand-off of that Secret to Krypton, not in plain text | every method | no |
| OpenBao PKI with a CA, and cert-manager for kcp workspaces with an issuer for it | Platform Mesh PKI | no |

The permission is an APIExport permission claim on `secrets`; the APIExport claims none today. The hand-off is
described in Common rules. Whether the PKI has one CA per tenant or one shared CA is open.

## Step 0. Tenant creation (the same for every method)

1. The customer enables OpenKCM in the Platform Mesh marketplace. This puts an APIBinding (the link between their
   kcp workspace and the OpenKCM API) into the workspace.
2. The controller sees the APIBinding and creates a Tenant resource in the workspace.
3. The controller asks Krypton to create the tenant (`CreateTenant`). Krypton returns a tenant ID.
4. The controller stores the tenant ID in the Tenant annotation. This is the guard: Krypton returns a new ID on
   every call, so once the annotation is set the controller never calls `CreateTenant` again.

## 1. Static secret

The customer gives OpenKCM a long-lived secret. AWS has one method of this kind: the access key.

### A. Access key

**What it is:** the customer gives OpenKCM a long-lived access key (ID and secret) of an IAM user that can only
assume one role.

**What the customer provides:**

| # | Value | Where it comes from | Secret | Goes into | Step |
|---|---|---|---|---|---|
| 1 | KMS key ARN | the customer's KMS key | no | `spec.keyUri` | 1 |
| 2 | Region of the key | the customer's KMS key | no | `spec.region` | 1 |
| 3 | Access key ID | `aws iam create-access-key`, after the template | no | Secret entry `accessKeyId` | 7 |
| 4 | Secret access key | the previous command, shown once | yes | Secret entry `secretAccessKey` | 7 |
| 5 | Role ARN | output of the template's stack | no | `spec.auth.accessKey.roleArn` | 7 |

Not typed by the customer: `spec.tenantNameRef` and `spec.auth.method: AccessKey` (set by the UI with the draft), and
`spec.auth.accessKey.secretRef` (the UI sets it when it creates the Secret `awsrootkey-<UID>` from values 3 and 4). The
access key ID is not secret but sits in the Secret with the secret access key: a rotation replaces both at once, so one
write to the Secret keeps them in step.

**What the customer does:**
1. In the UI, picks AWS KMS with access keys and enters the KMS key ARN and region. The UI creates a draft
   AWSRootKey with them.
2. The controller asks Krypton for the encryption context and writes it into the status.
3. The UI gives a CloudFormation template already filled in with the key ARN and the encryption context. The
   customer types nothing into it.
4. The customer deploys it in their AWS account.
5. The stack returns the role ARN and the user name.
6. The customer creates an access key for that user: `aws iam create-access-key --user-name <user name>`. AWS
   prints the access key ID and the secret once.
7. The customer enters the access key ID, the secret and the role ARN in the UI. The UI first creates the Secret
   `awsrootkey-<UID>` (the UID of the draft) with the access key ID and the secret, then saves the role ARN and
   `secretRef` to the AWSRootKey. That completes the key and starts registration.

**What the template creates** in the customer's AWS account:
- **An IAM user** with no console login. It only holds the access key, and its only permission is to assume the
  role below.
- **An IAM role** for OpenKCM:
    - may do only `kms:Encrypt` and `kms:Decrypt` with the encryption context, and `kms:DescribeKey`, and only on
      the customer's KMS key
    - trusts only the IAM user above
    - hands out temporary credentials, valid for one hour by default
- **A policy on the user** that lets it assume that role and nothing else.

**Where the values are kept:**
- Access key ID and secret access key: in a Kubernetes Secret in the customer's kcp workspace, created by the UI in
  step 7. The AWSRootKey holds only the reference.
- Role ARN: returned by the stack in step 5, kept in the AWSRootKey with the KMS key ARN and region.

**Example:**

```yaml
apiVersion: operations.openkcm.io/v1alpha1
kind: AWSRootKey
metadata:
  name: acme-aws-root
  namespace: default
  uid: 7c1e9b52-4a3d-4f0b-8e21-5b6d9a0c3f47
spec:
  tenantNameRef: acme
  region: eu-central-1
  keyUri: arn:aws:kms:eu-central-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab
  auth:
    method: AccessKey
    accessKey:
      roleArn: arn:aws:iam::123456789012:role/openkcm-l1-access
      secretRef:
        name: awsrootkey-7c1e9b52-4a3d-4f0b-8e21-5b6d9a0c3f47
        namespace: default
status:
  encryptionContext:
    tenant: 3f2a9c1e-7b4d-4e8a-9c61-2d5f0a7b8e13
```

The Secret the UI creates and the AWSRootKey points at:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: awsrootkey-7c1e9b52-4a3d-4f0b-8e21-5b6d9a0c3f47
  namespace: default
type: Opaque
stringData:
  accessKeyId: "<access key ID>"
  secretAccessKey: "<secret access key>"
```

**What the controller sends:** the method, the tenant ID, the key ARN, the region, the role ARN and the access key
ID and secret (how the secret reaches Krypton is open, see Common rules). Registration, validation and activation
then follow (see the last section).

**Required from Platform Mesh:** a Kubernetes Secret in the customer's workspace for the access key ID and secret,
and a way to hand that secret to Krypton without plain text (open, see Common rules).

**How Krypton authenticates (example):** it takes the ID and secret and sends `AssumeRole` for the role to AWS STS,
signed with the secret. It sends the access key ID and the signature, never the secret. STS checks the signature
and that the role trusts this user, then returns temporary credentials, and Krypton signs its KMS calls with them.

**Tenant isolation:** the IAM user, the role and the key belong to the customer. The role trusts only that user
and allows only this tenant's encryption context.
**Advantage:** the simplest method for the customer. It works with any AWS account and needs nothing on the Platform
Mesh side except the Secret.
**Downside:** a long-lived secret is stored at OpenKCM, and the customer rotates it by hand.
**Cut-off:** the customer deactivates the access key or deletes the role.

## 2. Identity provider

The customer tells AWS to trust tokens or statements from an identity provider (IdP). Krypton gets one from it and
shows it to AWS, and AWS returns temporary credentials for a role. There is no long-lived AWS secret.

**Note:** OpenKCM does not provide an identity provider. The customer brings their own.

**OIDC (variant A). What it is:** the customer tells AWS "trust identity tokens (JWT) signed by this issuer".
Krypton shows such a token to AWS STS (`AssumeRoleWithWebIdentity`) and gets temporary credentials for a role. AWS
downloads the issuer's public keys from the issuer's public address to check the signature.

**What the OIDC template creates** in the customer's AWS account:
- **An OIDC provider** with the issuer address and the audience. AWS allows one provider per issuer address in an
  account. If one already exists, the customer enters its ARN in the template's only optional parameter, and the
  template reuses it instead of creating a second one.
- **An IAM role** for OpenKCM:
    - may do only `kms:Encrypt` and `kms:Decrypt` with the encryption context, and `kms:DescribeKey`, and only on
      the customer's KMS key
    - trusts only tokens from that provider with that audience and that subject
    - hands out temporary credentials, valid for one hour by default

**After deploying:** the stack returns the role ARN and the customer enters it in the UI. The provider's ARN is
not needed: the role already names the provider.

### A. Customer's identity provider

**Who the issuer is:** the customer's own identity provider (for example Microsoft Entra ID, Okta or Keycloak).
The customer registers an application for OpenKCM in it and gives OpenKCM that application's client ID and client
secret. Krypton gets a token from the customer's identity provider and shows it to AWS.

**What the customer provides:**

| # | Value | Where it comes from | Secret | Goes into | Step |
|---|---|---|---|---|---|
| 1 | KMS key ARN | the customer's KMS key | no | `spec.keyUri` | 2 |
| 2 | Region of the key | the customer's KMS key | no | `spec.region` | 2 |
| 3 | Issuer address | the customer's identity provider | no | the template only (kept in the UI form) | 2 |
| 4 | Client ID | the application registered for OpenKCM | no | Secret entry `clientId` | 2 |
| 5 | Audience | set by the customer in the application | no | the template only (kept in the UI form) | 2 |
| 6 | Role ARN | output of the template's stack | no | `spec.auth.webIdentity.roleArn` | 5 |
| 7 | Token endpoint | the customer's identity provider | no | `spec.auth.webIdentity.tokenEndpoint` | 5 |
| 8 | Client secret | the application registered for OpenKCM | yes | Secret entry `clientSecret` | 5 |
| 9 | Scope | the identity provider, if it needs one (see "About the scope") | no | `spec.auth.webIdentity.scope` | 5 |

Not typed by the customer: `spec.tenantNameRef` and `spec.auth.method: WebIdentity` (set by the UI with the draft), and
`spec.auth.webIdentity.secretRef` (set when the UI creates the Secret `awsrootkey-<UID>` from values 4 and 8). The
client ID is not secret but sits in the Secret with the client secret, so the credentials are in one place and a
rotation is one write. The template needs the issuer address, the client ID and the audience in step 4, so the customer
enters them in step 2 and the UI keeps them in the form: the client ID until it creates the Secret in step 5, the issuer
address and the audience until the template is shown. The draft AWSRootKey holds none of them. The controller does not
send the issuer address or the audience to Krypton: only the OIDC provider and the role in AWS need them, and Krypton
uses the token endpoint.

**About the scope:** the scope is a standard OAuth 2.0 parameter of the token request that Krypton sends to the
customer's identity provider. It tells the provider what the token is for, and AWS never sees it. The field exists
because some providers refuse the request without one (Microsoft Entra ID needs the resource identifier with
`/.default`, Okta needs a scope the customer created), while others apply a default, and then the customer leaves it
empty. The customer takes the value from the provider's documentation for the client credentials grant, and Krypton
sends it as entered.

**What the customer does:**
1. In their identity provider, registers an application for OpenKCM that may get tokens with the client
   credentials grant, and notes the issuer address, the token endpoint, the client ID, the client secret, the
   audience and the scope if the identity provider needs one.
2. In the UI, picks AWS KMS with OIDC (own identity provider) and enters the KMS key ARN, region, issuer address,
   client ID and audience. Draft created with the key ARN and the region.
3. The controller asks Krypton for the encryption context and writes it into the status.
4. The UI gives the template filled in with the key ARN, the issuer address, the client ID as the subject, the
   audience and the encryption context.
5. The customer deploys it and enters in the UI the role ARN, the token endpoint, the scope (if any) and the client
   secret. The UI first creates the Secret `awsrootkey-<UID>` (the UID of the draft) with the client ID and the
   client secret, then saves the role ARN, the
   token endpoint, the scope and `secretRef` to the AWSRootKey. That completes the key and starts registration.

**To verify per identity provider:** the template uses the client ID as the subject. The `sub` claim of a
client-credentials token is not always the client ID (some providers put another identifier in `sub` and the client
ID in `azp`), and AWS may use `azp` instead of `aud` when it is present. The template needs the value that the
provider really puts into the token.

**Where the values are kept:**
- Client ID and client secret: from the application the customer registered in their identity provider. Kept in a
  Kubernetes Secret in the kcp workspace, created by the UI in step 5. The AWSRootKey holds only the reference.
- Token endpoint, scope: from the customer's identity provider, kept in the spec. The scope is sent in the
  token request, only if the identity provider needs one.
- Issuer address and audience: from the customer's identity provider, public. Not kept in the AWSRootKey: the UI uses
  them only to fill the template.
- Role ARN: returned by the stack, kept in the spec.

**Example:**

```yaml
apiVersion: operations.openkcm.io/v1alpha1
kind: AWSRootKey
metadata:
  name: acme-aws-root
  namespace: default
  uid: 7c1e9b52-4a3d-4f0b-8e21-5b6d9a0c3f47
spec:
  tenantNameRef: acme
  region: eu-central-1
  keyUri: arn:aws:kms:eu-central-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab
  auth:
    method: WebIdentity
    webIdentity:
      tokenEndpoint: https://idp.example.com/acme/oauth2/token
      scope: <scope, if the IdP needs one>
      roleArn: arn:aws:iam::123456789012:role/openkcm-l1-access
      secretRef:
        name: awsrootkey-7c1e9b52-4a3d-4f0b-8e21-5b6d9a0c3f47
        namespace: default
status:
  encryptionContext:
    tenant: 3f2a9c1e-7b4d-4e8a-9c61-2d5f0a7b8e13
```

The Secret the UI creates and the AWSRootKey points at:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: awsrootkey-7c1e9b52-4a3d-4f0b-8e21-5b6d9a0c3f47
  namespace: default
type: Opaque
stringData:
  clientId: "<client ID>"
  clientSecret: "<client secret>"
```

**What the controller sends:** the method, the tenant ID, the key ARN, the region, the role ARN, the token
endpoint, the client ID, the scope and the client secret (how the secret reaches Krypton is open, see Common
rules).

**Required from Platform Mesh:** a Kubernetes Secret in the customer's workspace for the client ID and secret, and a
way to hand them to Krypton without plain text (open). Nothing else; the identity provider is the customer's.

**How Krypton authenticates (example):** it asks the customer's token endpoint for a token (client credentials
grant, with the client ID and secret), sends the token to STS, and STS checks it against the customer's issuer.

**Tenant isolation:** the identity provider and the client belong to the customer.
**Advantage:** the customer controls access in their own identity provider and can cut it off there. No long-lived AWS
key exists.
**Downside:** a long-lived client secret is stored at OpenKCM, and the customer rotates it by hand. Access depends
on the availability of the customer's identity provider. AWS must be able to reach the issuer's discovery document
and key set over the public internet, so an identity provider reachable only inside the customer's network does not
work.
**Cut-off:** the customer disables the application in their identity provider, or deletes the role.

### B. Customer's SAML identity provider

**Comes after OIDC (variant A).** Most customers use OIDC; SAML is the older protocol. SAML has no login for
services, so Krypton logs in as a user of the customer's identity provider, with a password and with MFA switched
off for that user. It works only with identity providers that offer a login without a browser (SAML ECP), such as
Shibboleth and Keycloak. Microsoft Entra ID and AD FS have no proven way, and Okta discourages it. It needs more
manual steps than A, so it is meant for customers whose identity provider supports only SAML.

**Who the issuer is:** the customer's own SAML identity provider. The customer uploads its metadata (a public XML
file with its signing certificate) to AWS. Krypton logs in to the identity provider as a service user, gets a
signed SAML response, and exchanges it at AWS STS for temporary credentials.

**What the customer provides:**

| # | Value | Where it comes from | Secret | Goes into | Step |
|---|---|---|---|---|---|
| 1 | KMS key ARN | the customer's KMS key | no | `spec.keyUri` | 2 |
| 2 | Region of the key | the customer's KMS key | no | `spec.region` | 2 |
| 3 | Identity provider metadata (XML) | the customer's identity provider | no | `spec.auth.saml.idpMetadataXml` | 2 |
| 4 | ECP login address | the customer's identity provider | no | `spec.auth.saml.ecpUrl` | 2 |
| 5 | Service user name | the user created for OpenKCM | no | Secret entry `username` | 6 |
| 6 | SAML provider ARN | output of the template's stack | no | `spec.auth.saml.providerArn` | 6 |
| 7 | Role ARN | output of the template's stack | no | `spec.auth.saml.roleArn` | 6 |
| 8 | Service user password | the user created for OpenKCM | yes | Secret entry `password` | 6 |

Not typed by the customer: `spec.tenantNameRef`, `spec.auth.method: SAML` and `spec.auth.saml.secretRef` (set when the
UI creates the Secret `awsrootkey-<UID>` from values 5 and 8). The user name is not secret but sits in the Secret with
the password: they are one credential, and a rotation is one write.

**What the customer does:**
1. In their identity provider, creates an application for AWS with ECP allowed, and a service user for OpenKCM with
   a password and without MFA. Downloads the metadata.
2. In the UI, picks AWS KMS with SAML and enters values 1 to 4. Draft created.
3. The controller asks Krypton for the encryption context and writes it into the status.
4. The customer deploys the filled-in template. The stack returns the SAML provider ARN and the role ARN.
5. In the AWS application of their identity provider, the customer gives the service user that role (both ARNs).
6. The customer enters the two ARNs, the user name and the password in the UI. The UI first creates the Secret
   `awsrootkey-<UID>` (the UID of the draft) with the user name and the password, then saves the two ARNs and
   `secretRef` to the AWSRootKey. That completes the key and starts registration.

**What the template creates** in the customer's AWS account: a SAML provider with the metadata, and an IAM role
with the same KMS rights as in the other methods that trusts only that SAML provider.

**Where the values are kept:** the user name and the password in a Kubernetes Secret, everything else in the spec.

**Example:**

```yaml
apiVersion: operations.openkcm.io/v1alpha1
kind: AWSRootKey
metadata:
  name: acme-aws-root
  namespace: default
  uid: 7c1e9b52-4a3d-4f0b-8e21-5b6d9a0c3f47
spec:
  tenantNameRef: acme
  region: eu-central-1
  keyUri: arn:aws:kms:eu-central-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab
  auth:
    method: SAML
    saml:
      idpMetadataXml: "<identity provider metadata XML>"
      ecpUrl: https://idp.example.com/idp/profile/SAML2/SOAP/ECP
      providerArn: arn:aws:iam::123456789012:saml-provider/acme-idp
      roleArn: arn:aws:iam::123456789012:role/openkcm-l1-access
      secretRef:
        name: awsrootkey-7c1e9b52-4a3d-4f0b-8e21-5b6d9a0c3f47
        namespace: default
status:
  encryptionContext:
    tenant: 3f2a9c1e-7b4d-4e8a-9c61-2d5f0a7b8e13
```

The Secret the UI creates and the AWSRootKey points at:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: awsrootkey-7c1e9b52-4a3d-4f0b-8e21-5b6d9a0c3f47
  namespace: default
type: Opaque
stringData:
  username: "<service user name>"
  password: "<service user password>"
```

**What the controller sends:** the method, the tenant ID, the key ARN, the region, the two ARNs, the ECP login
address, the user name and the password (how the password reaches Krypton is open, see Common rules).

**Required from Platform Mesh:** a Kubernetes Secret in the customer's workspace for the user name and password, and
a way to hand them to Krypton without plain text (open).

**How Krypton authenticates (example):** it logs in at the ECP login address with the user name and password and
gets a signed SAML response. It sends the response and the two ARNs to STS (`AssumeRoleWithSAML`), which checks it
against the uploaded metadata and returns temporary credentials.

**Tenant isolation:** the identity provider, the service user and the role belong to the customer. The role
allows only this tenant's encryption context.
**Advantage:** works for a customer whose identity provider supports only SAML. No long-lived AWS key exists.
**Downside:** a password with MFA switched off, only some identity providers, more manual steps. When the
identity provider changes its signing certificate, the customer must update the metadata in AWS, otherwise access
stops.
**Cut-off:** the customer disables the service user, or deletes the role or the SAML provider.

## 3. Certificate

**Roles Anywhere. What it is:** the customer tells AWS "trust certificates from this certificate authority (CA)".
Krypton signs a `CreateSession` request with the certificate's private key and attaches the certificate, and Roles
Anywhere returns temporary credentials for a role. It is certificate based but not a TLS handshake: the
certificate travels in an HTTP header. The two variants differ in who issues the certificate.

**What every Roles Anywhere variant's template creates** in the customer's AWS account:
- **A trust anchor** holding the CA certificate. AWS accepts only certificates that chain to it.
- **A profile** that lists the role and sets the session length to 900 seconds.
- **An IAM role** for OpenKCM:
    - may do only `kms:Encrypt` and `kms:Decrypt` with the encryption context, and `kms:DescribeKey`, and only on
      the customer's KMS key
    - trusts only Roles Anywhere, only for this trust anchor, and only for certificates with the expected subject CN

**After deploying:** the stack returns three ARNs (trust anchor, profile, role). The customer enters them in the
UI, which saves all three or none.

**Downside (both variants):** the customer creates three AWS objects. Roles Anywhere can revoke a certificate only
through a revocation list the customer imports, so short-lived certificates are the practical way to limit a leak.

### A. Customer's certificate

**Who the CA is:** the customer's own PKI. The customer issues a client certificate for OpenKCM and gives OpenKCM
the certificate and its private key. This is the "static certificate" case.

**What the customer provides:**

| # | Value | Where it comes from | Secret | Goes into | Step |
|---|---|---|---|---|---|
| 1 | KMS key ARN | the customer's KMS key | no | `spec.keyUri` | 2 |
| 2 | Region of the key | the customer's KMS key | no | `spec.region` | 2 |
| 3 | CA certificate (PEM) | the customer's PKI | no | Secret entry `ca.crt`, and the template | 2 |
| 4 | Subject CN of the client certificate | the customer's PKI | no | the template only (kept in the UI form) | 2 |
| 5 | Trust anchor ARN | output of the template's stack | no | `spec.auth.rolesAnywhere.trustAnchorArn` | 5 |
| 6 | Profile ARN | output of the template's stack | no | `spec.auth.rolesAnywhere.profileArn` | 5 |
| 7 | Role ARN | output of the template's stack | no | `spec.auth.rolesAnywhere.roleArn` | 5 |
| 8 | Client certificate (PEM) | the customer's PKI | no | Secret entry `tls.crt` | 5 |
| 9 | Client private key (PEM) | the customer's PKI | yes | Secret entry `tls.key` | 5 |

Not typed by the customer: `spec.tenantNameRef`, `spec.auth.method: RolesAnywhere` and
`spec.auth.rolesAnywhere.certificateSource: Customer` (set by the UI with the draft), and
`spec.auth.rolesAnywhere.secretRef` (set when the UI creates the Secret `awsrootkey-<UID>` from values 3, 8 and 9).
Values 5 to 7 are saved together or not at all. The three certificate values sit in one Secret of type
`kubernetes.io/tls`, the standard Kubernetes format for a certificate with its key; the CA certificate is its
`ca.crt` entry. Only the key is secret. The template needs the CA certificate and the subject CN in step 4, so the
customer gives them in step 2 and the UI keeps them in the form: the CA certificate until it creates the Secret in
step 5, the subject CN until the template is shown. The draft AWSRootKey holds neither. The subject CN is not kept:
it is already in the client certificate.

**What the customer does:**
1. In their own PKI, issues a client certificate for OpenKCM and keeps the CA certificate, the client certificate
   and the client private key.
2. In the UI, picks AWS KMS with Roles Anywhere (own certificate) and enters the KMS key ARN, region, the CA
   certificate and the subject CN of the client certificate. Draft created with the key ARN and the region.
3. The controller asks Krypton for the encryption context and writes it into the status.
4. The UI gives the template filled in with the key ARN, the CA certificate, the subject CN and the encryption
   context.
5. The customer deploys it and enters in the UI the three ARNs, the client certificate and the client private key.
   The UI first creates the Secret `awsrootkey-<UID>` (the UID of the draft) with the client certificate, the private
   key and the CA certificate, then saves the three ARNs and `secretRef` to the AWSRootKey. That completes the key
   and starts registration.

**Where the values are kept:**
- Client certificate, client private key and CA certificate: from the customer's PKI. Kept in one Kubernetes Secret
  of type `kubernetes.io/tls` in the kcp workspace, created by the UI in step 5. The AWSRootKey holds only the
  reference.
- Subject CN: from the customer's PKI, public. Not kept: the UI uses it only to fill the template, and it is also in
  the client certificate.
- Three ARNs: returned by the stack, kept in the spec.

**Example:**

```yaml
apiVersion: operations.openkcm.io/v1alpha1
kind: AWSRootKey
metadata:
  name: acme-aws-root
  namespace: default
  uid: 7c1e9b52-4a3d-4f0b-8e21-5b6d9a0c3f47
spec:
  tenantNameRef: acme
  region: eu-central-1
  keyUri: arn:aws:kms:eu-central-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab
  auth:
    method: RolesAnywhere
    rolesAnywhere:
      certificateSource: Customer
      trustAnchorArn: arn:aws:rolesanywhere:eu-central-1:123456789012:trust-anchor/00000000-0000-0000-0000-000000000001
      profileArn: arn:aws:rolesanywhere:eu-central-1:123456789012:profile/00000000-0000-0000-0000-000000000002
      roleArn: arn:aws:iam::123456789012:role/openkcm-l1-access
      secretRef:
        name: awsrootkey-7c1e9b52-4a3d-4f0b-8e21-5b6d9a0c3f47
        namespace: default
status:
  encryptionContext:
    tenant: 3f2a9c1e-7b4d-4e8a-9c61-2d5f0a7b8e13
```

The Secret the UI creates and the AWSRootKey points at:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: awsrootkey-7c1e9b52-4a3d-4f0b-8e21-5b6d9a0c3f47
  namespace: default
type: kubernetes.io/tls
stringData:
  tls.crt: "<client certificate, PEM>"
  tls.key: "<client private key, PEM>"
  ca.crt: "<CA certificate, PEM>"
```

**What the controller sends:** the method, the tenant ID, the key ARN, the region, the three ARNs, the client
certificate and the client private key (how the key reaches Krypton is open, see Common rules). The CA certificate
stays in the Secret.

**Required from Platform Mesh:** a Kubernetes Secret in the customer's workspace for the client certificate, the
private key and the CA certificate, and a way to hand the key to Krypton without plain text (open). No PKI on our side.

**How Krypton authenticates (example):** it signs `CreateSession` with the private key, sending the client
certificate and the three ARNs. Roles Anywhere checks the signature and the certificate against the trust
anchor, then STS checks the role's trust policy and returns credentials for 900 seconds.

**Tenant isolation:** the CA, the certificate and the role belong to the customer. The trust anchor accepts only
the customer's CA, and the role allows only this tenant's encryption context.
**Advantage:** no PKI is needed on the Platform Mesh side, and the customer keeps the certificate under control in
their own PKI.
**Downside:** a long-lived private key is stored at OpenKCM. The customer must renew the client certificate before
it expires and update the Secret; otherwise access stops.
**Cut-off:** the customer disables the trust anchor, revokes the certificate in an imported revocation list, or
deletes the role.

### B. Platform Mesh PKI

**Who the CA is:** the Platform Mesh PKI (OpenBao). OpenKCM does not build or run a CA of its own. The customer
does not provide the certificate: Platform Mesh generates it. The controller creates a cert-manager `Certificate`
whose issuer is the Platform Mesh PKI, and cert-manager writes the short-lived certificate and its key into a Secret
of type `kubernetes.io/tls`, renewing it before it expires. The `Certificate` and the Secret live in kcp, in the
customer's workspace and in the namespace of the AWSRootKey, like every other Secret here: Platform Mesh runs
cert-manager for kcp workspaces, and cert-manager writes the Secret next to the `Certificate`. The UI never shows the
private key. The AWSRootKey points at the Secret with `secretRef` in the spec, as in 3. A, but the controller writes
it, not the UI: the customer gives no secret, so the UI creates no Secret. The name is known upfront, the same as in
every other method: `awsrootkey-<UID>`, derived from the UID that the API server assigns when the draft is created.
The controller creates the `Certificate` with that Secret name and writes the reference into the spec in the same
step. The customer can edit the spec, so on every reconcile the controller accepts only the expected name and
namespace; if the field says anything else, it reads no Secret and shows the reason in the status. This way the
controller can watch the Secret and follow every renewal. Krypton cannot fetch a
certificate from Platform Mesh itself, so the controller reads the Secret and sends the certificate to Krypton. The
customer puts the CA's public certificate into AWS.

**What the customer provides:**

| # | Value | Where it comes from | Secret | Goes into | Step |
|---|---|---|---|---|---|
| 1 | KMS key ARN | the customer's KMS key | no | `spec.keyUri` | 1 |
| 2 | Region of the key | the customer's KMS key | no | `spec.region` | 1 |
| 3 | Trust anchor ARN | output of the template's stack | no | `spec.auth.rolesAnywhere.trustAnchorArn` | 5 |
| 4 | Profile ARN | output of the template's stack | no | `spec.auth.rolesAnywhere.profileArn` | 5 |
| 5 | Role ARN | output of the template's stack | no | `spec.auth.rolesAnywhere.roleArn` | 5 |

Not typed by the customer: `spec.tenantNameRef`, `spec.auth.method: RolesAnywhere` and
`spec.auth.rolesAnywhere.certificateSource: PlatformMesh` (set by the UI with the draft), and
`spec.auth.rolesAnywhere.secretRef` (set by the controller when it creates the `Certificate`): the name and
namespace of the Secret that cert-manager creates. The customer gives no secret and does not choose this reference.
The CA certificate and the expected subject CN go into the template (the status shows them). Values 3 to 5 are saved
together or not at all.

**What the customer does:**
1. In the UI, picks AWS KMS with Roles Anywhere (Platform Mesh PKI) and enters the KMS key ARN and region. Draft
   created.
2. The controller creates the cert-manager `Certificate` for this root key in the AWSRootKey's namespace (see the
   example below), writes `spec.auth.rolesAnywhere.secretRef` (a patch that touches only this field) and asks Krypton
   for the encryption context. Repeating the call returns the same result. Meanwhile the platform issues the
   certificate, with no customer action and no call from the controller:
    - cert-manager picks up the `Certificate` and asks the issuer named in `issuerRef` for a client certificate with
      the subject CN from the `Certificate`.
    - The issuer is backed by the Platform Mesh PKI (OpenBao), which signs with its CA and returns the certificate and
      the CA certificate. cert-manager's Vault issuer type is the likely fit, because OpenBao keeps Vault's API. How
      cert-manager logs in to OpenBao is the platform's concern: the controller never talks to OpenBao and holds no
      address or credential for it.
    - cert-manager writes the Secret (`tls.crt`, `tls.key`, `ca.crt`) with the label
      `openkcm.io/root-key-credentials: "true"`, and renews it before it expires (8 hours before, in the example below).
    - When the Secret exists, the controller sets `CertificateReady: True`. While the certificate is not issued, the
      status shows
      `CertificateReady: False` with the reason, such as the Platform Mesh PKI being unreachable (the reason can be
      taken from the `Certificate`'s own condition).
3. The encryption context comes from Krypton. The CA certificate (PEM) comes from the generated Secret (`ca.crt`),
   and the expected subject CN from the `Certificate`, not from Krypton. The controller writes all of them into the
   status.
4. The UI gives the template filled in with the key ARN, the encryption context, the CA certificate and the subject
   CN. The customer types nothing into it.
5. The customer deploys it and enters the three ARNs.
6. The three ARNs complete the key. When the certificate is issued, the controller registers the root key (see the
   last section).

**Where the values are kept:** the customer provides nothing secret. The certificate and key are in the Secret that
cert-manager creates in the AWSRootKey's namespace; the CA data in the status; key ARN, region, three ARNs and the
Secret reference in the spec.

**Example:**

```yaml
apiVersion: operations.openkcm.io/v1alpha1
kind: AWSRootKey
metadata:
  name: acme-aws-root
  namespace: default
  uid: 7c1e9b52-4a3d-4f0b-8e21-5b6d9a0c3f47
spec:
  tenantNameRef: acme
  region: eu-central-1
  keyUri: arn:aws:kms:eu-central-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab
  auth:
    method: RolesAnywhere
    rolesAnywhere:
      certificateSource: PlatformMesh
      trustAnchorArn: arn:aws:rolesanywhere:eu-central-1:123456789012:trust-anchor/00000000-0000-0000-0000-000000000001
      profileArn: arn:aws:rolesanywhere:eu-central-1:123456789012:profile/00000000-0000-0000-0000-000000000002
      roleArn: arn:aws:iam::123456789012:role/openkcm-l1-access
      secretRef:
        name: awsrootkey-7c1e9b52-4a3d-4f0b-8e21-5b6d9a0c3f47
        namespace: default
status:
  conditions:
  - type: CertificateReady
    status: "True"
  encryptionContext:
    tenant: 3f2a9c1e-7b4d-4e8a-9c61-2d5f0a7b8e13
  clientCertificate:
    caCertificatePem: |
      -----BEGIN CERTIFICATE-----
      MIIB...
      -----END CERTIFICATE-----
    subjectCn: openkcm-3f2a9c1e
```

The `Certificate` the controller creates for this root key, next to the AWSRootKey:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: awsrootkey-7c1e9b52-4a3d-4f0b-8e21-5b6d9a0c3f47
  namespace: default
spec:
  secretName: awsrootkey-7c1e9b52-4a3d-4f0b-8e21-5b6d9a0c3f47
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

- The names derive from the UID of the AWSRootKey (`metadata.uid`), which the API server assigns: two workspaces can
  both have an `acme-aws-root`, but never the same UID.
- `commonName` is the subject CN that the customer's role pins. The customer does not choose it.
- `usages` lists `digital signature` next to `client auth` because Roles Anywhere expects the digital-signature key
  usage on the client certificate, and cert-manager's list replaces its defaults.
- `ca.crt` in the Secret and `caCertificatePem` in `status.clientCertificate` are the same CA certificate. The status
  copy lets the UI fill the template without reading the Secret that holds the private key.
- `secretTemplate` puts the label on the Secret. The controller watches Secrets by this label, not every Secret. It maps
  a changed Secret to its root key through the UID in the Secret name, so the label carries no root key identity.
- The issuer is one per tenant if the CA is per tenant (open, see below).
- Names, namespace, label key and lifetimes are illustrative; the platform chooses them.
- Deleting the AWSRootKey: see Common rules (open).

The Secret cert-manager creates from it:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: awsrootkey-7c1e9b52-4a3d-4f0b-8e21-5b6d9a0c3f47
  namespace: default
  labels:
    openkcm.io/root-key-credentials: "true"
type: kubernetes.io/tls
stringData:
  tls.crt: "<client certificate, PEM>"
  tls.key: "<client private key, PEM>"
  ca.crt: "<CA certificate, PEM>"
```

**What the controller sends:** the method, the tenant ID, the key ARN, the region, the three ARNs, the client
certificate and the client private key from the Secret (how the key reaches Krypton is open, see Common rules). When
cert-manager renews the certificate, the controller sends the new one (see Secret changes).

**Required from Platform Mesh:** the OpenBao PKI with a CA (per tenant or shared, open), cert-manager for kcp workspaces
with an issuer for that PKI, and a way to hand the private key to Krypton without plain text (open). Neither the PKI nor
cert-manager is offered on Platform Mesh today. The customer gives no secret.

**How Krypton authenticates (example):** it signs `CreateSession` with the private key it got from the controller,
sending the certificate and the three ARNs, and Roles Anywhere and STS check it as in variant A.

**Tenant isolation:** the role trusts only certificates with this tenant's subject CN and allows only this
tenant's encryption context. Platform Mesh sets the CN in the `Certificate`; the customer does not choose it, and
the UI never shows the private key.
**Advantage:** no secret from the customer and no long-lived credential: the certificates are short-lived and
renewed automatically. The customer enters only the three ARNs.
**Downside:** depends on the PKI and cert-manager in Platform Mesh, and every renewal must reach Krypton through the
controller. The customer must update the trust anchor before the CA's not-after date, otherwise Krypton loses access
to the key. A trust anchor can hold two certificates, so old and new can overlap.
**Cut-off:** the customer disables the trust anchor or deletes the role.

**Required for this method:**
- The `Certificate` and its Secret live in the customer's workspace, next to the AWSRootKey. The controller needs the
  right to create `certificates` and to read and watch `secrets` there (APIExport permission claims).
- `spec.auth.rolesAnywhere.secretRef` points at that Secret, and the controller follows it to see every renewal. The
  controller writes the reference itself, when it creates the `Certificate`, with a patch that touches only this
  field, so the UI's completion write is not overwritten. It accepts only the expected name and namespace (derived
  from the UID) and sets `CertificateReady: False` with the reason for any other value, so a customer cannot point it
  at another Secret.

**Open (Platform Mesh side), to settle before this variant is built:**
- When cert-manager for kcp workspaces is available, with a reachable issuer for the Platform Mesh PKI.
- Whether the controller may update the spec of the root key (not only its status) through the APIExport. It writes
  `secretRef` there.
- One CA per tenant, or one shared CA with a subject CN per tenant. With a shared CA, tenant isolation rests on the
  CN check in the role alone.

## Quick comparison

For every method the customer also enters the KMS key ARN and region.

| Kind | Variant | Customer enters | Secret from the customer |
|---|---|---|---|
| 1. Static secret | A. Access key | access key ID, secret, role ARN | yes, access key |
| 2. Identity provider | A. Customer's IdP (OIDC) | issuer, audience, client ID, token URL, scope, secret, role | yes |
| | B. Customer's SAML IdP | metadata, ECP address, user name, password, 2 ARNs | yes, user name and password |
| 3. Certificate | A. Customer's certificate | CA, subject CN, certificate, private key, 3 ARNs | yes, private key |
| | B. Platform Mesh PKI | 3 ARNs | no |

| Kind | Variant | Customer renews |
|---|---|---|
| 1. Static secret | A. Access key | the access key |
| 2. Identity provider | A. Customer's IdP (OIDC) | the client secret |
| | B. Customer's SAML IdP | the password; the metadata when the IdP changes its signing certificate |
| 3. Certificate | A. Customer's certificate | the client certificate |
| | B. Platform Mesh PKI | the trust anchor, before the CA expires |

A changed Secret reaches Krypton through the controller (see Secret changes). In variant B cert-manager renews the
client certificate itself.

## Registration, validation and activation (the same for every method)

This starts when the AWSRootKey is complete: every field the method needs is set. For the Platform Mesh PKI the
certificate must also be issued.

1. The controller calls Krypton to register the root key with the method and its values (working name, the API is
   designed by the Krypton team). What it sends is described in each section under "What the controller sends".
2. Krypton answers at once with the key in the pre-activation state. The check continues in the background.
3. The controller polls Krypton every 5 seconds while the key is in pre-activation. The root key shows pending.
4. Krypton gets AWS credentials. This is the only part that depends on the method, see "How Krypton
   authenticates" in each section.
5. Krypton checks the key on KMS: `DescribeKey` (the key must be Enabled, symmetric and customer-managed, and the
   ARN and account must match), then `Encrypt` and `Decrypt` with the tenant's encryption context, then `Encrypt`
   with a foreign context. The last call must be denied: it proves the role allows only the tenant's context.
6. Krypton records the result: passed, or failed with a reason (AWS refused: no retries; AWS unavailable: retry;
   check failed).
7. If the check passed, the controller calls `ActivateKey`. A key cannot go back to pre-activation, so this happens
   only after a passed check. If the check failed, the controller does not activate the key and writes the reason
   into the AWSRootKey status.
8. The controller sets the AWSRootKey status to Ready and the key to Active, and the UI shows it.

From registration on, when a Secret of the AWSRootKey changes, the controller sends the new values to Krypton (see
Common rules, Secret changes).
