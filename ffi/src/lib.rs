//! # cic-ffi — the unsafe airlock
//!
//! The single crate in the workspace permitted to use `unsafe`. It is the C-ABI
//! seam that links the safe Rust trust-flow crates (`connections` /
//! `vault-adapter` / `module`) into the Go relay.
//!
//! Every export follows the three-phase airlock fixed in
//! `features/feature-008/ffi-boundary.md`:
//!
//! 1. **INGRESS** (`unsafe`): copy the caller's bytes into an *owned* Rust value
//!    and validate them. No borrow into caller memory escapes this phase.
//! 2. **CORE** (safe): dispatch into the safe crates — no raw pointers, no
//!    `unsafe`, panic-contained via [`std::panic::catch_unwind`].
//! 3. **EGRESS** (`unsafe`): hand an owned buffer back to the caller as
//!    `ptr` + `len`; the caller releases it via [`cic_ffi_free`].
//!
//! `unsafe` therefore touches only pointer/length handling at the two edges,
//! never business logic. The trust-flow crates stay `unsafe_code = "forbid"`.

use std::panic::catch_unwind;
use std::ptr;
use std::slice;

/// The safe core. Contains no `unsafe` and no raw pointers — only owned values.
///
/// This is where real trust-flow calls (`connections` / `vault-adapter` /
/// `module`) are dispatched once the boundary is wired. Today it is an identity
/// echo that proves the airlock round-trips bytes correctly end to end.
mod logic {
    /// Process an owned, validated input into an owned output.
    #[must_use]
    pub fn echo(input: Vec<u8>) -> Vec<u8> {
        input
    }
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
    // in an export below (so capacity == len) and is released at most once.
    // Rebuilding the `Box<[u8]>` lets Rust drop it with the correct layout.
    let slice = ptr::slice_from_raw_parts_mut(ptr, len);
    drop(Box::from_raw(slice));
}

/// Echo `len` bytes from `ptr` back to the caller — the airlock demonstration
/// and the linkability smoke test for the Rust ↔ Go bridge.
///
/// On success returns a freshly allocated buffer of `*out_len` bytes that the
/// caller **must** release with [`cic_ffi_free`]. Returns null (and sets
/// `*out_len` to `0`) for empty input, a null `out_len`, or a panic in the core.
///
/// # Safety
///
/// `ptr` must point to `len` valid bytes (or be null with `len == 0`), and
/// `out_len` must be a valid, writable `*mut usize` (or null).
#[no_mangle]
pub unsafe extern "C" fn cic_ffi_ping(ptr: *const u8, len: usize, out_len: *mut usize) -> *mut u8 {
    if out_len.is_null() {
        return ptr::null_mut();
    }
    // SAFETY: `out_len` checked non-null just above; single write.
    *out_len = 0;

    // ---- INGRESS (unsafe): copy + own immediately; no borrow escapes. ----
    let owned: Vec<u8> = if ptr.is_null() || len == 0 {
        Vec::new()
    } else {
        // SAFETY: the caller guarantees `ptr` points to `len` valid bytes. We
        // copy straight into an owned `Vec` and never retain the raw pointer.
        slice::from_raw_parts(ptr, len).to_vec()
    };

    // ---- CORE (safe): all business logic runs here, panic-contained. ----
    // Backstop: `catch_unwind` recovers only in unwinding (dev/test) builds; the
    // release profile is `panic = "abort"`, so a panic aborts before reaching here.
    let Ok(result) = catch_unwind(move || logic::echo(owned)) else {
        return ptr::null_mut();
    };
    if result.is_empty() {
        return ptr::null_mut();
    }

    // ---- EGRESS (unsafe): hand an owned buffer to the caller. ----
    let boxed = result.into_boxed_slice();
    let produced = boxed.len();
    let raw = Box::into_raw(boxed).cast::<u8>();
    // SAFETY: `out_len` checked non-null at the top; single write.
    *out_len = produced;
    raw
}

#[cfg(test)]
mod tests {
    use super::{cic_ffi_free, cic_ffi_ping};
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
}
