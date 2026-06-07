# Certificate Validation in CIC

This document describes the advanced trust model used by the CIC system to manage secure, auditable certificate validation, enabling safe Root CA rotation while preserving full integrity.

## Goals

* Ensure each signed object is self-contained and verifiable.
* Allow secure Root CA rotation without breaking existing signatures.
* Preserve long-term auditability of origin and trust level.

## Key Fields

* `Signature`: Signs the object's `Checksum` using the current certificate.
* `Certificate`: The PEM-encoded certificate that signed the object.
* `CertificateSignature`: A separate signature over the `Certificate`, created using a known and trusted older Root CA.

## Validation Process

1. **Certificate Origin Validation**

    * Check `CertificateSignature` using known/trusted historical Root CAs.
    * If valid, accept `Certificate` as a trusted signer for this context.

2. **Object Signature Validation**

    * Use the verified `Certificate` to check the `Signature` over the `Checksum`.
    * If valid, the object is cryptographically sound.

## Rationale

This dual-signature model ensures that:

* The actual certificate is not blindly trusted — its origin is also verified.
* New signers (certificates) can be rolled out while keeping the old Root CA in control.
* Trust decisions are reproducible, decentralized, and explicitly embedded in the object.

## Benefits

* Safe multi-phase Root CA migration.
* Compatibility with strict validation chains.
* Fully declarative trust anchors for distributed systems.

## Implications for Relay/Module Design

* Modules should load their own certificate and RootCA chain at startup.
* On validating an object:

    * First verify the `CertificateSignature` to accept the `Certificate`.
    * Then verify the object’s `Signature` using the now-accepted `Certificate`.
* All validations must be deterministic and offline-verifiable.

## Future Work

* Add optional fields to express trust epoch or revocation reason.
* Consider integrating CRL/OCSP checks if online context is available.
* Investigate compression or chain bundle encoding to reduce object size.
