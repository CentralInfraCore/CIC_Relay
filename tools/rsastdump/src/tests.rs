//! Unit tests: the sample fixture plus the edge shapes it does not cover.

use super::{dump_all, dump_file, dump_source, FileDump, Item};

const SAMPLE: &str = include_str!("../testdata/sample.rs");

fn sample() -> FileDump {
    let d = dump_source("sample.rs", SAMPLE);
    assert!(d.error.is_none(), "{:?}", d.error);
    d
}

fn find<'a>(d: &'a FileDump, kind: &str, name: &str) -> &'a Item {
    d.items
        .iter()
        .find(|i| i.kind == kind && i.name == name)
        .unwrap_or_else(|| panic!("no {kind} {name}"))
}

#[test]
fn file_level() {
    let d = sample();
    assert_eq!(
        d.inner_doc,
        "Sample exercises the shapes the companion generator must report."
    );
    assert_eq!(d.mods, ["child", "tests"]);
}

#[test]
fn kinds_and_order() {
    let d = sample();
    let got: Vec<(&str, &str)> = d
        .items
        .iter()
        .map(|i| (i.kind.as_str(), i.name.as_str()))
        .collect();
    assert_eq!(
        got,
        [
            ("mod", "child"),
            ("const", "ALPHA"),
            ("static", "GAMMA"),
            ("trait", "Store"),
            ("struct", "Boxed"),
            ("enum", "Kind"),
            ("type", "Alias"),
            ("impl", "Boxed"),
            ("method", "new"),
            ("method", "touch"),
            ("impl", "fmt::Display for Kind"),
            ("method", "fmt"),
            ("impl", "Eq for Kind"),
            ("fn", "handle"),
            ("fn", "helper"),
            ("macro", "local_macro"),
            ("mod", "tests"),
            ("fn", "works"),
        ]
    );
}

#[test]
fn use_expansion_and_aliases() {
    let d = sample();
    let s = find(&d, "static", "GAMMA");
    assert_eq!(s.refs, ["serde_json::Value", "serde_json::Value::Null"]);
    let b = find(&d, "struct", "Boxed");
    assert_eq!(
        b.refs,
        [
            "crate::graph::Node",
            "std::collections::HashMap",
            "serde_json::Value",
            "serde::Serialize",
            "serde::Deserialize",
        ]
    );
    assert_eq!(
        b.derives.as_deref().unwrap(),
        ["Debug", "Clone", "serde::Serialize", "serde::Deserialize"]
    );
    assert_eq!(b.visibility.as_deref(), Some("pub(crate)"));
    // `use crate::graph::{self, ..}` binds `graph` itself.
    assert_eq!(find(&d, "enum", "Kind").refs, ["crate::graph::Edge"]);
}

#[test]
fn trait_shape() {
    let d = sample();
    let t = find(&d, "trait", "Store");
    assert_eq!(t.methods.as_deref().unwrap(), ["get", "len", "put"]);
    assert_eq!(
        t.supertraits.as_deref().unwrap(),
        ["std::fmt::Debug", "Send"]
    );
    assert_eq!(t.calls, ["Store::keys"]); // self in a default body
    assert_eq!(t.doc, "Store is a trait with a supertrait.");
}

#[test]
fn impls_and_methods() {
    let d = sample();
    let i = find(&d, "impl", "fmt::Display for Kind");
    assert_eq!(i.receiver.as_deref(), Some("Kind"));
    assert_eq!(i.trait_path.as_deref(), Some("std::fmt::Display"));
    let m = find(&d, "method", "fmt");
    assert_eq!(m.visibility, None); // fixed by the trait
    assert_eq!(m.trait_path.as_deref(), Some("std::fmt::Display"));
    // write!(..) arguments are read; super:: is kept for the generator.
    assert_eq!(
        m.calls,
        ["serde_json::to_string", "super::sibling::Thing::default"]
    );
    let new = find(&d, "method", "new");
    assert_eq!(new.visibility.as_deref(), Some("pub"));
    assert_eq!(new.receiver.as_deref(), Some("Boxed"));
    assert_eq!(find(&d, "method", "touch").calls, ["Boxed::size"]);
    assert_eq!(
        find(&d, "impl", "Eq for Kind").trait_path.as_deref(),
        Some("Eq")
    );
}

