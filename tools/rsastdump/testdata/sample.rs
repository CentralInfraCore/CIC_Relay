//! Sample exercises the shapes the companion generator must report.

pub mod child;

use std::collections::HashMap;
use std::fmt;

use serde::{Deserialize, Serialize};
use serde_json as json;
use crate::graph::{self, Node};
use super::sibling::Thing;

/// Grouped constant.
pub const ALPHA: u32 = 1;

static GAMMA: json::Value = json::Value::Null;

/// Store is a trait with a supertrait.
pub trait Store: fmt::Debug + Send {
    /// Gets a value.
    fn get(&self, key: &str) -> Option<String>;
    fn put(&mut self, key: &str, val: String);
    fn len(&self) -> usize {
        self.keys().len()
    }
}

/// Boxed holds a node and a map.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub(crate) struct Boxed<T> {
    item: T,
    node: Node,
    map: HashMap<String, json::Value>,
}

/// Kind of a thing.
#[derive(Debug, PartialEq)]
pub enum Kind {
    /// First.
    A,
    B(graph::Edge),
}

pub type Alias = reqwest::Client;

impl<T> Boxed<T> {
    /// Creates a box.
    pub fn new(item: T, node: Node) -> Self {
        Self { item, node, map: HashMap::new() }
    }

    fn touch(&self) -> usize {
        self.size()
    }
}

impl fmt::Display for Kind {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", json::to_string(&Thing::default()).unwrap_or_default())
    }
}

impl Eq for Kind {}

/// Handle uses typed locals and a child module.
pub fn handle(client: &reqwest::Client, n: Node) -> anyhow::Result<()> {
    let b = Boxed { item: 1, node: n, map: HashMap::new() };
    b.touch();
    let c: Kind = Kind::A;
    c.describe();
    client.get("http://example.com");
    let v = json::json!({ "k": 1 });
    child::helper(v);
    helper();
    let untyped = make();
    untyped.go();
    Ok(())
}

fn helper() {}

macro_rules! local_macro {
    () => {};
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn works() {
        helper();
    }
}
