package hostsign

// Boundary and rule tests found by the CI mutation ratchet (the seeded faults of the job had not covered them):
// every limit at its exact edge, every request-shape rule on its own, the record fields, the target encoding.
// They check the contract (SS-03/SS-05, record wire), not the mutants.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errProfile is a profile whose canonicalisation fails.
type errProfile struct{ testProfile }

func (errProfile) Prepare(PrepRequest, string) (*Prepared, error) { return nil, errors.New("boom") }

// sizedDoer answers with a body of exactly n bytes, with or without a Content-Length.
type sizedDoer struct {
	n             int
	contentLength bool
}

func (d sizedDoer) Do(*http.Request) (*http.Response, error) {
	cl := int64(-1)
	if d.contentLength {
		cl = int64(d.n)
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", d.n))), ContentLength: cl}, nil
}

func (r *rig) handle(t *testing.T, raw []byte) (map[string]any, string) {
	t.Helper()
	out := r.e.Handle(context.Background(), raw)
	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m), string(out))
	return m, string(out)
}

func TestRequestShapeRulesOneByOne(t *testing.T) {
	bad := map[string]func(m map[string]any){
		"v is 0":                       func(m map[string]any) { m["v"] = 0 },
		"v is 2":                       func(m map[string]any) { m["v"] = 2 },
		"signing_profile malformed":    func(m map[string]any) { m["signing_profile"] = "NOT A PROFILE" },
		"key_ref malformed":            func(m map[string]any) { m["key_ref"] = "no slash" },
		"method outside the enum":      func(m map[string]any) { sub(m, "request")["method"] = "TRACE" },
		"host malformed":               func(m map[string]any) { sub(m, "request")["host"] = "Bad Host" },
		"path malformed":               func(m map[string]any) { sub(m, "request")["path"] = "no-leading-slash" },
		"no intent":                    func(m map[string]any) { delete(m, "intent") },
		"intent operation malformed":   func(m map[string]any) { sub(m, "intent")["operation"] = "a b" },
		"intent resource_id malformed": func(m map[string]any) { sub(m, "intent")["resource_id"] = "a b" },
		"binding digest malformed":     func(m map[string]any) { sub(m, "intent")["binding_digest"] = "sha256:zz" },
		"trace_id 129 bytes":           func(m map[string]any) { m["trace_id"] = strings.Repeat("a", 129) },
	}
	for name, mut := range bad {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, nil)
			req := baseReq()
			mut(req)
			raw, _ := json.Marshal(req)
			m, out := r.handle(t, raw)
			assert.Equal(t, CodeRequestInvalid, m["code"], out)
			r.assertNothingLeft(t)
		})
	}
	t.Run("trace_id of exactly 128 bytes is accepted", func(t *testing.T) {
		r := newRig(t, nil)
		req := baseReq()
		req["trace_id"] = strings.Repeat("a", 128)
		resp, _ := r.do(t, req)
		assert.Equal(t, "completed", resp.Outcome)
	})
}

// The raw request is capped at 2*MaxRequestBytes+64KiB. The edge itself is accepted, one more byte is not.
func TestRawRequestSizeEdge(t *testing.T) {
	limit := 2*refPolicy().Limits.MaxRequestBytes + (64 << 10)
	pad := func(n int) []byte {
		raw, _ := json.Marshal(baseReq())
		require.LessOrEqual(t, len(raw), n)
		return append(raw, []byte(strings.Repeat(" ", n-len(raw)))...)
	}
	r := newRig(t, nil)
	m, out := r.handle(t, pad(limit))
	assert.Equal(t, "completed", m["outcome"], out)
	r2 := newRig(t, nil)
	m, out = r2.handle(t, pad(limit+1))
	assert.Equal(t, CodeRequestInvalid, m["code"], out)
	assert.Contains(t, out, "request too large")
}

func TestRequestBodyEdgeAndTheRecordedBodyDigest(t *testing.T) {
	max := refPolicy().Limits.MaxRequestBytes
	with := func(n int) map[string]any {
		req := baseReq()
		sub(req, "request")["body"] = map[string]any{"base64": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("b", n)))}
		return req
	}
	r := newRig(t, nil)
	resp, _ := r.do(t, with(max))
	assert.Equal(t, "completed", resp.Outcome, "a body of exactly the cap is accepted")
	recs := r.e.Log().Records()
	require.Len(t, recs, 1)
	require.NotNil(t, recs[0].RequestBodySHA256, "a request with a body records its digest")
	assert.Equal(t, sha([]byte(strings.Repeat("b", max))), *recs[0].RequestBodySHA256)

	r2 := newRig(t, nil)
	resp, _ = r2.do(t, with(max+1))
	require.Equal(t, "denied", resp.Outcome)
	assert.Equal(t, CodeBodyTooLarge, resp.Denial.Code)
	r2.assertNothingLeft(t)

	r3 := newRig(t, nil)
	r3.do(t, baseReq())
	recs = r3.e.Log().Records()
	require.Len(t, recs, 1)
	assert.Nil(t, recs[0].RequestBodySHA256, "a request without a body records no body digest")
}

func TestProfileCanonicalisationErrorIsASignerError(t *testing.T) {
	r := newRig(t, func(c *Config) { c.Profiles = []Profile{errProfile{}} })
	resp, _ := r.do(t, baseReq())
	require.Equal(t, "failed", resp.Outcome)
	assert.Equal(t, CodeSignerError, resp.Failure.Code)
	assert.Equal(t, "profile: boom", resp.Failure.Detail)
	assert.Zero(t, r.sg.calls, "nothing is signed when the profile can not canonicalise")
	assert.Zero(t, r.dc.calls)
}

