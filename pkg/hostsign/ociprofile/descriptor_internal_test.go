package ociprofile

import (
	"strings"
	"testing"
)

// The descriptor the digest covers names this profile's own identifier: a renamed profile can not keep the digest.
func TestDescriptorNamesTheProfileID(t *testing.T) {
	if !strings.Contains(descriptor, `"id":"`+ProfileID+`"`) {
		t.Fatalf("the descriptor does not carry the profile id %q", ProfileID)
	}
}
