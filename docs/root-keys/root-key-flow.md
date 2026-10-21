# Root keys: the generic flow

The first diagram applies to every provider and every authentication method. It has two phases: creating the root
key, and linking an L2 domain key to it. It stops at Krypton: the controller calls Krypton and follows up. Call
names marked "working name" are placeholders, the API is designed by the Krypton team.

The authentication methods and their fields are listed per provider:

- [AWS KMS](aws.md)
- [GCP Cloud KMS](gcp.md)
- [Azure Key Vault](azure.md)

The small diagrams at the end show how Krypton authenticates to AWS with each method. They are behind Krypton, so
they are examples only and not part of the contract.

## Generic flow

```mermaid
sequenceDiagram
  autonumber
  actor U as Customer
  participant UI as UI
  participant K as kcp workspace
  participant C as Controller
  participant KR as Krypton

  rect rgba(52, 168, 83, 0.14)
    Note over U,KR: PHASE 1. Create the root key
    U->>UI: picks the provider and method, enters the key reference and other template values
    UI->>K: creates a draft root key (tenant, method, key reference, other template values)
    K->>C: the controller sees the draft

    opt only if the method needs setup data (every AWS method does)
      C->>KR: gRPC prepare setup data (working name)
      KR-->>C: setup data for the customer
      C->>K: writes it into the root key status
    end

    opt only for the Platform Mesh PKI (Platform Mesh generates the certificate, the customer gives none)
      Note over C: creates a cert-manager Certificate in its own cluster.<br/>cert-manager issues it into a Secret there
    end

    K-->>UI: root key status with the setup data
    UI-->>U: setup template for the provider, already filled in
    Note over U: sets up the provider side with the template and reads what it returns
    U->>UI: enters what the method needs (ARNs, or credentials)
    opt only if the method has a credential from the customer
      UI->>K: first creates the Secret with the credentials, labelled with the root key UID
    end
    UI->>K: completes the root key (ARNs and the reference to the Secret)
    K->>C: the controller sees the complete root key

    opt only if the method sends a secret to Krypton (hand-off not agreed yet)
      Note over C,KR: Target: nothing unencrypted is stored at Krypton or travels to it
      Note over C,KR: Plan: a Krypton public key. No such API exists yet, the calls below are placeholders
      C->>KR: gRPC get the public encryption key (working name)
      KR-->>C: public key
      Note over C: reads the Secret and protects it as agreed with the Krypton team
    end

    C->>KR: gRPC register the root key, method and values (working name)
    KR-->>C: key in pre-activation
    Note over KR: Behind Krypton, not part of the contract.<br/>Krypton authenticates and checks the key.

    loop every 5 seconds until the check ends
      C->>KR: gRPC GetKey
      KR-->>C: pre-activation, or failed with a reason, or ready
    end

    alt check passed
      C->>KR: gRPC ActivateKey
      KR-->>C: key Active
      C->>K: sets the root key status to Ready and the key to Active
    else check failed
      C->>K: sets the root key status to failed, with the reason
    end
    K-->>UI: root key status

    opt the Secret changes later (the customer replaces a value, or cert-manager renews the certificate)
      Note over C: sees the changed Secret
      C->>KR: gRPC new credentials for this root key (working name)
    end
  end

  rect rgba(66, 133, 244, 0.14)
    Note over U,KR: PHASE 2. Link an L2 domain key to the root key
    U->>UI: creates an L2 domain key on this root key
    UI->>K: creates the DomainKey with the root key name
    K->>C: the controller sees the DomainKey
    C->>KR: gRPC link the L2 key to the root key by name (working name)
    KR-->>C: key in pre-activation, linking runs in the background
    Note over KR: Behind Krypton, not part of the contract.<br/>The plugin of the root key provider wraps the L2 key.

    loop every 5 seconds until linking ends
      C->>KR: gRPC GetKey
      KR-->>C: pre-activation, or failed with a reason, or ready
    end

    C->>K: sets the DomainKey status to Ready, or failed with the reason
    K-->>UI: DomainKey status
  end
```

## What differs per AWS method

Every method also sends the method, the tenant ID, the KMS key ARN and the region in the register call. The
methods are described in [aws.md](aws.md).

| Kind | Variant | Setup data in the status | Customer enters after the template |
|---|---|---|---|
| 1. Static secret | A. Access key | encryption context | access key ID, secret, role ARN |
| 2. Identity provider | A. Customer's IdP | encryption context | role ARN, token endpoint, client secret |
| | B. Kubernetes service account | cluster issuer, audience, subject, encryption context | role ARN |
| | C. Customer's SAML identity provider (after OIDC) | encryption context | 2 ARNs, user name, password |
| 3. Certificate | A. Customer's certificate | encryption context | 3 ARNs, certificate, private key |
| | B. Platform Mesh PKI | CA certificate, subject CN, encryption context, not-after | 3 ARNs |

