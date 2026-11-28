package trustbundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	k1   = "sha256:" + strings.Repeat("1", 64)
	k2   = "sha256:" + strings.Repeat("2", 64)
	pd   = "sha256:" + strings.Repeat("a", 64)
	now  = time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	good = `{"kind":"CICTrustBundle","bundle_version":5,"issued_at":"2026-11-28T10:00:00Z",` +
		`"authorities":[{"key_id":"` + k1 + `","roles":["module:sign"]},{"key_id":"` + k2 + `","roles":[]}],` +
		`"accepted_profiles":[{"id":"oci-1","digest":"` + pd + `"}]}`
)

func pol(raw string) Policy { return Policy{PinnedDigest: Digest([]byte(raw)), MinVersion: 5} }

func code(t *testing.T, raw string, p Policy, st State) string {
	t.Helper()
	_, err := Admit([]byte(raw), p, st, now)
	require.Error(t, err)
	return CodeOf(err)
}

func TestAdmitsThePinnedBundleAndReadsItsContent(t *testing.T) {
	a, err := Admit([]byte(good), pol(good), State{HighWatermark: 5}, now)
	require.NoError(t, err)
	assert.Equal(t, Digest([]byte(good)), a.Digest)
	assert.EqualValues(t, 5, a.Version)
	assert.Equal(t, map[string]bool{k1: true, k2: false}, a.ModuleSign, "a key listed without the role holds no authority")
	assert.Equal(t, map[string]bool{k1: false, k2: false}, a.ProfileSign, "module:sign is not profile:sign")
	assert.Equal(t, []Profile{{ID: "oci-1", Digest: pd}}, a.Profiles)
	assert.True(t, a.IssuedAt.Equal(time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)))
}

func TestProfileSignIsAnIndependentRole(t *testing.T) {
	raw := strings.Replace(good, `"roles":[]`, `"roles":["profile:sign"]`, 1)
	a, err := Admit([]byte(raw), pol(raw), State{}, now)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{k1: false, k2: true}, a.ProfileSign)
	assert.Equal(t, map[string]bool{k1: true, k2: false}, a.ModuleSign, "a profile signer is not a module signer")
	both := strings.Replace(good, `"roles":["module:sign"]`, `"roles":["module:sign","profile:sign"]`, 1)
	a, err = Admit([]byte(both), pol(both), State{}, now)
	require.NoError(t, err)
	assert.True(t, a.ModuleSign[k1] && a.ProfileSign[k1])
}

func TestNotPinned(t *testing.T) {
	other := strings.Replace(good, `"bundle_version":5`, `"bundle_version":6`, 1)
	assert.Equal(t, CodeNotPinned, code(t, other, pol(good), State{}), "a different bundle, even a valid and newer one")
	assert.Equal(t, CodeNotPinned, code(t, good, Policy{}, State{}), "no pin configured is not 'accept anything'")
	assert.Equal(t, CodeNotPinned, code(t, good, Policy{PinnedDigest: "sha256:XYZ"}, State{}), "a malformed pin")
	assert.Equal(t, CodeNotPinned, code(t, good, Policy{PinnedDigest: strings.ToUpper(Digest([]byte(good)))}, State{}))
}

// The pin is checked on the raw bytes before anything else, so a hostile bundle cannot choose its own diagnosis.
func TestAnUnpinnedBundleIsNotParsed(t *testing.T) {
	assert.Equal(t, CodeNotPinned, code(t, `not json at all`, pol(good), State{}))
	assert.Equal(t, CodeNotPinned, code(t, `{"kind":"CICTrustBundle","bundle_version":1}`, pol(good), State{}))
}

