# RootCA Validation Guide

This document describes the RootCA validation process for the Relay project. It explains how the CA public key fingerprint is stored, how certificates (CRT) are validated, and how the CRL is used to verify certificate status.

## 1. Storing CA fingerprint
The SHA-256 fingerprint of the CA public key is included in the build metadata:
```yaml
ca_fingerprint: <ca_fingerprint>
```
This ensures that the certificate authority can be precisely identified during validation.

## 2. Certificate (CRT) validation
- The CRT is signed using the RootCA private key.
- Validation is performed using the CA public key.
- The CRT includes the serial number and validity period.

## 3. CRL check
- The RootCA periodically issues a Certificate Revocation List (CRL).
- During validation, the CRT serial number is checked against the CRL.
- If the CRT appears in the list, it is invalid.

## 4. OCSP check (optional)
- The Online Certificate Status Protocol (OCSP) can be used for real-time status queries.
- OCSP returns whether the CRT is "good", "revoked" or "unknown".

## 5. Example CA validation metadata
```yaml
ca_validation:
  ca_fingerprint: <ca_fingerprint>
  crt_status: valid
  crt_serial: <crt_serial_number>
  crl_check: passed
  crl_last_update: <timestamp>
  ocsp_status: good
```
