package api_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mairuu/loghub/backend/internal/api/gen"
)

// The ingest contract is permissive on purpose: a vendor sends what it sends,
// and the fields the spec does not name still have to reach the normalizer so
// they can be stored in `raw`. These assert that promise against the real
// sample payloads rather than against a hand-written fixture.

func samples(t *testing.T) map[string][]byte {
	t.Helper()
	paths, err := filepath.Glob("../../../samples/json/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no sample payloads found: %v", err)
	}
	out := make(map[string][]byte, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		out[filepath.Base(p)] = b
	}
	return out
}

// Every sample must survive as an IngestEvent without losing a byte of meaning.
func TestIngestEventPreservesVendorFields(t *testing.T) {
	for name, raw := range samples(t) {
		t.Run(name, func(t *testing.T) {
			var ev gen.IngestEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			var got, want map[string]any
			if err := json.Unmarshal(ev, &got); err != nil {
				t.Fatalf("decode round-tripped event: %v", err)
			}
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatalf("decode sample: %v", err)
			}
			for k, wantV := range want {
				gotV, ok := got[k]
				if !ok {
					t.Errorf("field %q was dropped", k)
					continue
				}
				if toJSON(t, gotV) != toJSON(t, wantV) {
					t.Errorf("field %q changed: got %s, want %s", k, toJSON(t, gotV), toJSON(t, wantV))
				}
			}
		})
	}
}

// The fields that make the case: none of these are named in the spec, and each
// belongs to a different source.
func TestIngestEventKeepsUndeclaredFields(t *testing.T) {
	all := samples(t)
	for _, tc := range []struct {
		file, field string
	}{
		{"ad_4625.json", "logon_type"},
		{"ad_4625.json", "event_id"},
		{"crowdstrike.json", "sha256"},
		{"m365_audit.json", "workload"},
		{"api.json", "reason"},
	} {
		t.Run(tc.file+"/"+tc.field, func(t *testing.T) {
			raw, ok := all[tc.file]
			if !ok {
				t.Fatalf("sample %s is missing", tc.file)
			}
			var ev gen.IngestEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			var m map[string]any
			if err := json.Unmarshal(ev, &m); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if _, ok := m[tc.field]; !ok {
				t.Errorf("undeclared field %q did not survive ingest", tc.field)
			}
		})
	}
}

// Nested objects are a separate risk from scalars: the AWS sample carries both
// a `cloud` object the spec names and a `raw` object it does not inspect.
func TestIngestEventKeepsNestedObjects(t *testing.T) {
	var ev gen.IngestEvent
	if err := json.Unmarshal(samples(t)["aws_cloudtrail.json"], &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var m struct {
		Cloud struct {
			AccountID string `json:"account_id"`
			Region    string `json:"region"`
		} `json:"cloud"`
		Raw struct {
			RequestParameters map[string]any `json:"requestParameters"`
		} `json:"raw"`
	}
	if err := json.Unmarshal(ev, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.Cloud.AccountID != "123456789012" || m.Cloud.Region != "ap-southeast-1" {
		t.Errorf("cloud context lost: %+v", m.Cloud)
	}
	if _, ok := m.Raw.RequestParameters["userName"]; !ok {
		t.Error("nested raw.requestParameters.userName did not survive")
	}
}

// Source is a closed set backed by a CHECK constraint, and the generated
// Valid() is what ingest will reject unknown sources with.
func TestSourceValid(t *testing.T) {
	for _, s := range []gen.Source{
		gen.SourceFirewall, gen.SourceNetwork, gen.SourceCrowdstrike,
		gen.SourceAws, gen.SourceM365, gen.SourceAd, gen.SourceAPI,
	} {
		if !s.Valid() {
			t.Errorf("%q should be a valid source", s)
		}
	}
	if gen.Source("nope").Valid() {
		t.Error("unknown source should not be valid")
	}
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