func TestInvalidWire(t *testing.T) {
	cases := map[string]string{
		"duplicate key":        strings.Replace(good, `"bundle_version":5,`, `"bundle_version":5,"bundle_version":9,`, 1),
		"unknown key":          strings.Replace(good, `"kind":`, `"extra":1,"kind":`, 1),
		"case-variant key":     strings.Replace(good, `"kind"`, `"Kind"`, 1),
		"missing key":          strings.Replace(good, `"accepted_profiles":[{"id":"oci-1","digest":"`+pd+`"}]`, `"x":1`, 1),
		"trailing data":        good + ` {}`,
		"wrong kind":           strings.Replace(good, `CICTrustBundle`, `CICTrustBundle2`, 1),
		"negative version":     strings.Replace(good, `"bundle_version":5`, `"bundle_version":-5`, 1),
		"fractional version":   strings.Replace(good, `"bundle_version":5`, `"bundle_version":5.5`, 1),
		"string version":       strings.Replace(good, `"bundle_version":5`, `"bundle_version":"5"`, 1),
		"bad issued_at":        strings.Replace(good, `2026-11-28T10:00:00Z`, `yesterday`, 1),
		"bad key_id":           strings.Replace(good, k1, "sha256:ABC", 1),
		"duplicate key_id":     strings.Replace(good, k2, k1, 1),
		"unknown role":         strings.Replace(good, `"module:sign"]}`, `"module:sign","root"]}`, 1),
		"bad profile id":       strings.Replace(good, `"oci-1"`, `"OCI 1"`, 1),
		"bad profile digest":   strings.Replace(good, pd, "sha256:zz", 1),
		"duplicate profile id": strings.Replace(good, `"accepted_profiles":[{"id":"oci-1","digest":"`+pd+`"}]`, `"accepted_profiles":[{"id":"oci-1","digest":"`+pd+`"},{"id":"oci-1","digest":"`+pd+`"}]`, 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, CodeInvalid, code(t, raw, pol(raw), State{}))
		})
	}
}

func TestStaleBelowTheOperatorMinimum(t *testing.T) {
	p := pol(good)
	p.MinVersion = 6
	assert.Equal(t, CodeStale, code(t, good, p, State{}))
	p.MinVersion = 5
	_, err := Admit([]byte(good), p, State{}, now)
	assert.NoError(t, err, "the minimum itself is admitted")
}

func TestRollbackBelowTheHighWatermark(t *testing.T) {
	assert.Equal(t, CodeRollback, code(t, good, pol(good), State{HighWatermark: 6, HighWatermarkDigest: pd}))
	_, err := Admit([]byte(good), pol(good), State{HighWatermark: 5, HighWatermarkDigest: Digest([]byte(good))}, now)
	assert.NoError(t, err, "the same version again (a restart) is not a rollback")
}

// TB-01: one version names one bundle.
func TestSameVersionDifferentContentIsAConflict(t *testing.T) {
	st := State{HighWatermark: 5, HighWatermarkDigest: Digest([]byte(good))}
	other := strings.Replace(good, `"roles":[]`, `"roles":["module:sign"]`, 1) // same version 5, different authorities
	require.NotEqual(t, good, other)
	assert.Equal(t, CodeConflict, code(t, other, pol(other), st))
	_, err := Admit([]byte(good), pol(good), st, now)
	assert.NoError(t, err, "the identical bundle again")
	higher := strings.Replace(other, `"bundle_version":5`, `"bundle_version":6`, 1)
	_, err = Admit([]byte(higher), pol(higher), st, now)
	assert.NoError(t, err, "a different bundle under a higher version")
	_, err = Admit([]byte(other), pol(other), State{}, now)
	assert.NoError(t, err, "no mark yet: nothing to conflict with")
}

func TestFreshness(t *testing.T) {
	p := pol(good)
	p.MaxAge = 2 * time.Hour // issued exactly two hours ago
	_, err := Admit([]byte(good), p, State{}, now)
	assert.NoError(t, err, "exactly at the limit")
	p.MaxAge = 2*time.Hour - time.Second
	assert.Equal(t, CodeExpired, code(t, good, p, State{}))
	p.MaxAge = 0
	_, err = Admit([]byte(good), p, State{}, now.Add(1000*time.Hour))
	assert.NoError(t, err, "no age limit configured")
}

