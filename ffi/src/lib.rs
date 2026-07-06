//! # cic-ffi — the unsafe airlock
//!
//! The single crate in the workspace permitted to use `unsafe`. It is the C-ABI
//! seam that links the safe Rust trust-flow crates (`connections` /
//! `vault-adapter` / `module`) into the Go relay.
//!
//! Every export routes through one shared airlock ([`marshal`]) that fixes the
//! three phases from `features/feature-008/ffi-boundary.md`:
//!
//! 1. **INGRESS** (`unsafe`): copy the caller's bytes into an *owned* Rust value.
//!    No borrow into caller memory escapes this phase.
//! 2. **CORE** (safe): a plain `fn(&[u8]) -> Option<Vec<u8>>` — no raw pointers,
//!    no `unsafe`, panic-contained via [`std::panic::catch_unwind`]. `None`
//!    means "rejected" (validation failure), never a crash.
//! 3. **EGRESS** (`unsafe`): hand an owned buffer back to the caller as
//!    `ptr` + `len`; the caller releases it via [`cic_ffi_free`].
//!
//! `unsafe` therefore lives only in [`marshal`] and [`cic_ffi_free`], and only
//! for pointer/length handling — never business logic. The trust-flow crates
//! stay `unsafe_code = "forbid"`.

use std::panic::{catch_unwind, UnwindSafe};
use std::ptr;
use std::slice;

/// Maximum input accepted at the seam (1 MiB). Control-plane requests are tiny;
/// anything larger is rejected on ingress rather than copied (ffi-boundary.md
/// rule 3 — validate/size-cap at the seam).
const MAX_INPUT_LEN: usize = 1024 * 1024;

/// C-ABI version of this staticlib. Bump on any breaking change to an export's
/// signature or semantics; the Go side checks it against its own expectation to
/// catch a stale or mismatched library link.
const CIC_FFI_ABI_VERSION: u32 = 1;

/// The safe core. Contains no `unsafe` and no raw pointers — every function here
/// takes owned/borrowed-owned bytes and returns owned bytes, so it is ordinary,
/// machine-checked safe Rust dispatching into the trust-flow crates.
mod logic {
    use connections::vault::{AuthScope, Cn, Connection};
    use serde_json::{json, Value};

    /// Identity echo — the linkability smoke test for the Rust ↔ Go bridge.
    // Always `Some`, but the `Option` return is the uniform core contract the
    // airlock ([`super::marshal`]) dispatches; keeping it matches every other
    // core function's signature.
    #[allow(clippy::unnecessary_wraps)]
    pub fn echo(input: &[u8]) -> Option<Vec<u8>> {
        Some(input.to_vec())
    }

    /// Build a Vault Connection request (trust-flow step 1) from a JSON
    /// `{"subject": "...", "scope": ["cn", ...]}` document and return its
    /// **canonical** form: the scope is deduplicated and sorted by
    /// [`AuthScope`]'s `BTreeSet`, so the output is reproducible (a `ProofTrace`
    /// prerequisite). Returns `None` — a clean rejection, never a panic — on
    /// malformed JSON, a missing/non-string subject, or an empty subject.
    pub fn connection_request(input: &[u8]) -> Option<Vec<u8>> {
        let doc: Value = serde_json::from_slice(input).ok()?;

        let subject = doc.get("subject")?.as_str()?.trim();
        if subject.is_empty() {
            return None;
        }

        let cns: Vec<Cn> = doc
            .get("scope")
            .and_then(Value::as_array)
            .map(|arr| {
                arr.iter()
                    .filter_map(Value::as_str)
                    .filter(|s| !s.is_empty())
                    .map(Cn::new)
                    .collect()
            })
            .unwrap_or_default();

        let conn = Connection::request(Cn::new(subject), AuthScope::from_cns(cns));

        let canonical = json!({
            "subject": conn.subject().as_str(),
            "scope": conn.scope().cns().map(Cn::as_str).collect::<Vec<_>>(),
        });
        serde_json::to_vec(&canonical).ok()
    }
}

