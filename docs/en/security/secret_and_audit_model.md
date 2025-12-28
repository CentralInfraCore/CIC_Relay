# CIC – Secret & QA Evidence Management
## Specification Draft

### 1. Goal and Scope
System goals:
*   Recording state-based QA evidences in Git.
*   Protecting sensitive data using Vault.
*   Ensuring a reproducible and authentic (signed) audit trail.

Non-goals:
*   Storing raw logs or plaintext sensitive data in Git.

### 2. Core Principles
*   **Git ≠ secret store**
*   **Vault ≠ log store**
*   **CIC ≠ data owner**
*   **Defense-in-depth:** crypto + policy + process separation.
*   **Plaintext:** only for minimal duration, only where strictly necessary.

### 3. Logging Levels (Normative)

#### 3.1 none
*   Nothing is stored or synchronized.

#### 3.2 basic (default)
*   Stored in Git:
    *   expected / actual state snapshot,
    *   diff summary,
    *   meta (run-id, versions, timestamp).
*   No raw I/O.
*   Daily branch push is allowed.

#### 3.3 high
*   Stored in Git:
    *   content of basic level,
    *   I/O hash/fingerprint/aggregate (no payload).
*   Encrypted data is optional, plaintext is forbidden.
*   Daily branch push is allowed.

#### 3.4 raw (break-glass)
*   Not part of the normal operation.
*   Local only or separate, restricted remote.
*   Can be stored in Git:
    *   encrypted payload (Vault-managed).
*   Plaintext in Git is forbidden.
*   Short retention.

### 4. Git Structure and Policy

#### 4.1 Branches
*   `cache/date/*` – local, noisy, not audit.
*   `daily` – audit/QA branch, squashed daily state.

#### 4.2 Rules
*   `daily`:
    *   protected branch,
    *   no force-push,
    *   only signed commits are accepted.
*   `cache/*`:
    *   push is not mandatory,
    *   if pushed: only to debug remote.

### 5. Commit Authentication
*   Every audit-relevant commit: cryptographically signed (Vault Transit).
*   Signature contains:
    *   commit digest / manifest hash,
    *   key version,
    *   pipeline/run-id.
*   Dev hook = best effort.
*   Release/QA = relay-generated commit (normative).

### 6. Secret Management Model (Vault)

#### 6.1 Objects
*   host obj
*   module obj
*   relay obj

#### 6.2 Tokens
*   Every obj:
    *   own Vault token,
    *   own path + policy (no overlap).
*   Tokens:
    *   short TTL,
    *   minimal capability,
    *   inaccessible to each other.

#### 6.3 Unwrapping
*   Layered unwrap:
    ```
    relay.decrypt(
      module.decrypt(
        host.decrypt(data)
      )
    )
    ```
*   Plaintext is generated only if business justified.

### 7. Runtime Separation
*   **3 separate processes:**
    *   host-decrypt
    *   module-decrypt
    *   relay-decrypt
*   **1 coordinator:**
    *   sequence, timeout, retry,
    *   no access to token or plaintext.
*   **IPC:**
    *   wrapped data only,
    *   size limit,
    *   no payload logging.

### 8. Kubernetes / Docker Mode

#### 8.1 Kubernetes
*   At startup: one-time bootstrap token, only for secret seeding.
*   Bootstrap token:
    *   narrow policy,
    *   short TTL,
    *   cannot be used for decrypt/sign operations.

#### 8.2 Docker / Service
*   Local in-memory Vault instance.
*   Seeded from central Vault.
*   Plaintext is not persisted.

### 9. Prohibitions (Hard Rules)
*   Plaintext sensitive data in Git → **FORBIDDEN**
*   Cache branch to audit remote → **FORBIDDEN**
*   Wildcard Vault policy → **FORBIDDEN**
*   Token sharing between objs → **FORBIDDEN**

### 10. Audit and Observability
*   **Vault side:** every decrypt/sign event is audited.
*   **CIC side:** who, when, which run-id created the audit commit.
*   Break-glass events are logged separately.

### 11. Threat Model Baseline
*   Compromise of one component ≠ compromise of the entire system.
*   Obtaining plaintext requires cooperation of multiple tokens, multiple policies, and multiple processes.

---
*The goal of the system is not to hide errors, but to ensure quality assurance with provable, minimal exposure.*
