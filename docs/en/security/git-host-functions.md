# Git host functions: capability contract

The relay offers WASM modules a typed `git` host module. A module never receives a shell or a path: it names a **registered repository** by ID and asks for one of a small set of operations. This document is the contract between the operator, the module author and the relay.

## 1. What a module can call

| Function | Allowed | Notes |
|---|---|---|
| `git.status` | yes | working-tree status of a registered repository |
| `git.fetch` | yes | from a configured remote of that repository |
| `git.checkout` | yes | an existing branch, tag or commit; a remote-only branch name is not turned into a tracking branch |
| `git.commit` | yes | exactly the paths named in the request; changes staged earlier by someone else are not included |
| `git.clone` | yes | only from an exact source listed in the manifest, into the registered location |
| `git.push` | **no** | withdrawn, see section 5 |

## 2. The capability manifest

A module declares its reach in the signed `project.yaml`, in the `capability_manifest.git` block. Nothing is implied: an operation, repository, remote, ref or clone source that is not listed is refused.

```yaml
abi:
  imports:
    - module: git
      functions: [status, fetch, checkout]
capability_manifest:
  git:
    - repo: infra                 # one exact registered repository ID, no wildcards
      ops: [status, fetch, checkout]
      remotes: [origin]           # required for fetch: configured remote NAMES
      refs: ["main", "release/*"] # required for clone and checkout: exact names or "prefix/*"
      # sources: ["https://example.org/infra.git"]   # required for clone: exact URLs
```

Checked when the module is loaded (a violation rejects the module):

- every imported `git.*` function must be covered by an op in the manifest;
- `ops` may contain only `clone`, `fetch`, `checkout`, `commit`, `status`; **`push` is an error**, and importing `git.push` is an error;
- `fetch` needs `remotes`, `clone` and `checkout` need `refs`, `clone` needs `sources`;
- ref patterns name branches and tags only: nothing under `refs/` other than `refs/heads/` and `refs/tags/`, no `HEAD`, no bare `*`.

Checked on every call: the calling instance, the repository, the operation, the remote, the ref or the source against the rules above. A module that is not verified (no CICSourceCA provenance, trust not bootstrapped) gets no git capability at all.

### Identity and lifecycle

- The caller is identified by the **instance identity** the runtime attaches to the call: module ID, the checksum of the artifact the instance was created from, a per-instance serial and the digest of the manifest in force when it was created. A guest cannot supply or change it; the start section of a module runs under it as well.
- A grant belongs to the verified artifact **and** to that manifest. Loading the same module ID again revokes the earlier grant first; an instance that started under another artifact or another manifest is refused.
- Loads of one module ID are serialised. A grant is staged, the artifact is stored, then the grant is activated for exactly that artifact; a staged grant authorises nothing.

## 3. What the relay checks on every call

- **Remotes** must be configured in the repository. The URL(s) a fetch will read from, with URL rewriting and included configuration applied, must be `https://` or `ssh://` (printable ASCII, no whitespace, no credentials in the URL). A local path or `file://` source exists only as an operator-enabled option that is not wired in production.
- **Refs** follow git's ref-name rules; paths for `commit` are relative, inside the repository, never under `.git`, and never pathspec magic.
- **Repository configuration**: a registered repository may carry only the keys that `git clone` writes, remote `url`/`pushurl`/`fetch`, branch `remote`/`merge`, `user.name`/`user.email`, and the SHA-256 `extensions.objectformat`. Any other key (a program to run, a URL rewrite, an include, a worktree configuration, ...) makes every operation on that repository fail. Fix the repository, not the relay.
- **Git itself** runs with hooks off, only the `https` and `ssh` transports, literal pathspecs, no prompts, and **no global or system configuration** (see `CIC_GIT_GLOBAL_CONFIG` below). The child process gets an allowlisted environment, not the relay's.
- Credentials in a URL are masked wherever text that can contain a URL leaves the git layer (errors, git's own output, logs); validation errors do not repeat the URL at all.

## 4. Operator configuration

| Variable | Meaning |
|---|---|
| `CIC_INFRA_REPO_PATH`, `CIC_GIT_SOURCE_LOCAL_PATH` | register the repositories `infra` and `iac-config` |
| `CIC_GIT_GLOBAL_CONFIG` | optional absolute path of a git configuration file read as the *global* configuration (for example a credential helper). Unset: none is read. It must be a regular file that is not writable by group or others; otherwise git operations are refused. |

## 5. Push is withdrawn

`git.push` cannot currently be granted. Pushing from a guest needs the exact source object, the identity of the remote, the exact destination reference and its expected previous state to be tied to one authorised decision; the `git` command line resolves references by name on both ends and cannot give that guarantee. Until a push primitive that does is designed and proven:

- a manifest cannot grant `push` and a module cannot import it (both are load errors);
- the registry refuses `push` whatever a grant says;
- the `git.push` host function still exists, so a module that imports it can link, but it is a stub that always answers with an error and never reaches the git layer.

Bringing push back is a separate decision and work package.

## 6. Assumptions the operator must keep

- The registered repository directories are **not writable by anyone less trusted than the relay's authenticated API principals and the operators**. Those principals can already run commands as the relay user; modules cannot.
- The remote git servers are outside the relay's control; the relay only reads from them (`fetch`, `clone`).

## 7. Behaviour changes

- A module that imports a `git.*` function without a matching manifest rule is rejected at load.
- No module receives a git capability unless it is verified.
- `checkout <name>` no longer creates a local tracking branch from a remote-only name.
- Repositories with configuration outside the allowlist in section 3 are refused until cleaned.
- Global git configuration is no longer read.
