package merkle

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- LeafHash ---

func TestLeafHash_DomainSeparation(t *testing.T) {
	leaf := LeafHash("k", "v")
	node := NodeHash(LeafHash("a", "1"), LeafHash("b", "2"))
	assert.NotEqual(t, leaf, node, "leaf and inner-node hashes must differ by prefix")
}

func TestLeafHash_NullMarker(t *testing.T) {
	empty := LeafHash("field", "")
	explicit := LeafHash("field", NullMarker)
	assert.Equal(t, empty, explicit, "empty string must hash identically to NullMarker")
}

func TestLeafHash_Determinism(t *testing.T) {
	assert.Equal(t, LeafHash("source_ref", "abc123"), LeafHash("source_ref", "abc123"))
}

func TestLeafHash_NameAndValueBothMatter(t *testing.T) {
	assert.NotEqual(t, LeafHash("a", "x"), LeafHash("b", "x"))
	assert.NotEqual(t, LeafHash("a", "x"), LeafHash("a", "y"))
}

// --- NodeHash ---

func TestNodeHash_NotCommutative(t *testing.T) {
	a := LeafHash("a", "1")
	b := LeafHash("b", "2")
	assert.NotEqual(t, NodeHash(a, b), NodeHash(b, a), "node hash must not be commutative")
}

// --- MerkleRoot ---

func TestMerkleRoot_Empty(t *testing.T) {
	expected := sha256.Sum256([]byte("empty"))
	assert.Equal(t, expected[:], MerkleRoot(nil))
	assert.Equal(t, expected[:], MerkleRoot([][]byte{}))
}

func TestMerkleRoot_Single(t *testing.T) {
	item := LeafHash("k", "v")
	assert.Equal(t, item, MerkleRoot([][]byte{item}), "single-item root must equal the item itself")
}

func TestMerkleRoot_Two(t *testing.T) {
	a := LeafHash("a", "1")
	b := LeafHash("b", "2")
	assert.Equal(t, NodeHash(a, b), MerkleRoot([][]byte{a, b}))
}

func TestMerkleRoot_OddCount_DuplicatesLast(t *testing.T) {
	a := LeafHash("a", "1")
	b := LeafHash("b", "2")
	c := LeafHash("c", "3")
	// [a,b,c] → level1: [NODE(a,b), NODE(c,c)] → NODE(NODE(a,b), NODE(c,c))
	expected := NodeHash(NodeHash(a, b), NodeHash(c, c))
	assert.Equal(t, expected, MerkleRoot([][]byte{a, b, c}))
}

func TestMerkleRoot_EvenCount(t *testing.T) {
	a := LeafHash("a", "1")
	b := LeafHash("b", "2")
	c := LeafHash("c", "3")
	d := LeafHash("d", "4")
	expected := NodeHash(NodeHash(a, b), NodeHash(c, d))
	assert.Equal(t, expected, MerkleRoot([][]byte{a, b, c, d}))
}

func TestMerkleRoot_OrderMatters(t *testing.T) {
	a := LeafHash("a", "1")
	b := LeafHash("b", "2")
	assert.NotEqual(t, MerkleRoot([][]byte{a, b}), MerkleRoot([][]byte{b, a}))
}

func TestMerkleRoot_Determinism(t *testing.T) {
	items := [][]byte{
		LeafHash("x", "foo"),
		LeafHash("y", "bar"),
		LeafHash("z", "baz"),
	}
	assert.Equal(t, MerkleRoot(items), MerkleRoot(items))
}

// --- SizeLeaf ---

func TestSizeLeaf_DecimalEncoding(t *testing.T) {
	assert.Equal(t, SizeLeaf("artifact_size", 1234567), LeafHash("artifact_size", "1234567"))
	assert.Equal(t, SizeLeaf("artifact_size", 0), LeafHash("artifact_size", "0"))
}

func TestSizeLeaf_NoLeadingZeros(t *testing.T) {
	// 100 must not encode as "0100" or " 100"
	assert.Equal(t, SizeLeaf("s", 100), LeafHash("s", "100"))
}

// --- BuildVerificationRoot ---

func TestBuildVerificationRoot_Determinism(t *testing.T) {
	m := &VerificationManifest{
		Version: 1,
		Source: SourceFields{
			SourceRef:        "git:abc123",
			SourceTreeDigest: "sha256:aabbccdd",
			CommitSignature:  "sha256:eeff0011",
		},
		Artifact: ArtifactFields{
			ArtifactDigest: "sha256:11223344",
			ArtifactSize:   987654,
		},
		Audit: AuditFields{
			Timestamp: "2026-04-11T12:00:00Z",
		},
	}
	assert.Equal(t, BuildVerificationRoot(m), BuildVerificationRoot(m))
}

func TestBuildVerificationRoot_HexOutput(t *testing.T) {
	root := BuildVerificationRoot(&VerificationManifest{})
	_, err := hex.DecodeString(root)
	require.NoError(t, err, "root must be valid hex")
	assert.Len(t, root, 64, "SHA-256 root must be 64 hex chars")
}

func TestBuildVerificationRoot_SensitiveToLeafChange(t *testing.T) {
	base := &VerificationManifest{Source: SourceFields{SourceRef: "git:abc123"}}
	modified := &VerificationManifest{Source: SourceFields{SourceRef: "git:def456"}}
	assert.NotEqual(t, BuildVerificationRoot(base), BuildVerificationRoot(modified))
}

func TestBuildVerificationRoot_SensitiveToSubtreePosition(t *testing.T) {
	// The same digest value at different positions must yield different roots.
	digest := "sha256:cafebabe"
	inSource := &VerificationManifest{Source: SourceFields{SourceRef: digest}}
	inArtifact := &VerificationManifest{Artifact: ArtifactFields{ArtifactDigest: digest}}
	assert.NotEqual(t, BuildVerificationRoot(inSource), BuildVerificationRoot(inArtifact),
		"same digest in different subtrees must produce different roots")
}

func TestBuildVerificationRoot_EmptyManifestIsStable(t *testing.T) {
	// All-empty manifest must still return a consistent 64-char hex root.
	r1 := BuildVerificationRoot(&VerificationManifest{})
	r2 := BuildVerificationRoot(&VerificationManifest{})
	assert.Equal(t, r1, r2)
	assert.Len(t, r1, 64)
}

func TestBuildVerificationRoot_ArtifactSizeZeroVsNonZero(t *testing.T) {
	zero := &VerificationManifest{Artifact: ArtifactFields{ArtifactSize: 0}}
	nonZero := &VerificationManifest{Artifact: ArtifactFields{ArtifactSize: 1}}
	assert.NotEqual(t, BuildVerificationRoot(zero), BuildVerificationRoot(nonZero))
}