func TestIssuedInTheFuture(t *testing.T) {
	future := strings.Replace(good, `2026-11-28T10:00:00Z`, `2026-11-28T12:06:00Z`, 1)
	assert.Equal(t, CodeExpired, code(t, future, pol(future), State{}), "beyond the clock skew")
	ok := strings.Replace(good, `2026-11-28T10:00:00Z`, `2026-11-28T12:05:00Z`, 1)
	_, err := Admit([]byte(ok), pol(ok), State{}, now)
	assert.NoError(t, err, "within the clock skew")
}

func TestCheckOrder(t *testing.T) {
	// stale AND rolled back AND expired: the minimum comes first; rollback before expiry
	p := pol(good)
	p.MinVersion = 9
	p.MaxAge = time.Second
	assert.Equal(t, CodeStale, code(t, good, p, State{HighWatermark: 7, HighWatermarkDigest: pd}))
	p.MinVersion = 1
	assert.Equal(t, CodeRollback, code(t, good, p, State{HighWatermark: 7, HighWatermarkDigest: pd}))
	assert.Equal(t, CodeExpired, code(t, good, p, State{}))
}

func TestCodeOf(t *testing.T) {
	assert.Equal(t, "", CodeOf(os.ErrNotExist))
	assert.Equal(t, "", CodeOf(nil))
	assert.Equal(t, CodeStale+": m", (&Error{Code: CodeStale, Message: "m"}).Error())
}

func TestStateLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := LoadState(path)
	require.NoError(t, err)
	assert.Equal(t, State{}, st, "first start")

	adm, err := Admit([]byte(good), pol(good), st, now)
	require.NoError(t, err)
	st, err = Commit(path, st, adm)
	require.NoError(t, err)
	assert.EqualValues(t, 5, st.HighWatermark)

	again, err := LoadState(path)
	require.NoError(t, err)
	assert.EqualValues(t, 5, again.HighWatermark, "persisted")
	assert.Equal(t, adm.Digest, again.HighWatermarkDigest, "with the digest of the admitted bundle")

	// never lowered, and nothing is written for a lower version
	before, _ := os.ReadFile(path)
	st, err = Commit(path, st, &Admitted{Version: 3})
	require.NoError(t, err)
	assert.EqualValues(t, 5, st.HighWatermark)
	after, _ := os.ReadFile(path)
	assert.Equal(t, before, after)

	st, err = Commit(path, st, &Admitted{Version: 8})
	require.NoError(t, err)
	assert.EqualValues(t, 8, st.HighWatermark)
	var onDisk State
	data, _ := os.ReadFile(path)
	require.NoError(t, json.Unmarshal(data, &onDisk))
	assert.EqualValues(t, 8, onDisk.HighWatermark)

	left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".tb-state-*"))
	assert.Empty(t, left, "no temporary file is left behind")
}

func TestAZeroStateFileIsAValidFirstStart(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(p, []byte(`{"high_watermark":0,"high_watermark_digest":""}`), 0o600))
	st, err := LoadState(p)
	require.NoError(t, err)
	assert.Equal(t, State{}, st)
}

func TestACorruptStateIsNeverAReset(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"garbage":             `nope`,
		"empty":               ``,
		"unknown":             `{"high_watermark":3,"x":1}`,
		"duplicate":           `{"high_watermark":3,"high_watermark":0}`,
		"negative":            `{"high_watermark":-1}`,
		"missing":             `{}`,
		"mark without digest": `{"high_watermark":3,"high_watermark_digest":""}`,
		"digest without mark": `{"high_watermark":0,"high_watermark_digest":"sha256:` + strings.Repeat("a", 64) + `"}`,
		"malformed digest":    `{"high_watermark":3,"high_watermark_digest":"sha256:zz"}`,
		"old shape":           `{"high_watermark":3}`,
		"wrong type":          `{"high_watermark":"3"}`,
	} {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
		_, err := LoadState(p)
		assert.Error(t, err, name)
	}
	_, err := LoadState(dir) // a directory: unreadable, not "missing"
	assert.Error(t, err)
}

