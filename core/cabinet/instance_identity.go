package cabinet

import "context"

// InstanceIdentity names one live module instance and the exact artifact it was instantiated from. It is
// created by the WasmManager, before the instance exists, and travels in the context of every call into
// the instance (the instantiation itself included, so a start section runs under it too). A host function
// reads it back from the context it is given. The guest never supplies it and cannot alter it: it is not
// part of anything the guest can write, and each instance gets its own Serial, so one instance's identity
// can never be mistaken for the next one's, however the names are reused.
type InstanceIdentity struct {
	Name     string // the module ID the instance was started for
	Artifact string // checksum of the module artifact that was compiled for this instance
	Serial   uint64 // unique per instance for the lifetime of the WasmManager
	// Binding is an opaque token the embedding application supplies for the module when the instance is
	// created (WasmManagerConfig.InstanceBinding). The cabinet only carries it; the relay uses it to tie
	// an instance to the capability manifest that was in force when the instance started, which the
	// artifact checksum alone does not capture (the same wasm can be loaded with another manifest).
	Binding string
}

// instanceIdentityKey is the unexported context key under which WithInstanceIdentity stores the
// InstanceIdentity; no other package can set or overwrite it.
type instanceIdentityKey struct{}

// WithInstanceIdentity returns ctx carrying id.
func WithInstanceIdentity(ctx context.Context, id InstanceIdentity) context.Context {
	return context.WithValue(ctx, instanceIdentityKey{}, id)
}

// InstanceIdentityFrom returns the identity carried by ctx, if any.
func InstanceIdentityFrom(ctx context.Context) (InstanceIdentity, bool) {
	id, ok := ctx.Value(instanceIdentityKey{}).(InstanceIdentity)
	return id, ok && id.Name != "" && id.Artifact != "" && id.Serial != 0
}
