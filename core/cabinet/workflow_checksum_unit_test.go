package cabinet

import "testing"

func TestWorkflow_Checksum_CoversLine(t *testing.T) {
	wf := &Workflow{Meta: BaseBlock{Name: "wf", Version: "1"}, Steps: []string{"a","b"}}
	sum, err := wf.Checksum()
	if err != nil || len(sum) == 0 {
		t.Fatalf("Checksum: want non-empty, got sum=%q err=%v", sum, err)
	}
}
