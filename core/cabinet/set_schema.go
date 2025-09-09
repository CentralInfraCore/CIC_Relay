package cabinet

import (
	"sort"
	"strings"
)

// SetOptions hordozza a felhasználói beállításokat a Set kéréshez.
// A Priority jelenleg: low|normal|high (normal az alapértelmezett).
type SetOptions struct {
	TimeoutMS int    `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
	Priority  string `json:"priority,omitempty"   yaml:"priority,omitempty"` // low|normal|high
}

// SetPayload a Set belépési kérés kanonikus reprezentációja.
// A Modules csak hivatkozás; semmi sincs példányosítva.
type SetPayload struct {
	WorkflowID string                 `json:"workflow_id" yaml:"workflow_id"`
	Modules    []ModuleID             `json:"modules"     yaml:"modules"`
	Payload    map[string]any         `json:"payload"     yaml:"payload"`
	Options    *SetOptions            `json:"options,omitempty" yaml:"options,omitempty"`
}

// CanonicalizeSetPayload normalizálja a bemenetet (trim, dedup/sort, defaultok).
func CanonicalizeSetPayload(in *SetPayload) {
	if in == nil {
		return
	}
	in.WorkflowID = strings.TrimSpace(in.WorkflowID)

	// Options defaultok
	if in.Options != nil && in.Options.Priority == "" {
		in.Options.Priority = "normal"
	}

	// Modules dedup + sort a determinisztikus összehasonlításért/hash-ért
	if len(in.Modules) > 0 {
		seen := make(map[ModuleID]struct{}, len(in.Modules))
		uniq := make([]ModuleID, 0, len(in.Modules))
		for _, id := range in.Modules {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			uniq = append(uniq, id)
		}
		sort.Slice(uniq, func(i, j int) bool { return string(uniq[i]) < string(uniq[j]) })
		in.Modules = uniq
	}
}

