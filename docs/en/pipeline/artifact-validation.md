# Artifact Validation Guide

This document describes the validation process for build artifacts in the Relay project.

## 1. Artifact Types
- Binaries (e.g., `cic-relay`, `crt_parser`).
- Metadata files (YAML format).
- Certificates (CRT) and CA fingerprints.

## 2. Validation Goals
- Consistency between binaries and metadata.
- Identification and authenticity of the source commit.
- Validity of the RootCA signature.

## 3. Validation Steps
1. **Metadata Verification**
    - Compare binary metadata (`-v` output) with the external YAML file.
2. **Hash Verification**
    - Compute the binary SHA-256 hash and compare it to the value in the metadata file.
3. **Signature Verification**
    - Verify the commit signature with the developer key.
    - Verify the RootCA signature with the CA public key.
4. **Certificate Verification**
    - Check CRT status (CRL, OCSP).

## 4. Validation Output
- **Successful validation** → artifact is authenticated.
- **Failed validation** → log reason for failure (commit, metadata, or signature mismatch).

## 5. Audit and Archival
- Log validation results.
- Archive metadata and validation logs for audit purposes.
