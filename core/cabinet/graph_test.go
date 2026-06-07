package cabinet_test

import (
	"testing"

	"centralrelay/core/cabinet"
)

func TestParseExecutionGraph(t *testing.T) {
	t.Run("old format – outputKey defaults to result", func(t *testing.T) {
		g, err := cabinet.ParseExecutionGraph([]string{"deploy@1.0.Process(input)"})
		if err != nil {
			t.Fatal(err)
		}
		s := g.Steps[0]
		if s.ComponentID != "deploy@1.0" {
			t.Errorf("ComponentID = %q", s.ComponentID)
		}
		if s.FunctionName != "Process" {
			t.Errorf("FunctionName = %q", s.FunctionName)
		}
		if s.InputKey != "input" {
			t.Errorf("InputKey = %q", s.InputKey)
		}
		if s.OutputKey != "result" {
			t.Errorf("OutputKey = %q, want result", s.OutputKey)
		}
	})

	t.Run("new format – named output key", func(t *testing.T) {
		g, err := cabinet.ParseExecutionGraph([]string{"deploy@1.0.Process(input) -> deployed"})
		if err != nil {
			t.Fatal(err)
		}
		s := g.Steps[0]
		if s.OutputKey != "deployed" {
			t.Errorf("OutputKey = %q, want deployed", s.OutputKey)
		}
		if s.InputKey != "input" {
			t.Errorf("InputKey = %q", s.InputKey)
		}
	})

	t.Run("multi-step mixed format", func(t *testing.T) {
		steps := []string{
			"step1@1.0.Run(input) -> out1",
			"step2@1.0.Run(out1)",
		}
		g, err := cabinet.ParseExecutionGraph(steps)
		if err != nil {
			t.Fatal(err)
		}
		if len(g.Steps) != 2 {
			t.Fatalf("want 2 steps, got %d", len(g.Steps))
		}
		if g.Steps[0].OutputKey != "out1" {
			t.Errorf("step[0].OutputKey = %q", g.Steps[0].OutputKey)
		}
		if g.Steps[1].InputKey != "out1" {
			t.Errorf("step[1].InputKey = %q", g.Steps[1].InputKey)
		}
		if g.Steps[1].OutputKey != "result" {
			t.Errorf("step[1].OutputKey = %q", g.Steps[1].OutputKey)
		}
	})

	t.Run("component with dots in name (old format)", func(t *testing.T) {
		g, err := cabinet.ParseExecutionGraph([]string{"core.test@1.0.echo(input)"})
		if err != nil {
			t.Fatal(err)
		}
		if g.Steps[0].ComponentID != "core.test@1.0" {
			t.Errorf("ComponentID = %q", g.Steps[0].ComponentID)
		}
	})

	t.Run("empty output key after -> is an error", func(t *testing.T) {
		_, err := cabinet.ParseExecutionGraph([]string{"deploy@1.0.Process(input) -> "})
		if err == nil {
			t.Fatal("expected error for empty output key")
		}
	})

	t.Run("missing parentheses is an error", func(t *testing.T) {
		_, err := cabinet.ParseExecutionGraph([]string{"deploy@1.0.Process"})
		if err == nil {
			t.Fatal("expected error for missing parentheses")
		}
	})

	t.Run("dot at position zero is an error", func(t *testing.T) {
		_, err := cabinet.ParseExecutionGraph([]string{".Process(input)"})
		if err == nil {
			t.Fatal("expected error: component name is empty (dot at position 0)")
		}
	})

	t.Run("empty function name (trailing dot) is an error", func(t *testing.T) {
		_, err := cabinet.ParseExecutionGraph([]string{"deploy.(input)"})
		if err == nil {
			t.Fatal("expected error: function name is empty (dot at last position)")
		}
	})

	t.Run("Raw field preserved", func(t *testing.T) {
		raw := "deploy@1.0.Process(input) -> out"
		g, err := cabinet.ParseExecutionGraph([]string{raw})
		if err != nil {
			t.Fatal(err)
		}
		if g.Steps[0].Raw != raw {
			t.Errorf("Raw = %q, want %q", g.Steps[0].Raw, raw)
		}
	})
}
