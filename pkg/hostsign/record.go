package hostsign

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// RecordIntent is the intent a host interaction record carries.
type RecordIntent struct {
	Operation     string `json:"operation"`
	ResourceID    string `json:"resource_id"`
	BindingDigest string `json:"binding_digest"`
}

// Record is one host-interaction record (wire: host-interaction-record.schema.json, contract SS-04).
// The host writes it, never the guest; every call is recorded, denied and failed ones too.
type Record struct {
	InteractionID        string       `json:"interaction_id"`
	Seq                  int          `json:"seq"`
	Decision             string       `json:"decision"` // allowed | denied | failed
	Code                 *string      `json:"code"`
	Method               string       `json:"method"`
	Host                 string       `json:"host"`
	PathSHA256           string       `json:"path_sha256"`
	RequestBodySHA256    *string      `json:"request_body_sha256"`
	Status               *int         `json:"status"`
	ResponseBodySHA256   *string      `json:"response_body_sha256"`
	PolicyDigest         string       `json:"policy_digest"`
	ProfileDigest        string       `json:"profile_digest"`
	SigningProfileDigest string       `json:"signing_profile_digest"`
	Intent               RecordIntent `json:"intent"`
}

// ErrSeqGapOrOrder is returned by InteractionsDigest for a list that is not contiguous from 0.
var ErrSeqGapOrOrder = errors.New("SEQ_GAP_OR_ORDER")

// ErrBadValue is returned by RecordBytes for a value with a line break or a non-ASCII byte.
var ErrBadValue = errors.New("BAD_VALUE")

// recVal is a helper of the cic-flow.send host capability.
func recVal(s *string) string {
	if s == nil {
		return "null"
	}
	return *s
}

// asciiLine is a helper of the cic-flow.send host capability.
func asciiLine(s string) (string, error) {
	if strings.ContainsAny(s, "\n\r") {
		return "", ErrBadValue
	}
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7f {
			return "", ErrBadValue
		}
	}
	return s, nil
}

// RecordBytes is the fixed-order `key=value` line form of a record whose sha256 is the record digest.
func RecordBytes(r Record) ([]byte, error) {
	status := "null"
	if r.Status != nil {
		status = strconv.Itoa(*r.Status)
	}
	pairs := [][2]string{
		{"interaction_id", r.InteractionID}, {"seq", strconv.Itoa(r.Seq)}, {"decision", r.Decision},
		{"code", recVal(r.Code)}, {"method", r.Method}, {"host", r.Host}, {"path_sha256", r.PathSHA256},
		{"request_body_sha256", recVal(r.RequestBodySHA256)}, {"status", status},
		{"response_body_sha256", recVal(r.ResponseBodySHA256)}, {"policy_digest", r.PolicyDigest},
		{"profile_digest", r.ProfileDigest}, {"signing_profile_digest", r.SigningProfileDigest},
		{"intent.operation", r.Intent.Operation}, {"intent.resource_id", r.Intent.ResourceID},
		{"intent.binding_digest", r.Intent.BindingDigest},
	}
	var b strings.Builder
	for _, kv := range pairs {
		v, err := asciiLine(kv[1])
		if err != nil {
			return nil, ErrBadValue
		}
		b.WriteString(kv[0] + "=" + v + "\n")
	}
	return []byte(b.String()), nil
}

// RecordDigest is sha256:<hex> of RecordBytes.
func RecordDigest(r Record) (string, error) {
	b, err := RecordBytes(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// InteractionsDigest is the list digest (the ProofTrace V2 host_interactions.digest):
// sha256 over "CICHOSTINTv1\ncount=<n>\n" and one "<seq>:sha256:<record digest hex>\n" line per
// record. The sequence must be contiguous from 0, in order.
func InteractionsDigest(records []Record) (string, error) {
	for i, r := range records {
		if r.Seq != i {
			return "", ErrSeqGapOrOrder
		}
	}
	var b strings.Builder
	b.WriteString("CICHOSTINTv1\ncount=" + strconv.Itoa(len(records)) + "\n")
	for _, r := range records {
		d, err := RecordDigest(r)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%d:%s\n", r.Seq, d)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Log is the ordered host-interaction log of one scope (the WP-05 step binding takes it from
// here: Drain returns the records so far and starts a new list at seq 0).
type Log struct {
	mu      sync.Mutex
	records []Record
}

// NewLog returns an empty log.
func NewLog() *Log { return &Log{} }

// Append assigns seq and interaction_id, stores the record and returns it with its digest.
func (l *Log) Append(r Record) (Record, string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r.Seq = len(l.records)
	r.InteractionID = fmt.Sprintf("i-%04d", r.Seq+1)
	d, err := RecordDigest(r)
	if err != nil {
		return Record{}, "", err
	}
	l.records = append(l.records, r)
	return r, d, nil
}

// Records returns a copy of the records so far.
func (l *Log) Records() []Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Record(nil), l.records...)
}

// Drain returns the records so far and resets the log (the next record gets seq 0).
func (l *Log) Drain() []Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.records
	l.records = nil
	return out
}

// Digest is the list digest of the current records.
func (l *Log) Digest() (string, error) { return InteractionsDigest(l.Records()) }