#[test]
fn typed_calls_only() {
    let d = sample();
    let h = find(&d, "fn", "handle");
    assert_eq!(
        h.calls,
        [
            "std::collections::HashMap::new",
            "Boxed::touch",         // let b = Boxed { .. }
            "Kind::describe",       // let c: Kind
            "reqwest::Client::get", // typed parameter
            "self::child::helper",  // declared module gets self::
            "helper",
            "make",
            "Ok",
        ]
    );
    assert!(!h.calls.iter().any(|c| c.ends_with("go"))); // untyped receiver
    assert!(h.refs.contains(&"serde_json::json".to_string())); // macro path
}

#[test]
fn test_marking() {
    let d = sample();
    assert!(find(&d, "mod", "tests").test);
    let w = find(&d, "fn", "works");
    assert!(w.test);
    assert_eq!(w.mod_path, ["tests"]);
    assert!(!find(&d, "fn", "helper").test);
}

#[test]
fn macro_visibility() {
    let src =
        "#[macro_export]\nmacro_rules! m { () => {} }\nlazy_static! { static ref X: u8 = 1; }\n";
    let d = dump_source("m.rs", src);
    assert_eq!(d.items.len(), 1); // unnamed invocation is not an item
    assert_eq!(d.items[0].visibility.as_deref(), Some("pub"));
}

#[test]
fn misc_items() {
    let src = r#"
#![cfg(test)]
extern crate alloc as heap;
extern crate _x as _;
use other::{Thing as Renamed, Gone as _, glob::*};
use other::sub::{self as s2};
/// U.
pub(super) union U { a: u32, b: heap::boxed::Box<u8> }
pub(in crate::x) type T = Renamed;
extern "C" { pub fn ext(p: *const u8) -> s2::Out; }
impl<'a> Tr for &'a (u8, u16) { fn f(&self) { (self).g(); } }
impl !Send for U {}
#[cfg(test)]
impl U { #[tokio::test] async fn t(x: &Renamed) { x.h(); } }
fn q() { let (a, b) = (1, 2); <u8 as From<u8>>::from(1); let z: [u8; 1]; }
"#;
    let d = dump_source("x.rs", src);
    assert!(d.error.is_none(), "{:?}", d.error);
    assert!(d.items.iter().all(|i| i.test)); // #![cfg(test)] on the file
    let u = find(&d, "union", "U");
    assert_eq!(u.visibility.as_deref(), Some("pub(super)"));
    assert_eq!(u.refs, ["alloc::boxed::Box"]);
    assert_eq!(u.doc, "U.");
    let t = find(&d, "type", "T");
    assert_eq!(t.visibility.as_deref(), Some("pub(in crate::x)"));
    assert_eq!(t.refs, ["other::Thing"]);
    assert_eq!(find(&d, "fn", "ext").refs, ["other::sub::Out"]);
    let i = find(&d, "impl", "Tr for (u8,u16)");
    assert_eq!(i.receiver.as_deref(), Some("(u8,u16)"));
    assert_eq!(find(&d, "method", "f").calls, ["(u8,u16)::g"]);
    assert_eq!(
        find(&d, "impl", "!Send for U").trait_path.as_deref(),
        Some("Send")
    );
    assert_eq!(find(&d, "method", "t").calls, ["other::Thing::h"]);
    assert_eq!(find(&d, "fn", "q").calls, Vec::<String>::new());
}

#[test]
fn parse_error_has_position() {
    let d = dump_source("bad.rs", "fn x( {");
    let e = d.error.unwrap();
    assert!(e.starts_with("bad.rs:1:"), "{e}");
    assert!(d.items.is_empty());
}

#[test]
fn read_error_and_dump_all() {
    let out = dump_all(["/nonexistent/x.rs"]);
    assert_eq!(out.files.len(), 1);
    assert!(out.files[0]
        .error
        .as_deref()
        .unwrap()
        .contains("/nonexistent/x.rs"));
    let ok = dump_file(concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/sample.rs"));
    assert!(ok.error.is_none());
    let json = serde_json::to_value(&ok).unwrap();
    assert!(json.get("error").is_none());
    assert_eq!(json["items"][10]["trait"], "std::fmt::Display");
}
