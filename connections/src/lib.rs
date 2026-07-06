//! Typed model of the Cabinet's *Connections*: the logical relationships between
//! schemas, plugins, and graph nodes (feature-006).
//!
//! This is the first Rust foothold in the system. It is deliberately small but
//! real — enough to exercise the full build/lint/test/supply-chain gate — and is
//! expected to grow as more of the Cabinet's relationship logic moves to Rust.

pub mod vault;

use std::collections::BTreeMap;

/// The kind of node a [`NodeRef`] points at.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum NodeKind {
    /// A schema definition.
    Schema,
    /// A plugin / module descriptor.
    Plugin,
    /// A node in an execution graph.
    GraphNode,
}

/// A stable reference to a node, by kind and identifier.
#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct NodeRef {
    /// What kind of node this refers to.
    pub kind: NodeKind,
    /// The node's identifier, unique within its kind.
    pub id: String,
}

impl NodeRef {
    /// Creates a reference to the node `id` of the given `kind`.
    #[must_use]
    pub fn new(kind: NodeKind, id: impl Into<String>) -> Self {
        Self {
            kind,
            id: id.into(),
        }
    }
}

/// A directed, typed relationship from one node to another.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Connection {
    /// The source node.
    pub from: NodeRef,
    /// The target node.
    pub to: NodeRef,
    /// A free-form label describing the relationship (e.g. `"validates"`).
    pub relation: String,
}

/// An append-only set of [`Connection`]s with neighbour lookup.
///
/// Ordering is deterministic (backed by a [`BTreeMap`]) so that any hash or
/// serialization of the graph is reproducible — a prerequisite for `ProofTrace`.
#[derive(Debug, Clone, Default)]
pub struct ConnectionGraph {
    edges: BTreeMap<NodeRef, Vec<Connection>>,
}

impl ConnectionGraph {
    /// Creates an empty graph.
    #[must_use]
    pub fn new() -> Self {
        Self::default()
    }

    /// Records a directed `relation` from `from` to `to`.
    pub fn connect(&mut self, from: NodeRef, to: NodeRef, relation: impl Into<String>) {
        let conn = Connection {
            from: from.clone(),
            to,
            relation: relation.into(),
        };
        self.edges.entry(from).or_default().push(conn);
    }

    /// Returns every connection whose source is `from`, in insertion order.
    #[must_use]
    pub fn outgoing(&self, from: &NodeRef) -> &[Connection] {
        self.edges.get(from).map_or(&[], Vec::as_slice)
    }

    /// Total number of recorded connections.
    #[must_use]
    pub fn len(&self) -> usize {
        self.edges.values().map(Vec::len).sum()
    }

    /// Returns `true` if no connections have been recorded.
    #[must_use]
    pub fn is_empty(&self) -> bool {
        self.edges.values().all(Vec::is_empty)
    }
}

#[cfg(test)]
mod tests {
    use super::{ConnectionGraph, NodeKind, NodeRef};

    #[test]
    fn new_graph_is_empty() {
        let g = ConnectionGraph::new();
        assert!(g.is_empty());
        assert_eq!(g.len(), 0);
    }

    #[test]
    fn records_and_queries_outgoing() {
        let schema = NodeRef::new(NodeKind::Schema, "user.v1");
        let plugin = NodeRef::new(NodeKind::Plugin, "cert.selfsigned");

        let mut g = ConnectionGraph::new();
        g.connect(schema.clone(), plugin.clone(), "validated-by");

        assert_eq!(g.len(), 1);
        assert!(!g.is_empty());

        let out = g.outgoing(&schema);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].to, plugin);
        assert_eq!(out[0].relation, "validated-by");

        assert!(g.outgoing(&plugin).is_empty());
    }

    #[test]
    fn outgoing_preserves_insertion_order() {
        let root = NodeRef::new(NodeKind::GraphNode, "start");
        let mut g = ConnectionGraph::new();
        for id in ["a", "b", "c"] {
            g.connect(root.clone(), NodeRef::new(NodeKind::GraphNode, id), "next");
        }
        let seen: Vec<_> = g.outgoing(&root).iter().map(|c| c.to.id.as_str()).collect();
        assert_eq!(seen, ["a", "b", "c"]);
    }
}
