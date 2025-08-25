# Build Process Guide

This document describes the Relay project build process step by step.

## 1. Preparation
- Download dependencies (`go mod download`).
- Prepare module cache.
- Initialize test environment and required containers.

## 2. Source Code Verification
- Verify commit signatures (`git verify-commit`).
- Code quality checks (lint, staticcheck, ineffassign).
- Run tests (unit and integration tests).

## 3. Compilation
- Build binary with embedded commit hash and build metadata.
- Add RootCA signature to the metadata.

## 4. Artifact Management
- Save binary into directory structure based on version and commit hash.
- Generate external YAML metadata file for verification.

## 5. Notification and Publishing
- Send MQ notification about build result.
- Deliver artifacts to validation and test environments.

## 6. Validation Points
- Check consistency between metadata and binary.
- CA validation (CRT, CRL, optional OCSP).
- Log and archive results for audit purposes.
