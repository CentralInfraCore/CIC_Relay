package ois

import (
	"errors"
	"testing"
)

func TestAllowlistPolicy_Authorize(t *testing.T) {
	p := NewAllowlistPolicy().
		Allow(Rollback, "relay.operator").
		Allow(Apply, "relay.operator", "ci.bot")

	t.Run("authorized actor for action", func(t *testing.T) {
		err := p.Authorize(Intent{Actor: "relay.operator", Action: Rollback, TargetRef: "abc123", Reason: "restore stable"})
		if err != nil {
			t.Fatalf("expected authorized, got %v", err)
		}
	})

	t.Run("actor not authorized for action", func(t *testing.T) {
		err := p.Authorize(Intent{Actor: "ci.bot", Action: Rollback, TargetRef: "abc123"})
		var d *Denied
		if !errors.As(err, &d) {
			t.Fatalf("expected *Denied, got %v", err)
		}
		if d.Reason == "" {
			t.Fatal("denied reason must be non-empty")
		}
	})

	t.Run("second allowed actor for apply", func(t *testing.T) {
		if err := p.Authorize(Intent{Actor: "ci.bot", Action: Apply, TargetRef: "x"}); err != nil {
			t.Fatalf("ci.bot should be authorized for apply, got %v", err)
		}
	})

	t.Run("empty actor refused", func(t *testing.T) {
		err := p.Authorize(Intent{Action: Rollback, TargetRef: "abc123"})
		if err == nil {
			t.Fatal("empty actor must be refused")
		}
	})

	t.Run("empty target ref refused", func(t *testing.T) {
		err := p.Authorize(Intent{Actor: "relay.operator", Action: Rollback})
		if err == nil {
			t.Fatal("empty target ref must be refused")
		}
	})

	t.Run("unknown action refused", func(t *testing.T) {
		err := p.Authorize(Intent{Actor: "relay.operator", Action: Action("delete"), TargetRef: "x"})
		if err == nil {
			t.Fatal("unknown action must be refused")
		}
	})
}

func TestDenied_IsError(t *testing.T) {
	var err error = &Denied{Reason: "nope"}
	if err.Error() != "nope" {
		t.Fatalf("Error(): got %q", err.Error())
	}
}

// AllowlistPolicy must satisfy the Policy seam.
var _ Policy = (*AllowlistPolicy)(nil)
