//! End-to-end: the binary's argument handling and JSON output.

use std::process::Command;

const BIN: &str = env!("CARGO_BIN_EXE_rsastdump");

#[test]
fn no_args_is_usage_error() {
    let out = Command::new(BIN).output().unwrap();
    assert_eq!(out.status.code(), Some(2));
    assert!(String::from_utf8_lossy(&out.stderr).contains("usage"));
}

#[test]
fn dumps_files_as_json() {
    let sample = concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/sample.rs");
    let out = Command::new(BIN)
        .args([sample, "/nonexistent.rs"])
        .output()
        .unwrap();
    assert!(out.status.success());
    let v: serde_json::Value = serde_json::from_slice(&out.stdout).unwrap();
    let files = v["files"].as_array().unwrap();
    assert_eq!(files.len(), 2);
    assert_eq!(files[0]["mods"][0], "child");
    assert!(files[1]["error"].is_string());
}
