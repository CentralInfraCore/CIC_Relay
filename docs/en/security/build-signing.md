# Build Signing Guide

This document describes the build signing process for the Relay project and its integration with RootCA validation.

## 1. Build metadata
During build, the following metadata is injected into the binary via `ldflags`:
```yaml
build_id: relay-<version>-<commit>
commit: <commit_hash>
timestamp: <build_timestamp>
gpg_signature: <build_signature>
ca_fingerprint: <ca_fingerprint>
```

## 2. RootCA signing
- The commit ID (and optionally the artifact hash) is signed by the build system using the RootCA private key.
- The corresponding public key is used for verification.

## 3. Pipeline signing process
1. Validate developer commit signature (`git verify-commit`).
2. Build binary with injected commit ID and RootCA signature.
3. Generate external YAML meta file for verification:
```yaml
build_meta:
  commit: <commit_hash>
  commit_signature: <gpg_signature>
  ca_fingerprint: <ca_fingerprint>
  binary_sha256: <binary_hash>
```

## 4. Verification process
- `-v` flag in binary prints embedded metadata.
- External meta YAML is compared to embedded metadata.
- RootCA public key verifies build signature.

## 5. Security principle
This ensures:
- Authenticity of source commit (developer GPG signature).
- Integrity of build process (RootCA signature).
- Auditability via external meta file matching the binary metadata.