func TestObservationOnlyLetsGetAndHeadThroughAndNothingElse(t *testing.T) {
	for _, m := range []string{"GET", "HEAD"} {
		t.Run(m, func(t *testing.T) {
			r := newRig(t, func(c *Config) { c.Policy.ObservationOnly = true })
			req := baseReq()
			sub(req, "request")["method"] = m
			resp, _ := r.do(t, req)
			assert.Equal(t, "completed", resp.Outcome)
		})
	}
	r := newRig(t, func(c *Config) { c.Policy.ObservationOnly = true })
	req := baseReq()
	sub(req, "request")["method"] = "DELETE"
	sub(req, "intent")["operation"] = "destroy"
	resp, _ := r.do(t, req)
	assert.Equal(t, "denied", resp.Outcome)
	assert.Zero(t, r.sg.calls)
	assert.Zero(t, r.dc.calls)
}

func TestResponseSizeEdges(t *testing.T) {
	max := refPolicy().Limits.MaxResponseBytes
	for _, cl := range []bool{true, false} {
		name := map[bool]string{true: "with Content-Length", false: "without Content-Length"}[cl]
		t.Run(name+": exactly the cap", func(t *testing.T) {
			r := newRig(t, func(c *Config) { c.Doer = sizedDoer{n: max, contentLength: cl} })
			resp, _ := r.do(t, baseReq())
			assert.Equal(t, "completed", resp.Outcome)
		})
		t.Run(name+": one byte over", func(t *testing.T) {
			r := newRig(t, func(c *Config) { c.Doer = sizedDoer{n: max + 1, contentLength: cl} })
			resp, _ := r.do(t, baseReq())
			require.Equal(t, "failed", resp.Outcome)
			assert.Equal(t, CodeResponseTooLarge, resp.Failure.Code)
		})
	}
}

func TestEffectiveLimitsEdges(t *testing.T) {
	op := Limits{TimeoutMS: 10, MaxRequestBytes: 20, MaxResponseBytes: 30}
	got, err := effectiveLimits(op, op)
	require.NoError(t, err, "a profile limit equal to the cap is fine")
	assert.Equal(t, op, got)
	got, err = effectiveLimits(op, Limits{})
	require.NoError(t, err)
	assert.Equal(t, op, got, "a zero profile field does not narrow")
	got, err = effectiveLimits(op, Limits{TimeoutMS: 5, MaxRequestBytes: 6, MaxResponseBytes: 7})
	require.NoError(t, err)
	assert.Equal(t, Limits{TimeoutMS: 5, MaxRequestBytes: 6, MaxResponseBytes: 7}, got, "a narrower profile limit wins")
	for name, p := range map[string]Limits{"timeout": {TimeoutMS: 11}, "request": {MaxRequestBytes: 21}, "response": {MaxResponseBytes: 31}} {
		_, err = effectiveLimits(op, p)
		assert.ErrorIs(t, err, ErrLimitExceedsOperatorCap, name)
	}
}

func TestOperatorLimitsMustBePositive(t *testing.T) {
	require.NoError(t, refPolicy().Validate())
	for name, mut := range map[string]func(p *Policy, v int){
		"timeout":  func(p *Policy, v int) { p.Limits.TimeoutMS = v },
		"request":  func(p *Policy, v int) { p.Limits.MaxRequestBytes = v },
		"response": func(p *Policy, v int) { p.Limits.MaxResponseBytes = v },
	} {
		for _, v := range []int{0, -1} {
			p := refPolicy()
			mut(&p, v)
			assert.Error(t, p.Validate(), "%s = %d", name, v)
		}
		p := refPolicy()
		mut(&p, 1)
		assert.NoError(t, p.Validate(), "%s = 1 is the smallest valid value", name)
	}
}

func TestAsciiLineEdges(t *testing.T) {
	s, err := asciiLine("a\x7fb")
	require.NoError(t, err, "DEL (0x7f) is ASCII")
	assert.Equal(t, "a\x7fb", s)
	_, err = asciiLine("a\x80b")
	assert.ErrorIs(t, err, ErrBadValue)
	_, err = asciiLine("a\nb")
	assert.ErrorIs(t, err, ErrBadValue)
	_, err = asciiLine("a\rb")
	assert.ErrorIs(t, err, ErrBadValue)
}

func TestInteractionIdsAndSequence(t *testing.T) {
	l := NewLog()
	for want := 0; want < 3; want++ {
		rec, _, err := l.Append(Record{Method: "GET", Host: "h"})
		require.NoError(t, err)
		assert.Equal(t, want, rec.Seq)
		assert.Equal(t, []string{"i-0001", "i-0002", "i-0003"}[want], rec.InteractionID)
	}
}

func TestTargetEncodesTheQueryCanonically(t *testing.T) {
	assert.Equal(t, "/p", PrepRequest{Path: "/p"}.Target())
	assert.Equal(t, "/p", PrepRequest{Path: "/p", Query: map[string]string{}}.Target())
	assert.Equal(t, "/p?a=1&b=2+3", PrepRequest{Path: "/p", Query: map[string]string{"b": "2 3", "a": "1"}}.Target(), "keys sorted, values escaped")
	assert.Equal(t, "/p?a%26b=c%3Dd", PrepRequest{Path: "/p", Query: map[string]string{"a&b": "c=d"}}.Target(), "keys and values are both escaped")
}