/// The shared airlock. INGRESS (copy + own) → CORE (safe, panic-contained) →
/// EGRESS (owned buffer to the caller). `core` is a safe function over owned
/// bytes; `None` from it (or a panic, or empty output) yields a null return.
///
/// # Safety
///
/// `ptr` must point to `len` valid bytes (or be null with `len == 0`), and
/// `out_len` must be a valid, writable `*mut usize` (or null).
unsafe fn marshal(
    ptr: *const u8,
    len: usize,
    out_len: *mut usize,
    core: impl FnOnce(&[u8]) -> Option<Vec<u8>> + UnwindSafe,
) -> *mut u8 {
    if out_len.is_null() {
        return ptr::null_mut();
    }
    // SAFETY: `out_len` checked non-null just above; single write.
    *out_len = 0;

    // Reject oversized input at the seam, before allocating/copying it.
    if len > MAX_INPUT_LEN {
        return ptr::null_mut();
    }

    // ---- INGRESS: copy + own immediately; no borrow escapes. ----
    let owned: Vec<u8> = if ptr.is_null() || len == 0 {
        Vec::new()
    } else {
        // SAFETY: the caller guarantees `ptr` points to `len` valid bytes. We
        // copy straight into an owned `Vec` and never retain the raw pointer.
        slice::from_raw_parts(ptr, len).to_vec()
    };

    // ---- CORE: safe, panic-contained. `Err` (panic) or `None` (rejection). ----
    // The backstop recovers only in unwinding (dev/test) builds; the release
    // profile is `panic = "abort"`, so a panic aborts before reaching here.
    let Ok(Some(result)) = catch_unwind(move || core(&owned)) else {
        return ptr::null_mut();
    };
    if result.is_empty() {
        return ptr::null_mut();
    }

    // ---- EGRESS: hand an owned buffer to the caller. ----
    let boxed = result.into_boxed_slice();
    let produced = boxed.len();
    let raw = Box::into_raw(boxed).cast::<u8>();
    // SAFETY: `out_len` checked non-null at the top; single write.
    *out_len = produced;
    raw
}

/// Release a buffer previously returned by a `cic_ffi_*` allocating export.
///
/// A null pointer or a zero length is a no-op, so it is always safe to call
/// even when an export returned "nothing".
///
/// # Safety
///
/// `ptr` and `len` must be exactly the pair returned by a `cic_ffi_*` export,
/// and this function must be called at most once for that pair.
#[no_mangle]
pub unsafe extern "C" fn cic_ffi_free(ptr: *mut u8, len: usize) {
    if ptr.is_null() || len == 0 {
        return;
    }
    // SAFETY: by the contract above, `ptr`/`len` came from `into_boxed_slice`
    // in [`marshal`] (so capacity == len) and is released at most once.
    // Rebuilding the `Box<[u8]>` lets Rust drop it with the correct layout.
    let slice = ptr::slice_from_raw_parts_mut(ptr, len);
    drop(Box::from_raw(slice));
}

/// Echo `len` bytes from `ptr` back to the caller — the airlock linkability
/// smoke test. Result must be released with [`cic_ffi_free`].
///
/// # Safety
///
/// See [`marshal`].
#[no_mangle]
pub unsafe extern "C" fn cic_ffi_ping(ptr: *const u8, len: usize, out_len: *mut usize) -> *mut u8 {
    marshal(ptr, len, out_len, logic::echo)
}

/// Build a Vault Connection request and return its canonical JSON form — the
/// first real trust-flow function bound through the seam.
///
/// Input: a JSON `{"subject": "...", "scope": [...]}` document (`ptr`/`len`).
/// Output: canonical JSON with a deduplicated, sorted scope, allocated for the
/// caller to release with [`cic_ffi_free`]. Returns null (and `*out_len == 0`)
/// on malformed input, an empty subject, a null `out_len`, or a panic.
///
/// # Safety
///
/// See [`marshal`].
#[no_mangle]
pub unsafe extern "C" fn cic_ffi_connection_request(
    ptr: *const u8,
    len: usize,
    out_len: *mut usize,
) -> *mut u8 {
    marshal(ptr, len, out_len, logic::connection_request)
}

/// Returns the C-ABI version of this staticlib. The Go side checks it against
/// its own expectation at startup to catch a stale or mismatched link. No
/// pointers cross here, so it needs no airlock.
#[no_mangle]
pub extern "C" fn cic_ffi_abi_version() -> u32 {
    CIC_FFI_ABI_VERSION
}

