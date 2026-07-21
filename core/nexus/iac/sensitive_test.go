package iac

import "testing"

func secretRef(path string) map[string]interface{} {
	return map[string]interface{}{"secret_ref": path}
}

func TestSensitivePolicy_ValidateValue(t *testing.T) {
	cases := []struct {
		name  string
		class SensitiveClass
		value interface{}
		ok    bool
	}{
		{"secret + secret ref", SensitiveSecret, secretRef("/vault/db/password"), true},
		{"secret + plaintext is rejected", SensitiveSecret, "hunter2", false},
		{"secret + empty ref is rejected", SensitiveSecret, map[string]interface{}{"secret_ref": ""}, false},
		{"none + plaintext", SensitiveNone, "public-value", true},
		{"none + secret ref is rejected", SensitiveNone, secretRef("/vault/db/password"), false},
		{"unset class + plaintext", "", "x", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := SensitivePolicy{Class: c.class}.ValidateValue(c.value)
			if (err == nil) != c.ok {
				t.Fatalf("ValidateValue = %v, want ok=%v", err, c.ok)
			}
		})
	}
}

func TestField_AsSecretRef(t *testing.T) {
	ref, ok := ExpandField(secretRef("/vault/db/password")).AsSecretRef()
	if !ok || ref.Path != "/vault/db/password" {
		t.Fatalf("AsSecretRef = %+v, %v", ref, ok)
	}
	if _, ok := ExpandField("plain").AsSecretRef(); ok {
		t.Errorf("a plaintext value must not read as a secret reference")
	}
}

func TestSensitivePolicy_AllowsBackup(t *testing.T) {
	p := SensitivePolicy{Backups: []BackupOp{BackupTransit, BackupStore}}
	if !p.AllowsBackup(BackupTransit) || !p.AllowsBackup(BackupStore) {
		t.Errorf("transit/store should be allowed")
	}
	if p.AllowsBackup(BackupRestore) {
		t.Errorf("restore was not granted")
	}
}

func TestParseSchema_Sensitive(t *testing.T) {
	doc := map[string]interface{}{
		"db_password": map[string]interface{}{
			"behavior": map[string]interface{}{
				"sensitive": map[string]interface{}{
					"class":    "secret",
					"exposure": "remote",
					"backup":   []interface{}{"transit", "store"},
				},
			},
		},
	}
	s := ParseSchema(doc)
	f := s.Fields[0]
	if f.Sensitive.Class != SensitiveSecret || f.Sensitive.Exposure != ExposureRemote {
		t.Fatalf("sensitive = %+v", f.Sensitive)
	}
	if !f.Sensitive.AllowsBackup(BackupTransit) || !f.Sensitive.AllowsBackup(BackupStore) {
		t.Errorf("backups = %v", f.Sensitive.Backups)
	}
}

func TestSchema_ValidateSensitive(t *testing.T) {
	schema := ParseSchema(map[string]interface{}{
		"db_password": map[string]interface{}{
			"behavior": map[string]interface{}{"sensitive": map[string]interface{}{"class": "secret", "exposure": "remote"}},
		},
		"region": map[string]interface{}{}, // ordinary field
	})

	// A secret held as a reference passes; region (plaintext) is fine.
	ok := map[string]interface{}{"db_password": secretRef("/vault/db/password"), "region": "eu-1"}
	if err := schema.ValidateSensitive(ok); err != nil {
		t.Errorf("valid object rejected: %v", err)
	}

	// A secret held as plaintext is rejected, and the error names the path.
	bad := map[string]interface{}{"db_password": "hunter2", "region": "eu-1"}
	err := schema.ValidateSensitive(bad)
	if err == nil {
		t.Fatal("plaintext secret must be rejected")
	}
	if got := err.Error(); got == "" || got[:12] != "/db_password" {
		t.Errorf("error should name the path, got %q", got)
	}
}
