package iac

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCollection_ElementKey(t *testing.T) {
	mapColl := Collection{Topology: TopologyMap, Keys: []string{"name"}}
	assert.Equal(t, "name=nic-0", mapColl.ElementKey(map[string]interface{}{"name": "nic-0", "subnet": "a"}))

	// multi-key: stable, sorted regardless of declared order
	multi := Collection{Topology: TopologyMap, Keys: []string{"zone", "name"}}
	assert.Equal(t, "name=nic-0,zone=eu", multi.ElementKey(map[string]interface{}{"name": "nic-0", "zone": "eu"}))

	// map topology, non-map element → no key
	assert.Empty(t, mapColl.ElementKey("scalar"))

	// missing key field rendered explicitly
	assert.Equal(t, "name=<nil>", mapColl.ElementKey(map[string]interface{}{"subnet": "a"}))

	// set → value form
	assert.Equal(t, "10.0.0.0/16", Collection{Topology: TopologySet}.ElementKey("10.0.0.0/16"))

	// atomic / unset → no per-element identity
	assert.Empty(t, Collection{Topology: TopologyAtomic}.ElementKey("x"))
	assert.Empty(t, Collection{}.ElementKey("x"))
}
