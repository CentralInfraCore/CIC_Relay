package main

import (
	"fmt"
	"os"
	"strings"

	"centralrelay/core/cabinet"
	"centralrelay/pkg/realization"
)

const (
	// realizationContextEnv is the path of the operator's realization context object.
	realizationContextEnv = "CIC_REALIZATION_CONTEXT"
	// realizationContextPinEnv is the sha256:<hex> of the exact object bytes the operator pinned (required with the path).
	realizationContextPinEnv = "CIC_REALIZATION_CONTEXT_PIN"
)

// admittedRealization is the loaded, pinned realization context; nil when none is configured.
var admittedRealization *realization.Admitted

// realizationDigest is the digest of the admitted realization context, or cabinet.None. It is a function because the cabinet
// is built before the context is loaded.
func realizationDigest() string {
	if admittedRealization == nil {
		return cabinet.None
	}
	return admittedRealization.Digest
}

// loadRealizationContext admits the operator's realization context. Unset: none (the records say "none"). Set: the pin is
// required, and an unreadable, unpinned or malformed object is a start-up error: a context the operator named but that cannot
// be admitted must not turn into "no context".
func loadRealizationContext() error {
	path := strings.TrimSpace(os.Getenv(realizationContextEnv))
	if path == "" {
		return nil
	}
	pin := strings.TrimSpace(os.Getenv(realizationContextPinEnv))
	if pin == "" {
		return fmt.Errorf("%s is set but %s is not: an unpinned context authenticates nothing", realizationContextEnv, realizationContextPinEnv)
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-supplied trust-domain path
	if err != nil {
		return fmt.Errorf("read realization context %q: %w", path, err)
	}
	a, err := realization.Admit(raw, pin)
	if err != nil {
		return err
	}
	admittedRealization = a
	return nil
}
