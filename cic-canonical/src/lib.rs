//! CIC Canonical Object Encoding — number normalization (spec #1).
//!
//! The Rust peer of the Go implementation in `core/nexus/iac/number.go`. Both must
//! produce byte-identical canonical forms so a digest computed in Go verifies in
//! Rust and vice versa. The contract is defined over the JSON numeric *literal*
//! (the text a number arrives as), which is the language-neutral surface:
//!
//! - `4`, `4.0`, `4e0` all canonicalize to `"4"`;
//! - `-0`, `-0.0` canonicalize to `"0"`;
//! - `4.50` canonicalizes to `"4.5"`;
//! - integer literals keep full precision (string-based, no float round-trip), so
//!   an integer beyond `f64`'s 53-bit mantissa canonicalizes to its exact digits.
//!
//! The test vectors below mirror `TestCanonicalNumber` in `number_test.go`; that Go
//! table is the source of truth.

/// Returns the canonical decimal form of a JSON numeric literal, or `None` if `s`
/// is not a number.
///
/// Integer literals are normalized as exact digit strings (sign, leading zeros and
/// negative zero folded); other numeric literals normalize through `f64`'s shortest
/// plain-decimal form.
#[must_use]
pub fn canonical_number(s: &str) -> Option<String> {
    let s = s.trim();
    if s.is_empty() {
        return None;
    }
    if let Some(int) = canonical_integer(s) {
        return Some(int);
    }
    let f: f64 = s.parse().ok()?;
    Some(canonical_float(f))
}

/// Normalizes an integer literal to exact digits, or `None` if `s` is not an
/// integer literal. Mirrors Go's `big.Int` path: strips a leading `+`, folds
/// leading zeros, and renders negative zero as `"0"`.
fn canonical_integer(s: &str) -> Option<String> {
    let (neg, digits) = match s.strip_prefix('-') {
        Some(rest) => (true, rest),
        None => (false, s.strip_prefix('+').unwrap_or(s)),
    };
    if digits.is_empty() || !digits.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let magnitude = digits.trim_start_matches('0');
    if magnitude.is_empty() {
        return Some("0".to_string()); // all zeros, including "-0"
    }
    Some(if neg {
        format!("-{magnitude}")
    } else {
        magnitude.to_string()
    })
}

/// Renders a float in plain (non-exponent) shortest form, folding negative zero to
/// `"0"`. Rust's `f64` `Display` is shortest round-trip and never uses an exponent,
/// matching Go's `FormatFloat(f, 'f', -1, 64)`.
fn canonical_float(f: f64) -> String {
    let s = format!("{f}");
    if s == "-0" {
        "0".to_string()
    } else {
        s
    }
}

#[cfg(test)]
mod tests {
    use super::canonical_number;

    #[test]
    fn vectors_mirror_go() {
        // Mirrors TestCanonicalNumber in core/nexus/iac/number_test.go.
        let cases = [
            ("4", "4"),
            ("4.0", "4"),
            ("4.5", "4.5"),
            ("4e0", "4"),
            ("4.50", "4.5"),
            ("1e3", "1000"),
            ("16", "16"),
            ("-0", "0"),
            ("-0.0", "0"),
            ("-7", "-7"),
            ("+4", "4"),
            ("007", "7"),
            // precision: beyond f64's 53-bit mantissa, kept exact
            ("9007199254740993", "9007199254740993"),
            ("9007199254740992", "9007199254740992"),
        ];
        for (input, want) in cases {
            assert_eq!(
                canonical_number(input).as_deref(),
                Some(want),
                "canonical_number({input:?})"
            );
        }
    }

    #[test]
    fn non_numeric_is_none() {
        for input in ["", "  ", "abc", "4.2.1", "0x10", "--1", "1e"] {
            assert_eq!(canonical_number(input), None, "canonical_number({input:?})");
        }
    }

    #[test]
    fn adjacent_large_integers_differ() {
        // The precision guarantee: these must not collapse to the same form.
        assert_ne!(
            canonical_number("9007199254740993"),
            canonical_number("9007199254740992")
        );
    }
}

// PROBE (do not merge): exercises the mutation-pr gate; has no test on purpose.
pub fn probe_add(a: u32, b: u32) -> u32 {
    a + b
}
