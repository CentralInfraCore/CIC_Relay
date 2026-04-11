// Package merkle implements a deterministic binary Merkle tree used for
// computing the final verification hash of a CIC build provenance manifest.
//
// Domain separation:
//
//	Leaf node:  SHA256(0x00 || name || 0x1f || value)
//	Inner node: SHA256(0x01 || left || right)
//
// Odd-count levels duplicate the last element before pairing.
// Empty tree returns SHA256("empty").
package merkle

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

const (
	// NullMarker replaces empty or missing field values to keep the tree
	// structure stable regardless of which fields are populated.
	NullMarker = "∅"

	leafPrefix = byte(0x00)
	nodePrefix = byte(0x01)
	fieldSep   = byte(0x1f) // ASCII unit separator between name and value
)

// LeafHash computes a domain-separated hash for a named leaf.
// An empty value is silently replaced with NullMarker.
func LeafHash(name, value string) []byte {
	if value == "" {
		value = NullMarker
	}
	h := sha256.New()
	h.Write([]byte{leafPrefix})
	h.Write([]byte(name))
	h.Write([]byte{fieldSep})
	h.Write([]byte(value))
	return h.Sum(nil)
}

// NodeHash combines two child hashes into a parent inner-node hash.
func NodeHash(left, right []byte) []byte {
	h := sha256.New()
	h.Write([]byte{nodePrefix})
	h.Write(left)
	h.Write(right)
	return h.Sum(nil)
}

// MerkleRoot computes the root of a binary Merkle tree over items.
// Returns SHA256("empty") when items is empty or nil.
// Odd-count levels duplicate the last element.
func MerkleRoot(items [][]byte) []byte {
	if len(items) == 0 {
		sum := sha256.Sum256([]byte("empty"))
		return sum[:]
	}

	level := make([][]byte, len(items))
	copy(level, items)

	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		next := make([][]byte, len(level)/2)
		for i := 0; i < len(level); i += 2 {
			next[i/2] = NodeHash(level[i], level[i+1])
		}
		level = next
	}
	return level[0]
}

// HexMerkleRoot returns the lowercase hex-encoded Merkle root.
func HexMerkleRoot(items [][]byte) string {
	return hex.EncodeToString(MerkleRoot(items))
}

// SizeLeaf encodes an int64 as a decimal string leaf (no leading zeros).
func SizeLeaf(name string, size int64) []byte {
	return LeafHash(name, strconv.FormatInt(size, 10))
}
