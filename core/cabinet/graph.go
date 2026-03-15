package cabinet

import (
	"fmt"
	"strings"
)

// ExecutionGraphStep is a single parsed step in a workflow.
type ExecutionGraphStep struct {
	Raw          string // original step string, preserved for ProofTrace
	ComponentID  string // e.g. "deploy@1.0"
	FunctionName string // e.g. "Process"
	InputKey     string // context variable to read as input
	OutputKey    string // context variable to write result into
}

// ExecutionGraph is the ordered list of parsed steps for a workflow execution.
type ExecutionGraph struct {
	Steps []ExecutionGraphStep
}

// ParseExecutionGraph parses a slice of step strings into an ExecutionGraph.
//
// Supported formats:
//
//	New: "component@version.function(inputKey) -> outputKey"
//	Old: "component.function(data)"   (outputKey defaults to "result")
//
// Both formats may appear in the same workflow (backward-compatible).
func ParseExecutionGraph(steps []string) (*ExecutionGraph, error) {
	parsed := make([]ExecutionGraphStep, 0, len(steps))
	for _, s := range steps {
		step, err := parseStep(s)
		if err != nil {
			return nil, fmt.Errorf("invalid step %q: %w", s, err)
		}
		parsed = append(parsed, step)
	}
	return &ExecutionGraph{Steps: parsed}, nil
}

// parseStep parses a single step string.
func parseStep(s string) (ExecutionGraphStep, error) {
	// Split on " -> " first to detect new format.
	var outputKey string
	core := s
	if idx := strings.Index(s, " -> "); idx != -1 {
		outputKey = strings.TrimSpace(s[idx+4:])
		core = s[:idx]
		if outputKey == "" {
			return ExecutionGraphStep{}, fmt.Errorf("empty output key after '->'")
		}
	} else {
		outputKey = "result" // backward-compat default
	}

	// Parse "componentID.functionName(inputKey)"
	parenOpen := strings.Index(core, "(")
	if parenOpen == -1 || !strings.HasSuffix(core, ")") {
		return ExecutionGraphStep{}, fmt.Errorf("missing parentheses")
	}
	inputKey := core[parenOpen+1 : len(core)-1]
	before := core[:parenOpen]

	lastDot := strings.LastIndex(before, ".")
	if lastDot <= 0 || lastDot == len(before)-1 {
		return ExecutionGraphStep{}, fmt.Errorf("expected component.function before '('")
	}

	return ExecutionGraphStep{
		Raw:          s,
		ComponentID:  before[:lastDot],
		FunctionName: before[lastDot+1:],
		InputKey:     inputKey,
		OutputKey:    outputKey,
	}, nil
}