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
| | | [B. Kubernetes service account](#b-kubernetes-service-account) |
| | SAML | [C. Customer's SAML identity provider](#c-customers-saml-identity-provider) (after OIDC) |
| [3. Certificate](#3-certificate) | Roles Anywhere | [A. Customer's certificate](#a-customers-certificate) |
| | | [B. Platform Mesh PKI](#b-platform-mesh-pki) |

Static secret: the customer hands over an access key (ID and secret). Identity provider: Krypton shows a token
from an identity provider. Certificate: Krypton shows a certificate.

Every method is documented here, but not every method is implemented on day one: the implementation takes them one
by one by priority. The methods that send a secret to Krypton (the access key, identity providers A and C, and
certificates A and B) also wait for the secret hand-off to Krypton (see Common rules). Identity provider B needs a
public cluster issuer, and certificate B needs cert-manager with the Platform Mesh PKI.

OpenKCM runs nothing in AWS and needs no AWS account of its own. Every AWS resource below is created in the
customer's account by a template the customer deploys. Krypton only calls the public AWS endpoints of the
customer's region.

The parts called "How Krypton authenticates" and steps 4-6 of the last section show what happens behind Krypton.
They explain each method and are examples only: that part is designed by the Krypton team.

## Common rules

- **Before any method:** OpenKCM is enabled and the tenant exists (step 0). The customer has a symmetric KMS key in
  their AWS account and knows its ARN and region.
- **Tenant ID:** the ID Krypton returns from `CreateTenant`, stored in the Tenant annotation (step 0).
- **Encryption context:** `openkcm:tenant=<tenant ID>`. It is not secret. For every method the
  template allows `kms:Encrypt` and `kms:Decrypt` only with this context, and Krypton sends it on every KMS call.
  The registration check depends on it (see the last section).
- **Setup data:** for every method, when the draft AWSRootKey appears, the controller asks Krypton for the setup
  data and writes it into the AWSRootKey status (`status.setupData`). The UI fills the template only from
  the status and from what the customer typed in the form. What the setup data contains depends on the method.
- **Template values first:** the role in every template names the customer's key, so the UI asks for the KMS key
  ARN and region, and any other value the template needs, before it shows the template.
- **Secrets:** all credentials of a method go into one Kubernetes Secret: the secret value and the identifiers that
  belong to it (access key ID, client ID, user name, certificates). The UI creates it in the customer's kcp workspace
  and labels it with the AWSRootKey's UID. The AWSRootKey only references it (`secretRef` with `name` and
  `namespace`). No secret value is ever written into an AWSRootKey, its status, a log or an event. The entry names
  are fixed per method and listed in each section.
- **Secret hand-off to Krypton (open):** the target is that nothing unencrypted is stored at Krypton or travels to it.
  The plan is that the controller encrypts the Secret with a public key taken from Krypton; that endpoint does not
  exist yet and is not agreed with the Krypton team. For now the value stays in the Kubernetes Secret as is, and how
  it reaches Krypton is open.
- **Secret changes:** the controller watches the labelled Secrets. When one changes (the customer replaces a value,
  or cert-manager renews a certificate), it tells Krypton that the root key has new credentials (working name, the
  API is designed by the Krypton team).
- **After the key is Active** the KMS key ARN, region and method do not change: a different key is a different
  root key.
- **CRD change:** the `auth` block and `status.setupData` are new in the AWSRootKey CRD, so that one kind holds
  several methods. Today the CRD has `spec.rolesAnywhere` with three required ARNs, so a draft without ARNs is
  rejected. In the new CRD that block moves to `spec.auth.rolesAnywhere`; `tenantNameRef`, `region`, `keyUri`,
  `endpointUrl` and `lifecycle` stay as they are.
- **About the examples:** each YAML shows the AWSRootKey after the customer has completed it. The kind is
  namespaced; the UI creates it in the customer's workspace, `default` here. Names, ARNs, the account number and
  addresses are illustrative. `endpointUrl` is optional and `lifecycle` defaults to Active, so neither is shown.
  Fields under `auth` and `status.setupData` are new; `tenantNameRef`, `region`, `keyUri` and the three
  Roles Anywhere ARNs exist in the CRD today. Secret values are written as placeholders.

## What is required from Platform Mesh

| Building block | Needed by | Offered today |
|---|---|---|
| Secret in the customer's kcp workspace | access key, customer's IdPs, customer's certificate | yes |
| Controller may read and watch those Secrets (APIExport claim on `secrets`) | the same | no |
| Hand-off of the credentials to Krypton, not in plain text | the same, and the Platform Mesh PKI | no |
| Public service-account issuer of the Krypton cluster | Kubernetes service account | not confirmed (Gardener can) |
| OpenBao PKI with a CA, and cert-manager with an issuer for it in the controller's cluster | Platform Mesh PKI | no |

The hand-off is described in Common rules.

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

Not typed by the customer: `spec.tenantNameRef` and `spec.auth.method: AccessKey` (set by the UI with the draft),
and `spec.auth.accessKey.secretRef` (the UI sets it when it creates the Secret from values 3 and 4). The access
key ID is not secret but sits in the Secret with the secret access key: a rotation replaces both at once, so one
write to the Secret keeps them in step.

**What the customer does:**
1. In the UI, picks AWS KMS with access keys and enters the KMS key ARN and region. The UI creates a draft
   AWSRootKey with them.
2. The controller asks Krypton for the setup data and writes it into the status. For this method it is only the
   encryption context.
3. The UI gives a CloudFormation template already filled in with the key ARN and the encryption context. The
   customer types nothing into it.
4. The customer deploys it in their AWS account.
5. The stack returns the role ARN and the user name.
6. The customer creates an access key for that user: `aws iam create-access-key --user-name <user name>`. AWS
   prints the access key ID and the secret once.
7. The customer enters the access key ID, the secret and the role ARN in the UI. The UI first creates the Secret
   with the access key ID and the secret, then saves the role ARN and `secretRef` to the AWSRootKey. That completes
   the key and starts registration.

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
spec:
  tenantNameRef: acme
  region: eu-central-1
  keyUri: arn:aws:kms:eu-central-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab
  auth:
    method: AccessKey
    accessKey:
      roleArn: arn:aws:iam::123456789012:role/openkcm-l1-access
      secretRef:
        name: acme-aws-access-key
        namespace: default
status:
  setupData:
    encryptionContext: openkcm:tenant=3f2a9c1e-7b4d-4e8a-9c61-2d5f0a7b8e13
```

The Secret the UI creates and the AWSRootKey points at:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: acme-aws-access-key
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

**Note:** OpenKCM does not provide an identity provider. The customer brings their own (A and C). If Krypton runs
in Kubernetes, the service-account issuer of that cluster can be used instead (B); it is an option, not a rule.

**OIDC (variants A and B). What it is:** the customer tells AWS "trust identity tokens (JWT) signed by this
issuer". Krypton shows such a token to AWS STS (`AssumeRoleWithWebIdentity`) and gets temporary credentials for a
role. AWS downloads the issuer's public keys from the issuer's public address to check the signature. The two
variants differ in who the issuer is.

**What every OIDC variant's template creates** in the customer's AWS account:
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
| 3 | Issuer address | the customer's identity provider | no | `spec.auth.webIdentity.issuerUrl` | 2 |
| 4 | Client ID | the application registered for OpenKCM | no | Secret entry `clientId` | 2 |
| 5 | Audience | set by the customer in the application | no | `spec.auth.webIdentity.audience` | 2 |
| 6 | Role ARN | output of the template's stack | no | `spec.auth.webIdentity.roleArn` | 5 |
| 7 | Token endpoint | the customer's identity provider | no | `spec.auth.webIdentity.tokenEndpoint` | 5 |
| 8 | Client secret | the application registered for OpenKCM | yes | Secret entry `clientSecret` | 5 |

Not typed by the customer: `spec.tenantNameRef`, `spec.auth.method: WebIdentity` and
`spec.auth.webIdentity.issuer: Customer` (set by the UI with the draft), and `spec.auth.webIdentity.secretRef`
(set when the UI creates the Secret from values 4 and 8). The client ID is not secret but sits in the Secret with
the client secret, so the credentials are in one place and a rotation is one write. The template needs the client
ID in step 4, so the customer enters it in step 2 and the UI keeps it in the form until it creates the Secret in
step 5; the draft AWSRootKey does not hold it.

**What the customer does:**
1. In their identity provider, registers an application for OpenKCM that may get tokens with the client
   credentials grant, and notes the issuer address, the token endpoint, the client ID, the client secret and the
   audience.
2. In the UI, picks AWS KMS with OIDC (own identity provider) and enters the KMS key ARN, region, issuer address,
   client ID and audience. Draft created with all of them except the client ID.
3. The controller asks Krypton for the setup data: the encryption context. It writes it into the status.
4. The UI gives the template filled in with the key ARN, the issuer address, the client ID as the subject, the
   audience and the encryption context.
5. The customer deploys it and enters in the UI the role ARN, the token endpoint and the client secret. The UI
   first creates the Secret with the client ID and the client secret, then saves the role ARN, the token endpoint
   and `secretRef` to the AWSRootKey. That completes the key and starts registration.

**To verify per identity provider:** the template uses the client ID as the subject. The `sub` claim of a
client-credentials token is not always the client ID (some providers put another identifier in `sub` and the client
ID in `azp`), and AWS may use `azp` instead of `aud` when it is present. The template needs the value that the
provider really puts into the token.

**Where the values are kept:**
- Client ID and client secret: from the application the customer registered in their identity provider. Kept in a
  Kubernetes Secret in the kcp workspace, created by the UI in step 5. The AWSRootKey holds only the reference.
- Issuer address, token endpoint, audience: from the customer's identity provider, kept in the spec.
- Role ARN: returned by the stack, kept in the spec.

**Example:**

```yaml
apiVersion: operations.openkcm.io/v1alpha1
kind: AWSRootKey
metadata:
  name: acme-aws-root
  namespace: default
spec:
  tenantNameRef: acme
  region: eu-central-1
  keyUri: arn:aws:kms:eu-central-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab
  auth:
    method: WebIdentity
    webIdentity:
      issuer: Customer
      issuerUrl: https://idp.example.com/acme
      tokenEndpoint: https://idp.example.com/acme/oauth2/token
      audience: sts.amazonaws.com
      roleArn: arn:aws:iam::123456789012:role/openkcm-l1-access
      secretRef:
        name: acme-idp-client-secret
        namespace: default
status:
  setupData:
    encryptionContext: openkcm:tenant=3f2a9c1e-7b4d-4e8a-9c61-2d5f0a7b8e13
```

The Secret the UI creates and the AWSRootKey points at:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: acme-idp-client-secret
  namespace: default
type: Opaque
stringData:
  clientId: "<client ID>"
  clientSecret: "<client secret>"
```

**What the controller sends:** the method, the tenant ID, the key ARN, the region, the role ARN, the token
endpoint, the client ID, the audience and the client secret (how the secret reaches Krypton is open, see Common
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

### B. Kubernetes service account

**Who the issuer is:** the Kubernetes cluster where Krypton runs. Kubernetes signs a short-lived token for
Krypton's service account (a projected service-account token with the audience `sts.amazonaws.com`). The cluster
serves its public keys at its service-account issuer address. This variant only applies if Krypton runs in
Kubernetes, and it is an option for whoever deploys it that way, not a rule.

**What the customer provides:**

| # | Value | Where it comes from | Secret | Goes into | Step |
|---|---|---|---|---|---|
| 1 | KMS key ARN | the customer's KMS key | no | `spec.keyUri` | 1 |
| 2 | Region of the key | the customer's KMS key | no | `spec.region` | 1 |
| 3 | Role ARN | output of the template's stack | no | `spec.auth.webIdentity.roleArn` | 4 |

Not typed by the customer: `spec.tenantNameRef`, `spec.auth.method: WebIdentity` and
`spec.auth.webIdentity.issuer: ServiceAccount` (set by the UI with the draft). There is no secret and no
`secretRef`. The cluster issuer, audience and subject come from Krypton as setup data and go into the template.

**What the customer does:**
1. In the UI, picks AWS KMS with OIDC (service account) and enters the KMS key ARN and region. Draft created.
2. The controller asks Krypton for the setup data: the cluster's issuer address, the audience, the subject
   (`system:serviceaccount:<namespace>:<name>` of Krypton) and the encryption context. It writes them into the
   status.
3. The UI gives the template filled in.
4. The customer deploys it and enters the role ARN.

**Where the values are kept:** nothing secret. Setup data in the status; role ARN, KMS key ARN and region in the
spec.

**Example:**

```yaml
apiVersion: operations.openkcm.io/v1alpha1
kind: AWSRootKey
metadata:
  name: acme-aws-root
  namespace: default
spec:
  tenantNameRef: acme
  region: eu-central-1
  keyUri: arn:aws:kms:eu-central-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab
  auth:
    method: WebIdentity
    webIdentity:
      issuer: ServiceAccount
      roleArn: arn:aws:iam::123456789012:role/openkcm-l1-access
status:
  setupData:
    encryptionContext: openkcm:tenant=3f2a9c1e-7b4d-4e8a-9c61-2d5f0a7b8e13
    issuerUrl: https://oidc.krypton-cluster.example.com
    audience: sts.amazonaws.com
    subject: system:serviceaccount:krypton:krypton
```

**What the controller sends:** the method, the tenant ID, the key ARN, the region and the role ARN.

**Required from Platform Mesh:** the cluster where Krypton runs must publish its service-account issuer at a public
address (Gardener can do this through an extension). No secret.

**How Krypton authenticates (example):** it reads its service-account token from the file Kubernetes mounts into
the pod and sends it to STS. STS downloads the cluster's public keys from the issuer address and checks the token.

**Tenant isolation:** weaker than the other variants. The subject is Krypton's service account, the same for every
tenant, so the subject check does not separate tenants. Only the encryption context does: a role that allows only
tenant A's context cannot be used for tenant B, as long as Krypton always sends the requesting tenant's context.
**Advantage:** no secret from the customer at all. Kubernetes issues and rotates the short-lived token, and the
customer enters only the role ARN.
**Downside:** every customer trusts the service-account issuer of the cluster where Krypton runs. Krypton runs in
several clusters and regions, so each cluster is a different issuer. Moving Krypton to another cluster breaks
every customer's trust.
**Cut-off:** the customer deletes the role or the OIDC provider.

### C. Customer's SAML identity provider

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

Not typed by the customer: `spec.tenantNameRef`, `spec.auth.method: SAML` and `spec.auth.saml.secretRef` (set when
the UI creates the Secret from values 5 and 8). The user name is not secret but sits in the Secret with the
password: they are one credential, and a rotation is one write.

**What the customer does:**
1. In their identity provider, creates an application for AWS with ECP allowed, and a service user for OpenKCM with
   a password and without MFA. Downloads the metadata.
2. In the UI, picks AWS KMS with SAML and enters values 1 to 4. Draft created.
3. The controller asks Krypton for the setup data: the encryption context. It writes it into the status.
4. The customer deploys the filled-in template. The stack returns the SAML provider ARN and the role ARN.
5. In the AWS application of their identity provider, the customer gives the service user that role (both ARNs).
6. The customer enters the two ARNs, the user name and the password in the UI. The UI first creates the Secret with
   the user name and the password, then saves the two ARNs and `secretRef` to the AWSRootKey. That completes the
   key and starts registration.

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
        name: acme-saml-password
        namespace: default
status:
  setupData:
    encryptionContext: openkcm:tenant=3f2a9c1e-7b4d-4e8a-9c61-2d5f0a7b8e13
```

The Secret the UI creates and the AWSRootKey points at:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: acme-saml-password
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
| 3 | CA certificate (PEM) | the customer's PKI | no | Secret entry `ca.crt` | 2 |
| 4 | Subject CN of the client certificate | the customer's PKI | no | `spec.auth.rolesAnywhere.subjectCn` | 2 |
| 5 | Trust anchor ARN | output of the template's stack | no | `spec.auth.rolesAnywhere.trustAnchorArn` | 5 |
| 6 | Profile ARN | output of the template's stack | no | `spec.auth.rolesAnywhere.profileArn` | 5 |
| 7 | Role ARN | output of the template's stack | no | `spec.auth.rolesAnywhere.roleArn` | 5 |
| 8 | Client certificate (PEM) | the customer's PKI | no | Secret entry `tls.crt` | 5 |
| 9 | Client private key (PEM) | the customer's PKI | yes | Secret entry `tls.key` | 5 |

Not typed by the customer: `spec.tenantNameRef`, `spec.auth.method: RolesAnywhere` and
`spec.auth.rolesAnywhere.certificateSource: Customer` (set by the UI with the draft), and
`spec.auth.rolesAnywhere.secretRef` (set when the UI creates the Secret from values 3, 8 and 9). Values 5 to 7 are
saved together or not at all. The three certificate values sit in one Secret of type `kubernetes.io/tls`, the
standard Kubernetes format for a certificate with its key; the CA certificate is its `ca.crt` entry. Only the key is
secret. The template needs the CA certificate in step 4, so the customer gives it in step 2 and the UI keeps it in
the form until it creates the Secret in step 5; the draft AWSRootKey does not hold it.

**What the customer does:**
1. In their own PKI, issues a client certificate for OpenKCM and keeps the CA certificate, the client certificate
   and the client private key.
2. In the UI, picks AWS KMS with Roles Anywhere (own certificate) and enters the KMS key ARN, region, the CA
   certificate and the subject CN of the client certificate. Draft created with all of them except the CA
   certificate.
3. The controller asks Krypton for the setup data: the encryption context. It writes it into the status.
4. The UI gives the template filled in with the key ARN, the CA certificate, the subject CN and the encryption
   context.
5. The customer deploys it and enters in the UI the three ARNs, the client certificate and the client private key.
   The UI first creates the Secret with the client certificate, the private key and the CA certificate, then saves
   the three ARNs and `secretRef` to the AWSRootKey. That completes the key and starts registration.

**Where the values are kept:**
- Client certificate, client private key and CA certificate: from the customer's PKI. Kept in one Kubernetes Secret
  of type `kubernetes.io/tls` in the kcp workspace, created by the UI in step 5. The AWSRootKey holds only the
  reference.
- Subject CN: from the customer's PKI, public, kept in the spec.
- Three ARNs: returned by the stack, kept in the spec.

**Example:**

```yaml
apiVersion: operations.openkcm.io/v1alpha1
kind: AWSRootKey
metadata:
  name: acme-aws-root
  namespace: default
spec:
  tenantNameRef: acme
  region: eu-central-1
  keyUri: arn:aws:kms:eu-central-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab
  auth:
    method: RolesAnywhere
    rolesAnywhere:
      certificateSource: Customer
      subjectCn: openkcm-acme
      trustAnchorArn: arn:aws:rolesanywhere:eu-central-1:123456789012:trust-anchor/00000000-0000-0000-0000-000000000001
      profileArn: arn:aws:rolesanywhere:eu-central-1:123456789012:profile/00000000-0000-0000-0000-000000000002
      roleArn: arn:aws:iam::123456789012:role/openkcm-l1-access
      secretRef:
        name: acme-client-cert-key
        namespace: default
status:
  setupData:
    encryptionContext: openkcm:tenant=3f2a9c1e-7b4d-4e8a-9c61-2d5f0a7b8e13
```

The Secret the UI creates and the AWSRootKey points at:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: acme-client-cert-key
  namespace: default
type: kubernetes.io/tls
stringData:
  tls.crt: "<client certificate, PEM>"
  tls.key: "<client private key, PEM>"
  ca.crt: "<CA certificate, PEM>"
```

**What the controller sends:** the method, the tenant ID, the key ARN, the region, the three ARNs, the client
certificate and the client private key (how the key reaches Krypton is open, see Common rules). The CA certificate
stays in the Secret: only the template needs it.

**Required from Platform Mesh:** a Kubernetes Secret in the customer's workspace for the client certificate, the
private key and the CA certificate, and a way to hand the key to Krypton without plain text (open). No PKI on our
side.

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
of type `kubernetes.io/tls`, renewing it before it expires. The `Certificate` and the Secret live in the cluster where
the controller runs (a normal Kubernetes cluster, where cert-manager can run; a kcp workspace has no workloads), in a
namespace owned by the platform. The customer never sees or touches them. The AWSRootKey does not point at the
Secret: the controller derives its location from the AWSRootKey and shows it in the status. Krypton cannot fetch a
certificate from Platform Mesh itself, so the controller reads the Secret and sends the certificate to Krypton. The
customer puts the CA's public certificate into AWS.

The placement of the `Certificate` and the Secret, and the missing `secretRef`, are required for this method and
are to be confirmed with the Platform Mesh team (see the end of this section).

**What the customer provides:**

| # | Value | Where it comes from | Secret | Goes into | Step |
|---|---|---|---|---|---|
| 1 | KMS key ARN | the customer's KMS key | no | `spec.keyUri` | 1 |
| 2 | Region of the key | the customer's KMS key | no | `spec.region` | 1 |
| 3 | Trust anchor ARN | output of the template's stack | no | `spec.auth.rolesAnywhere.trustAnchorArn` | 5 |
| 4 | Profile ARN | output of the template's stack | no | `spec.auth.rolesAnywhere.profileArn` | 5 |
| 5 | Role ARN | output of the template's stack | no | `spec.auth.rolesAnywhere.roleArn` | 5 |

Not typed by the customer: `spec.tenantNameRef`, `spec.auth.method: RolesAnywhere` and
`spec.auth.rolesAnywhere.certificateSource: PlatformMesh` (set by the UI with the draft). The customer gives no
secret, and there is no `secretRef` in the spec: the controller derives where the Secret is and shows it in the
status (field name to be agreed), so the customer cannot point the controller at another Secret. The CA
certificate, expected subject CN and not-after date go into the template as setup data. Values 3 to 5 are saved
together or not at all.

**What the customer does:**
1. In the UI, picks AWS KMS with Roles Anywhere (Platform Mesh PKI) and enters the KMS key ARN and region. Draft
   created.
2. The controller creates the cert-manager `Certificate` for this root key (see the example below) and asks Krypton
   for the setup data. Repeating the call returns the same result. Meanwhile the platform issues the certificate,
   with no customer action and no call from the controller:
   - cert-manager picks up the `Certificate` and asks the issuer named in `issuerRef` for a client certificate with
     the subject CN from the `Certificate`.
   - The issuer is backed by the Platform Mesh PKI (OpenBao), which signs with its CA and returns the certificate and
     the CA certificate. cert-manager's Vault issuer type is the likely fit, because OpenBao keeps Vault's API. How
     cert-manager logs in to OpenBao is the platform's concern: the controller never talks to OpenBao and holds no
     address or credential for it.
   - cert-manager writes the Secret (`tls.crt`, `tls.key`, `ca.crt`) with the label of the AWSRootKey's UID, and
     renews it before it expires (8 hours before, in the example below).
   - The controller finds the Secret by that label and sets `CertificateReady: True`. While the certificate is not
     issued, the status shows `CertificateReady: False` with the reason, such as the Platform Mesh PKI being
     unreachable (the reason can be taken from the `Certificate`'s own condition).
3. The setup data has four values: the encryption context, the CA certificate (PEM), the expected subject CN and the
   not-after date of the CA. The controller writes them into the status. Where the last three come from (Krypton or
   the generated Secret) is open, see below.
4. The UI gives the template filled in with the key ARN and the four values. The customer types nothing into it.
5. The customer deploys it and enters the three ARNs.
6. The three ARNs complete the key. When the certificate is issued, the controller registers the root key (see the
   last section).

**Where the values are kept:** the customer provides nothing secret. The certificate and key are in the Secret that
cert-manager creates in the controller's cluster; CA data and the Secret reference in the status; key ARN, region and
three ARNs in the spec.

**Example:**

```yaml
apiVersion: operations.openkcm.io/v1alpha1
kind: AWSRootKey
metadata:
  name: acme-aws-root
  namespace: default
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
status:
  conditions:
  - type: CertificateReady
    status: "True"
  clientCertificate:
    secretRef:
      name: awsrootkey-7c1d2e3f-4a5b-4c6d-8e9f-0a1b2c3d4e5f
      namespace: openkcm-platform
  setupData:
    encryptionContext: openkcm:tenant=3f2a9c1e-7b4d-4e8a-9c61-2d5f0a7b8e13
    caCertificatePem: |
      -----BEGIN CERTIFICATE-----
      MIIB...
      -----END CERTIFICATE-----
    subjectCn: openkcm-3f2a9c1e
    notAfter: "2027-10-14T00:00:00Z"
```

The `Certificate` the controller creates for this root key, in the cluster where it runs:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: awsrootkey-7c1d2e3f-4a5b-4c6d-8e9f-0a1b2c3d4e5f
  namespace: openkcm-platform
spec:
  secretName: awsrootkey-7c1d2e3f-4a5b-4c6d-8e9f-0a1b2c3d4e5f
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
      openkcm.io/awsrootkey-uid: 7c1d2e3f-4a5b-4c6d-8e9f-0a1b2c3d4e5f
```

- The names derive from the AWSRootKey's UID, not its name: two workspaces can both have an `acme-aws-root`.
- `commonName` is the subject CN that the customer's role pins. The customer does not choose it.
- `usages` lists `digital signature` next to `client auth` because Roles Anywhere expects the digital-signature key
  usage on the client certificate, and cert-manager's list replaces its defaults.
- `ca.crt` in the Secret and `caCertificatePem` in `status.setupData` are the same CA certificate. The status copy
  lets the UI fill the template without reading the Secret that holds the private key.
- `secretTemplate` puts the label on the Secret. The controller watches Secrets by this label, not every Secret.
- The issuer is one per tenant if the CA is per tenant (open, see below).
- Names, namespace, label key and lifetimes are illustrative; the platform chooses them.
- When the AWSRootKey is deleted, a finalizer on it deletes the `Certificate` and the Secret first. An
  ownerReference cannot span the kcp workspace and the controller's cluster.

The Secret cert-manager creates from it:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: awsrootkey-7c1d2e3f-4a5b-4c6d-8e9f-0a1b2c3d4e5f
  namespace: openkcm-platform
  labels:
    openkcm.io/awsrootkey-uid: 7c1d2e3f-4a5b-4c6d-8e9f-0a1b2c3d4e5f
type: kubernetes.io/tls
stringData:
  tls.crt: "<client certificate, PEM>"
  tls.key: "<client private key, PEM>"
  ca.crt: "<CA certificate, PEM>"
```

**What the controller sends:** the method, the tenant ID, the key ARN, the region, the three ARNs, the client
certificate and the client private key from the Secret (how the key reaches Krypton is open, see Common rules). When
cert-manager renews the certificate, the controller sends the new one (see Secret changes).

**Required from Platform Mesh:** the OpenBao PKI with a CA (per tenant or shared, open), cert-manager with an issuer
for that PKI in the cluster where the controller runs, and a way to hand the private key to Krypton without plain
text (open). Neither the PKI nor cert-manager is offered on Platform Mesh today. The customer gives no secret.

**How Krypton authenticates (example):** it signs `CreateSession` with the private key it got from the controller,
sending the certificate and the three ARNs, and Roles Anywhere and STS check it as in variant A.

**Tenant isolation:** the role trusts only certificates with this tenant's subject CN and allows only this
tenant's encryption context. Platform Mesh sets the CN in the `Certificate`; the customer cannot choose it and
cannot reach the `Certificate` or its Secret.
**Advantage:** no secret from the customer and no long-lived credential: the certificates are short-lived and
renewed automatically. The customer enters only the three ARNs.
**Downside:** depends on the PKI and cert-manager in Platform Mesh, and every renewal must reach Krypton through the
controller. The customer must update the trust anchor before the CA's not-after date, otherwise Krypton loses access
to the key. A trust anchor can hold two certificates, so old and new can overlap.
**Cut-off:** the customer disables the trust anchor or deletes the role.

**Required for this method (to confirm with the Platform Mesh team):**
- The `Certificate` and its Secret live in the cluster where the controller runs, not in the customer's workspace, so
  the customer never holds Krypton's private key. The controller then needs a client for its own cluster and the
  right to manage `certificates` and `secrets` there.
- No `secretRef` in the spec. The controller derives the Secret's name from the AWSRootKey's UID and shows the
  reference in the status. Otherwise a customer-editable pointer into the platform's cluster would let a customer
  make the controller read another Secret.

**Open (Platform Mesh side), to settle before this variant is built:**
- Whether the cluster where the controller runs has cert-manager with a reachable issuer for the Platform Mesh PKI.
- Where the CA certificate, the subject CN and the not-after date come from: Krypton, or the generated Secret
  (`ca.crt`, and the CN the controller put into the `Certificate`).
- One CA per tenant, or one shared CA with a subject CN per tenant. With a shared CA, tenant isolation rests on the
  CN check in the role alone.

## Quick comparison

For every method the customer also enters the KMS key ARN and region.

| Kind | Variant | Customer enters | Secret from the customer |
|---|---|---|---|
| 1. Static secret | A. Access key | access key ID, secret, role ARN | yes, access key |
| 2. Identity provider | A. Customer's IdP (OIDC) | issuer, audience, client ID, token URL, secret, role ARN | yes |
| | B. Kubernetes service account (OIDC) | role ARN | no |
| | C. Customer's SAML IdP | metadata, ECP address, user name, password, 2 ARNs | yes, user name and password |
| 3. Certificate | A. Customer's certificate | CA, subject CN, certificate, private key, 3 ARNs | yes, private key |
| | B. Platform Mesh PKI | 3 ARNs | no |

| Kind | Variant | Customer renews |
|---|---|---|
| 1. Static secret | A. Access key | the access key |
| 2. Identity provider | A. Customer's IdP (OIDC) | the client secret |
| | B. Kubernetes service account (OIDC) | nothing |
| | C. Customer's SAML IdP | the password; the metadata when the IdP changes its signing certificate |
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