func TestCommitOfTheSameVersionWritesNothing(t *testing.T) {
	// the directory does not exist: any write would fail, so success means that nothing was attempted
	st, err := Commit(filepath.Join(t.TempDir(), "absent", "state.json"), State{HighWatermark: 4}, &Admitted{Version: 4})
	require.NoError(t, err)
	assert.EqualValues(t, 4, st.HighWatermark)
}

func TestCommitFailsLoudly(t *testing.T) {
	_, err := Commit(filepath.Join(t.TempDir(), "no-such-dir", "state.json"), State{}, &Admitted{Version: 1})
	assert.Error(t, err)
	// the target is a non-empty directory: the rename must fail, and the state must not claim success
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	require.NoError(t, os.Mkdir(target, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(target, "x"), []byte("x"), 0o600))
	_, err = Commit(target, State{}, &Admitted{Version: 1})
	assert.Error(t, err)
}

func TestLockAllowsOneAdmissionPerStateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	unlock, err := Lock(path)
	require.NoError(t, err)
	_, err = Lock(path)
	require.Error(t, err, "a second instance on the same state file")
	assert.Contains(t, err.Error(), "one relay instance per state file")
	unlock()
	unlock2, err := Lock(path)
	require.NoError(t, err, "released")
	unlock2()
	_, err = Lock(filepath.Join(t.TempDir(), "absent", "state.json"))
	assert.Error(t, err)
}

// TB-02: the durability sequence is asserted, not assumed. The temp file is flushed BEFORE the rename (the final path
// still holds the old state), the directory AFTER it (the final path holds the new state); a failed flush is an error.
func TestCommitFlushesTheFileBeforeAndTheDirectoryAfterTheRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	var events []string
	saved := syncFile
	t.Cleanup(func() { syncFile = saved })
	syncFile = func(f *os.File) error {
		st, err := f.Stat()
		require.NoError(t, err)
		_, final := os.Stat(path)
		events = append(events, map[bool]string{true: "dir", false: "file"}[st.IsDir()]+map[bool]string{true: "+final", false: "-final"}[final == nil])
		return f.Sync()
	}
	_, err := Commit(path, State{}, &Admitted{Version: 3, Digest: pd})
	require.NoError(t, err)
	assert.Equal(t, []string{"file-final", "dir+final"}, events)

	events = nil
	_, err = Commit(path, State{HighWatermark: 3, HighWatermarkDigest: pd}, &Admitted{Version: 3, Digest: pd})
	require.NoError(t, err)
	assert.Empty(t, events, "nothing to persist: nothing is flushed")
}

func TestAFailedFlushIsNotSwallowed(t *testing.T) {
	saved := syncFile
	t.Cleanup(func() { syncFile = saved })
	for name, failOn := range map[string]bool{"file": false, "dir": true} {
		t.Run(name, func(t *testing.T) {
			syncFile = func(f *os.File) error {
				st, _ := f.Stat()
				if st.IsDir() == failOn {
					return os.ErrInvalid
				}
				return nil
			}
			_, err := Commit(filepath.Join(t.TempDir(), "state.json"), State{}, &Admitted{Version: 1, Digest: pd})
			require.Error(t, err)
			assert.ErrorIs(t, err, os.ErrInvalid)
		})
	}
}

func TestSyncDirReportsAMissingDirectory(t *testing.T) {
	assert.NoError(t, syncDir(t.TempDir()))
	assert.Error(t, syncDir(filepath.Join(t.TempDir(), "absent")))
}

func TestClip(t *testing.T) {
	assert.Equal(t, strings.Repeat("r", 64), clip(strings.Repeat("r", 64)))
	assert.Equal(t, strings.Repeat("r", 64)+"...", clip(strings.Repeat("r", 65)))
}
