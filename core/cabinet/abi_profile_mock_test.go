package cabinet

// MockProfileSignatureVerifier is a placeholder ProfileSignatureVerifier: it
// returns Err unconditionally (nil = accept, non-nil = the given rejection).
// MOCK, PROVISIONAL — not a real signature check; see ProfileSignatureVerifier's
// doc comment. The name says MOCK: if this ever appears as an argument to
// NewProfileRegistry in a production call path, that is a defect in the caller,
// not something this job authorized.
type MockProfileSignatureVerifier struct{ Err error }

// VerifySignedProfile returns the configured Err unconditionally (MOCK, see the type).
func (m MockProfileSignatureVerifier) VerifySignedProfile(signedBytes []byte, sig ProfileSignature) error {
	return m.Err
}
