package cabinet

import "fmt"

// This file is the manifest side of ABI-profile generic dispatch (ref 02 §3/§7,
// job input "generic dispatch" section): pinning a module's manifest to an
// admitted AbiProfile, and checking that the manifest's declared op-set is a
// subset of the profile's. Setx's dispatch-time use of a binding is in
// service.go (the genericDispatch CabinetOption, off by default: AC-03-5).
//
// The binding is deliberately a separate value, not new fields on
// ModuleDescriptor: ModuleDescriptor is a public, shape-pinned type, and its
// identity (Checksum()) must not change because a module also has a profile.

// Manifest-level violation codes (ref 02 §3: "a manifest rögzíti a profilt:
// abi.profile: {id, digest}; a digest eltérése elutasítás... a manifest op-készlete
// a profil op-készletének részhalmaza").
const (
	// CodeProfilePinMismatch: the manifest's abi.profile {id, digest} does not
	// match the admitted profile's own identity (either field).
	CodeProfilePinMismatch = "PROFILE_DIGEST_MISMATCH"
	// CodeManifestOpNotInProfile: the manifest declares an operation the pinned
	// profile does not have (an "extra manifest-op", AC-03-2).
	CodeManifestOpNotInProfile = "MANIFEST_OP_NOT_IN_PROFILE"
)

// ModuleProfileBinding ties one module's content to an admitted ABI profile.
type ModuleProfileBinding struct {
	ModuleID       string
	ModuleChecksum string // the module's Checksum() at binding time; a different content is a stale binding and is rejected
	// EnvelopeDigest is the authenticated release-envelope identity of the module (#162/WP-02, CICMODENVv1 digest);
	// it is required, so a binding can not exist for a module whose artifact was not admitted (review F3).
	EnvelopeDigest string
	// Profile is an immutable snapshot (a deep copy taken at admission): later changes to the caller's value do not reach it.
	Profile       *AbiProfile
	ProfileID     string
	ProfileDigest string
	Operations    []string // the manifest's declared op-set, a non-empty subset of the profile's
}

// Operation returns the profile operation with that name only if the module's manifest also declares it.
func (b *ModuleProfileBinding) Operation(name string) (AbiOperation, bool) {
	for _, d := range b.Operations {
		if d == name {
			return b.Profile.FindOperation(name)
		}
	}
	return AbiOperation{}, false
}

// NewModuleProfileBinding binds a module to an admitted profile.
//
// profile must already be admitted (ProfileRegistry.Admit): this constructor does
// not re-run pinning, signature or semantics checks, only the manifest-to-profile
// binding:
//   - pin (the manifest's abi.profile {id, digest}) must name exactly the admitted
//     profile and the digest Admit returned for it; any mismatch is rejected
//     (CodeProfilePinMismatch), with no fallback to the legacy profile.
//   - declaredOps (the manifest's own op declaration) must be non-empty and a
//     subset of the profile's operation names; any other name is rejected
//     (CodeManifestOpNotInProfile).
func NewModuleProfileBinding(mod *ModuleDescriptor, envelopeDigest string, profile *AbiProfile, admittedDigest string, pin PinnedProfile, declaredOps []string) (*ModuleProfileBinding, error) {
	if envelopeDigest == "" {
		return nil, fmt.Errorf("%w: a profile binding requires the module's authenticated envelope digest", ErrInvalid)
	}
	if mod == nil || profile == nil {
		return nil, fmt.Errorf("%w: a module and an admitted AbiProfile are required", ErrInvalid)
	}
	if pin.ID != profile.ProfileID || pin.Digest != admittedDigest {
		return nil, &ABIProfileError{Code: CodeProfilePinMismatch}
	}
	if len(declaredOps) == 0 {
		return nil, fmt.Errorf("%w: manifest operations must be non-empty", ErrInvalid)
	}
	profileOps := make(map[string]bool, len(profile.Operations))
	for _, op := range profile.Operations {
		profileOps[op.Name] = true
	}
	for _, name := range declaredOps {
		if !profileOps[name] {
			return nil, &ABIProfileError{Code: CodeManifestOpNotInProfile}
		}
	}
	return &ModuleProfileBinding{
		ModuleID: mod.ID(), ModuleChecksum: mod.Checksum(), EnvelopeDigest: envelopeDigest, Profile: profile.snapshot(),
		ProfileID: pin.ID, ProfileDigest: pin.Digest, Operations: append([]string(nil), declaredOps...),
	}, nil
}

// snapshot returns a deep copy of the profile (no shared slices, maps or pointers).
func (p *AbiProfile) snapshot() *AbiProfile {
	c := *p
	c.RequiredHostCapabilities = append([]string(nil), p.RequiredHostCapabilities...)
	if p.Limits != nil {
		l := *p.Limits
		c.Limits = &l
	}
	c.Operations = make([]AbiOperation, len(p.Operations))
	for i, op := range p.Operations {
		if op.EffectResolution != nil {
			er := *op.EffectResolution
			er.Map = make(map[string]string, len(op.EffectResolution.Map))
			for k, v := range op.EffectResolution.Map {
				er.Map[k] = v
			}
			op.EffectResolution = &er
		}
		c.Operations[i] = op
	}
	return &c
}
