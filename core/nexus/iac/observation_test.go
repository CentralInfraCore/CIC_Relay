package iac

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestObservation_Coverage(t *testing.T) {
	o := Observation{
		Observed:            []string{"/id", "/display_name"},
		AuthoritativeAbsent: []string{"/secondary_dns"},
	}
	assert.Equal(t, CoverageObserved, o.Coverage("/id"))
	assert.Equal(t, CoverageAbsent, o.Coverage("/secondary_dns"))
	assert.Equal(t, CoverageUnobserved, o.Coverage("/boot_volume/key")) // in neither set
}

func TestObservation_ClassifyField(t *testing.T) {
	o := Observation{
		Observed:            []string{"/cidr", "/display_name"},
		AuthoritativeAbsent: []string{"/dns_label", "/route_table"},
	}

	// observed
	assert.Equal(t, VerdictConformant, o.ClassifyField("/cidr", true, true))
	assert.Equal(t, VerdictDrift, o.ClassifyField("/cidr", true, false))

	// authoritatively absent
	assert.Equal(t, VerdictDrift, o.ClassifyField("/route_table", true, false))         // intent wants it → missing
	assert.Equal(t, VerdictObservedAbsent, o.ClassifyField("/dns_label", false, false)) // intent omits it → fine

	// unobserved — conformance cannot be claimed regardless of match
	assert.Equal(t, VerdictUnobserved, o.ClassifyField("/private_ip", true, true))
}
