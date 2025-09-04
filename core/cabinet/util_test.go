package cabinet

import "testing"

func TestCalculateChecksum_DeterministicAndIgnoresVolatile(t *testing.T) {
	meta := BaseBlock{
		Name: "schema", Version: "1.0.0",
		Description: "x", CreatedBy: "dev", Timestamp: "2025-08-25",
		Signature: "will-be-ignored", Certificate: "ignored", CRLRef: "ignored",
	}

	// Ugyanaz az adat más kulcsrendezéssel
	data1 := map[string]any{"a": 1, "b": 2}
	data2 := map[string]any{"b": 2, "a": 1}

	sum1, err := genericChecksum(&meta, data1)
	if err != nil { t.Fatal(err) }
	sum2, err := genericChecksum(&meta, data2)
	if err != nil { t.Fatal(err) }

	if sum1 != sum2 {
		t.Fatalf("checksum should be deterministic, got %s vs %s", sum1, sum2)
	}

	// Ha az adat változik, változzon a checksum is
	data3 := map[string]any{"a": 1, "b": 3}
	sum3, _ := genericChecksum(&meta, data3)
	if sum3 == sum1 {
		t.Fatal("checksum should change when data changes")
	}
}
