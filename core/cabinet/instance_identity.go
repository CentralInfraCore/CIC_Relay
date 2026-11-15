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
}

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
