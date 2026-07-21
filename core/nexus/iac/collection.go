package iac

import (
	"fmt"
	"sort"
	"strings"
)

// Collection topology — how a list/set field's elements are identified. Taken
// (deliberately, not reinvented) from Kubernetes server-side-apply: a list is
// atomic, a set, or a map keyed by named fields. Topology governs element
// identity, and downstream merge, ownership, drift, deletion, and proof paths
// (iac-object-model.md, spec #3). This file is the formalization + element
// identity; the merge/ownership use is a later round.

// CollectionTopology names how a list field's elements are identified.
type CollectionTopology string

const (
	// TopologyAtomic: the whole list is a single opaque value (one owner).
	TopologyAtomic CollectionTopology = "atomic"
	// TopologySet: elements are unique by value; order is irrelevant.
	TopologySet CollectionTopology = "set"
	// TopologyMap: elements are identified by one or more key fields.
	TopologyMap CollectionTopology = "map"
)

// Collection describes a list field's topology. Keys apply only to TopologyMap.
type Collection struct {
	Topology CollectionTopology
	Keys     []string
}

// ElementKey returns a stable identity for a list element under this topology:
//   - map: the sorted "k=v" tuple of the element's key fields ("name=nic-0"); a
//     missing key field is rendered empty;
//   - set: a string form of the element value (a placeholder identity until the
//     CIC Canonical Object Encoding lands — spec #1);
//   - atomic (or unset): "" — the list has no per-element identity, it is one value.
func (c Collection) ElementKey(elem interface{}) string {
	switch c.Topology {
	case TopologyMap:
		m, ok := elem.(map[string]interface{})
		if !ok {
			return ""
		}
		parts := make([]string, 0, len(c.Keys))
		keys := append([]string(nil), c.Keys...)
		sort.Strings(keys) // stable regardless of declared order
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
		}
		return strings.Join(parts, ",")
	case TopologySet:
		return fmt.Sprintf("%v", elem)
	default: // atomic or unset
		return ""
	}
}