The methods that send a secret to Krypton (static secret, identity provider A and C, certificates A and B) use the
hand-off `opt` block of the generic flow. How the secret reaches Krypton is not agreed yet.

For all of them except the Platform Mesh PKI, the credentials stay in a Kubernetes Secret in the customer's
workspace. The UI creates it before it completes the root key, and the AWSRootKey only references it. For the
Platform Mesh PKI, cert-manager creates the Secret in the controller's cluster, and the controller shows the
reference in the status.

## How Krypton authenticates to AWS, per method (examples)

Each diagram starts when Krypton has received the register call and ends when it can use the key.

### 1. Static secret

#### A. Access key

```mermaid
sequenceDiagram
  autonumber
  participant KR as Krypton
  participant STS as AWS STS
  participant KMS as Customer KMS

  KR->>STS: AssumeRole for the role, signed with the secret access key
  Note over STS: gets the access key ID and the signature, never the secret.<br/>Checks the signature and the role trust
  STS-->>KR: temporary credentials
  KR->>KMS: Encrypt and Decrypt with the encryption context, signed with the temporary credentials
  KMS-->>KR: ciphertext, plaintext
```

### 2. Identity provider

#### A. Customer's identity provider

```mermaid
sequenceDiagram
  autonumber
  participant KR as Krypton
  participant IDP as Customer identity provider
  participant STS as AWS STS
  participant KMS as Customer KMS

  KR->>IDP: token request, client credentials grant (client ID, client secret)
  IDP-->>KR: signed token
  KR->>STS: AssumeRoleWithWebIdentity (role ARN, token)
  STS->>IDP: GET discovery document and key set
  IDP-->>STS: public key named by the kid in the token
  Note over STS: verifies the signature with the public key.<br/>Checks exp, aud and the role trust policy
  STS-->>KR: temporary credentials
  KR->>KMS: Encrypt and Decrypt with the encryption context, signed with the temporary credentials
  KMS-->>KR: ciphertext, plaintext
```

#### B. Kubernetes service account

```mermaid
sequenceDiagram
  autonumber
  participant KR as Krypton
  participant ISS as Krypton cluster issuer address
  participant STS as AWS STS
  participant KMS as Customer KMS

  Note over KR: reads the service-account token that Kubernetes mounts into the pod
  KR->>STS: AssumeRoleWithWebIdentity (role ARN, service-account token)
  STS->>ISS: GET discovery document and key set
  ISS-->>STS: public key named by the kid in the token
  Note over STS: verifies the signature with the public key.<br/>Checks exp, aud and the role trust policy
  STS-->>KR: temporary credentials
  KR->>KMS: Encrypt and Decrypt with the encryption context, signed with the temporary credentials
  KMS-->>KR: ciphertext, plaintext
```

#### C. Customer's SAML identity provider

```mermaid
sequenceDiagram
  autonumber
  participant KR as Krypton
  participant IDP as Customer identity provider
  participant STS as AWS STS
  participant KMS as Customer KMS

  KR->>IDP: ECP login (user name, password)
  IDP-->>KR: signed SAML response
  KR->>STS: AssumeRoleWithSAML (role ARN, SAML provider ARN, response)
  Note over STS: checks the response against the uploaded metadata
  STS-->>KR: temporary credentials
  KR->>KMS: Encrypt and Decrypt with the encryption context, signed with the temporary credentials
  KMS-->>KR: ciphertext, plaintext
```

### 3. Certificate

#### A. Customer's certificate

```mermaid
sequenceDiagram
  autonumber
  participant KR as Krypton
  participant RA as AWS Roles Anywhere and STS
  participant KMS as Customer KMS

  Note over KR: uses the customer's private key
  KR->>RA: CreateSession signed with the customer's key, with the certificate and the three ARNs
  Note over RA: checks the signature and the certificate against the trust anchor.<br/>Checks the profile and the role
  RA-->>KR: temporary credentials
  KR->>KMS: Encrypt and Decrypt with the encryption context, signed with the temporary credentials
  KMS-->>KR: ciphertext, plaintext
```

#### B. Platform Mesh PKI

```mermaid
sequenceDiagram
  autonumber
  participant KR as Krypton
  participant RA as AWS Roles Anywhere and STS
  participant KMS as Customer KMS

  Note over KR: uses the certificate and key from the controller.<br/>cert-manager issued them
  KR->>RA: CreateSession signed with the certificate key, with the certificate and the three ARNs
  Note over RA: checks the signature and the certificate against the trust anchor.<br/>Checks the profile and the role
  RA-->>KR: temporary credentials
  KR->>KMS: Encrypt and Decrypt with the encryption context, signed with the temporary credentials
  KMS-->>KR: ciphertext, plaintext
```
