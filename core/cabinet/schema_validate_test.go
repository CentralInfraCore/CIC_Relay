package cabinet

import (
	"context"
	"errors"
	"testing"
)

func TestValidateSchema(t *testing.T) {
	t.Run("empty accepts passes anything", func(t *testing.T) {
		if err := ValidateSchema(nil, map[string]interface{}{}); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("matching $schema passes", func(t *testing.T) {
		data := map[string]interface{}{"$schema": "relay.config@1.0", "x": 1}
		if err := ValidateSchema([]string{"relay.config@1.0"}, data); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("non-matching $schema returns ErrInvalid", func(t *testing.T) {
		data := map[string]interface{}{"$schema": "other@1.0"}
		err := ValidateSchema([]string{"relay.config@1.0"}, data)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected ErrInvalid, got %v", err)
		}
	})

	t.Run("missing $schema key returns ErrInvalid", func(t *testing.T) {
		data := map[string]interface{}{"x": 1}
		err := ValidateSchema([]string{"relay.config@1.0"}, data)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected ErrInvalid, got %v", err)
		}
	})

	t.Run("non-map input returns ErrInvalid", func(t *testing.T) {
		err := ValidateSchema([]string{"relay.config@1.0"}, "not a map")
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected ErrInvalid, got %v", err)
		}
	})

	t.Run("$schema not a string returns ErrInvalid", func(t *testing.T) {
		data := map[string]interface{}{"$schema": 42}
		err := ValidateSchema([]string{"relay.config@1.0"}, data)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected ErrInvalid, got %v", err)
		}
	})
}

func TestValidateInputSchema(t *testing.T) {
	ctx := context.Background()
	mod := func(accepts ...string) *ModuleDescriptor {
		m, err := NewModuleDescriptor(ctx, BaseBlock{Name: "m", Version: "1.0"}, "native", accepts, nil, nil)
		if err != nil {
			t.Fatalf("NewModuleDescriptor: %v", err)
		}
		return m
	}

	t.Run("empty Accepts passes any input", func(t *testing.T) {
		if err := validateInputSchema(mod(), "anything"); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("matching $schema passes", func(t *testing.T) {
		input := map[string]interface{}{"$schema": "deploy@1.0", "host": "x"}
		if err := validateInputSchema(mod("deploy@1.0", "infra@2.0"), input); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("non-matching $schema returns ErrInvalid", func(t *testing.T) {
		input := map[string]interface{}{"$schema": "unknown@1.0"}
		err := validateInputSchema(mod("deploy@1.0"), input)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected ErrInvalid, got %v", err)
		}
	})

	t.Run("missing $schema key returns ErrInvalid", func(t *testing.T) {
		input := map[string]interface{}{"host": "x"}
		err := validateInputSchema(mod("deploy@1.0"), input)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected ErrInvalid, got %v", err)
		}
	})

	t.Run("$schema not a string returns ErrInvalid", func(t *testing.T) {
		input := map[string]interface{}{"$schema": 42}
		err := validateInputSchema(mod("deploy@1.0"), input)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected ErrInvalid, got %v", err)
		}
	})

	t.Run("input not a map returns ErrInvalid", func(t *testing.T) {
		err := validateInputSchema(mod("deploy@1.0"), "raw string")
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected ErrInvalid, got %v", err)
		}
	})

	t.Run("second schema in Accepts list matches", func(t *testing.T) {
		input := map[string]interface{}{"$schema": "infra@2.0"}
		if err := validateInputSchema(mod("deploy@1.0", "infra@2.0"), input); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})
}