#[cfg(test)]
mod tests {
    use super::{cic_ffi_abi_version, cic_ffi_connection_request, cic_ffi_free, cic_ffi_ping};
    use serde_json::{json, Value};
    use std::ptr;
    use std::slice;

    #[test]
    fn ping_round_trips_bytes() {
        let input = b"hello-cic";
        let mut out_len: usize = 0;
        // SAFETY: `input` is a valid slice; `out_len` is a valid local.
        let out = unsafe { cic_ffi_ping(input.as_ptr(), input.len(), &raw mut out_len) };
        assert!(!out.is_null());
        assert_eq!(out_len, input.len());
        // SAFETY: `out`/`out_len` are the pair just returned by the export.
        let got = unsafe { slice::from_raw_parts(out, out_len) };
        assert_eq!(got, input);
        // SAFETY: releasing the exact pair returned above, exactly once.
        unsafe { cic_ffi_free(out, out_len) };
    }

    #[test]
    fn ping_empty_input_returns_null() {
        let mut out_len: usize = 7;
        // SAFETY: null input with zero length is an explicitly supported call.
        let out = unsafe { cic_ffi_ping(ptr::null(), 0, &raw mut out_len) };
        assert!(out.is_null());
        assert_eq!(out_len, 0);
    }

    #[test]
    fn ping_null_out_len_returns_null() {
        let input = b"x";
        // SAFETY: `input` is valid; a null `out_len` is an explicitly handled case.
        let out = unsafe { cic_ffi_ping(input.as_ptr(), input.len(), ptr::null_mut()) };
        assert!(out.is_null());
    }

    #[test]
    fn free_null_is_noop() {
        // SAFETY: null/zero is a documented no-op.
        unsafe { cic_ffi_free(ptr::null_mut(), 0) };
    }

    #[test]
    fn connection_request_canonicalizes_scope() {
        let input = br#"{"subject":"mod-x","scope":["cn-b","cn-a","cn-b"]}"#;
        let mut out_len: usize = 0;
        // SAFETY: `input` is a valid slice; `out_len` is a valid local.
        let out =
            unsafe { cic_ffi_connection_request(input.as_ptr(), input.len(), &raw mut out_len) };
        assert!(!out.is_null());
        // SAFETY: `out`/`out_len` are the pair just returned by the export.
        let got = unsafe { slice::from_raw_parts(out, out_len) }.to_vec();
        // SAFETY: releasing the exact pair returned above, exactly once.
        unsafe { cic_ffi_free(out, out_len) };

        let v: Value = serde_json::from_slice(&got).expect("valid JSON out");
        assert_eq!(v["subject"], "mod-x");
        // deduped ("cn-b" once) + sorted (BTreeSet) regardless of input order
        assert_eq!(v["scope"], json!(["cn-a", "cn-b"]));
    }

    #[test]
    fn connection_request_rejects_empty_subject() {
        let input = br#"{"subject":"","scope":[]}"#;
        let mut out_len: usize = 9;
        // SAFETY: `input` is valid; `out_len` is a valid local.
        let out =
            unsafe { cic_ffi_connection_request(input.as_ptr(), input.len(), &raw mut out_len) };
        assert!(out.is_null());
        assert_eq!(out_len, 0);
    }

    #[test]
    fn connection_request_rejects_malformed_json() {
        let input = b"not json at all";
        let mut out_len: usize = 9;
        // SAFETY: `input` is valid; `out_len` is a valid local.
        let out =
            unsafe { cic_ffi_connection_request(input.as_ptr(), input.len(), &raw mut out_len) };
        assert!(out.is_null());
    }

    #[test]
    fn ping_rejects_oversized_input() {
        let big = vec![b'x'; super::MAX_INPUT_LEN + 1];
        let mut out_len: usize = 9;
        // SAFETY: `big` is a valid slice; `out_len` is a valid local.
        let out = unsafe { cic_ffi_ping(big.as_ptr(), big.len(), &raw mut out_len) };
        assert!(out.is_null());
        assert_eq!(out_len, 0);
    }

    #[test]
    fn abi_version_matches_the_constant() {
        assert_eq!(cic_ffi_abi_version(), super::CIC_FFI_ABI_VERSION);
    }
}
