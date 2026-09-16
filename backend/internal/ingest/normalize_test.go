package ingest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/store"
)

// sampleNow is later on the day the samples were written, so none of them
// falls outside the retention window.
var sampleNow = ts("2025-08-20T13:00:00Z")

// wantSamples is what every file in samples/ must normalize to. A nil Raw
// means the whole record.
var wantSamples = map[string]store.NewEventParams{
	"json/ad_4625.json": {
		Ts: ts("2025-08-20T11:11:11Z"), TenantID: "demoA", Source: "ad",
		Vendor: new("microsoft"), Product: new("windows"),
		EventType: new("LogonFailed"), EventSubtype: new("network"), Action: new("login"),
		SrcIP: addr("203.0.113.77"), UserName: new(`demo\eve`), Host: new("DC01"),
		Tags: []string{TagAuthFailure},
	},
	"json/api.json": {
		Ts: ts("2025-08-20T07:20:00Z"), TenantID: "demoA", Source: "api",
		EventType: new("app_login_failed"), Action: new("login"),
		SrcIP: addr("203.0.113.7"), UserName: new("alice"),
		Tags: []string{TagAuthFailure},
	},
	"json/aws_cloudtrail.json": {
		Ts: ts("2025-08-20T09:10:00Z"), TenantID: "demoB", Source: "aws",
		Vendor: new("aws"), Product: new("cloudtrail"),
		EventType: new("CreateUser"), Action: new("create"), UserName: new("admin"),
		CloudAccountID: new("123456789012"), CloudRegion: new("ap-southeast-1"), CloudService: new("iam"),
		Raw:  []byte(`{"eventName": "CreateUser", "requestParameters": {"userName": "temp-user"}}`),
		Tags: []string{},
	},
	"json/crowdstrike.json": {
		Ts: ts("2025-08-20T08:00:00Z"), TenantID: "demoA", Source: "crowdstrike",
		Vendor: new("crowdstrike"), Product: new("falcon"),
		EventType: new("malware_detected"), Severity: ptr[int16](8), Action: new("quarantine"),
		Host: new("WIN10-01"), Process: new("powershell.exe"),
		Tags: []string{},
	},
	"json/m365_audit.json": {
		Ts: ts("2025-08-20T10:05:00Z"), TenantID: "demoB", Source: "m365",
		Vendor: new("microsoft"), Product: new("m365"),
		EventType: new("UserLoggedIn"), Action: new("login"),
		SrcIP: addr("198.51.100.23"), UserName: new("bob@demo.local"),
		Tags: []string{TagAuthSuccess},
	},
	// Syslog carries no trustworthy time, so these take the receipt time.
	"syslog/firewall.log:1": {
		Ts: sampleNow, TenantID: "demoA", Source: "firewall",
		Vendor: new("demo"), Product: new("ngfw"),
		EventType: new("traffic"), Severity: ptr[int16](2), Action: new("deny"),
		SrcIP: addr("10.0.1.10"), SrcPort: ptr[int32](5353),
		DstIP: addr("8.8.8.8"), DstPort: ptr[int32](53), Protocol: new("udp"),
		Host: new("fw01"), RuleName: new("Block-DNS"),
		Raw:  messageRaw(firewallLine),
		Tags: []string{},
	},
	"syslog/network.log:1": {
		Ts: sampleNow, TenantID: "demoA", Source: "network",
		EventType: new("link-down"), EventSubtype: new("carrier-loss"), Severity: ptr[int16](2),
		Host: new("r1"),
		Raw:  messageRaw(routerLine),
		Tags: []string{},
	},
}

func TestNormalizeSamples(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range loadSamples(t) {
		seen[s.name] = true
		t.Run(s.name, func(t *testing.T) {
			want, ok := wantSamples[s.name]
			if !ok {
				t.Fatalf("no expectation for %s: add one to wantSamples", s.name)
			}
			if want.Raw == nil {
				want.Raw = s.record
			}
			got, err := at(sampleNow).Normalize(s.record, Defaults{})
			if err != nil {
				t.Fatalf("rejected: %v", err)
			}
			assertEvent(t, got, want)
		})
	}
	for name := range wantSamples {
		if !seen[name] {
			t.Errorf("expectation for %s has no sample", name)
		}
	}
}

// Replayed today, the 2025 JSON samples are far outside retention. They must
// still be stored and findable, under the time they arrived.
func TestNormalizeSamplesOutsideRetention(t *testing.T) {
	now := ts("2026-09-16T12:00:00Z")
	for _, s := range loadSamples(t) {
		t.Run(s.name, func(t *testing.T) {
			got, err := at(now).Normalize(s.record, Defaults{})
			if err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if !got.Ts.Equal(now) {
				t.Errorf("ts = %v, want receipt time %v", got.Ts, now)
			}
			// Syslog samples already use the receipt time, so nothing is rebased.
			wantTag := strings.HasPrefix(s.name, "json/")
			if slices.Contains(got.Tags, TagRebasedTime) != wantTag {
				t.Errorf("tags = %q, want %s present: %v", got.Tags, TagRebasedTime, wantTag)
			}
		})
	}
}

func TestNormalizeRejects(t *testing.T) {
	for _, tc := range []struct{ name, record, code string }{
		{"not JSON", `tenant=demoA`, "invalid_json"},
		{"array", `[{"tenant":"demoA","source":"api"}]`, "invalid_json"},
		{"null", `null`, "invalid_json"},
		{"missing tenant", `{"source":"api"}`, "missing_tenant"},
		{"blank tenant", `{"tenant":"  ","source":"api"}`, "missing_tenant"},
		{"tenant with a space", `{"tenant":"a b","source":"api"}`, "invalid_tenant"},
		{"tenant not a string", `{"tenant":5,"source":"api"}`, "invalid_tenant"},
		{"unknown source", `{"tenant":"demoA","source":"syslog"}`, "unknown_source"},
		{"source not a string", `{"tenant":"demoA","source":["api"]}`, "unknown_source"},
		{"no source and no message", `{"tenant":"demoA","event_type":"x"}`, "missing_source"},
		{"no source and message not a string", `{"tenant":"demoA","message":{"text":"x"}}`, "missing_source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := at(sampleNow).Normalize([]byte(tc.record), Defaults{})
			if err == nil {
				t.Fatalf("accepted, want %s", tc.code)
			}
			if got := errors.CodeOf(err); got != tc.code {
				t.Errorf("code = %s, want %s (%v)", got, tc.code, err)
			}
		})
	}
}

// A value that cannot be normalized costs that field, never the event.
func TestNormalizeInvalidFieldsKept(t *testing.T) {
	for _, tc := range []struct {
		name, record, tag string
		ok                func(store.NewEventParams) bool
	}{
		{"severity not a number", `{"severity":"high"}`, "invalid:severity",
			func(e store.NewEventParams) bool { return e.Severity == nil }},
		{"severity out of range", `{"severity":11}`, "invalid:severity",
			func(e store.NewEventParams) bool { return e.Severity == nil }},
		{"severity not whole", `{"severity":2.5}`, "invalid:severity",
			func(e store.NewEventParams) bool { return e.Severity == nil }},
		{"port out of range", `{"src_port":70000}`, "invalid:src_port",
			func(e store.NewEventParams) bool { return e.SrcPort == nil }},
		{"bad ip alias", `{"ip":"nope"}`, "invalid:ip",
			func(e store.NewEventParams) bool { return e.SrcIP == nil }},
		{"bad src_ip falls back to ip", `{"src_ip":"nope","ip":"10.0.0.1"}`, "invalid:src_ip",
			func(e store.NewEventParams) bool { return e.SrcIP != nil && e.SrcIP.String() == "10.0.0.1" }},
		{"user as an object", `{"user":{"name":"alice"}}`, "invalid:user",
			func(e store.NewEventParams) bool { return e.UserName == nil }},
		{"cloud not an object", `{"cloud":"aws"}`, "invalid:cloud",
			func(e store.NewEventParams) bool { return e.CloudService == nil }},
		{"cloud field not a scalar", `{"cloud":{"region":["a"],"service":"iam"}}`, "invalid:cloud.region",
			func(e store.NewEventParams) bool { return e.CloudRegion == nil && deref(e.CloudService) == "iam" }},
		{"tags not an array", `{"_tags":"x"}`, "invalid:_tags",
			func(e store.NewEventParams) bool { return len(e.Tags) == 1 }},
		{"raw not an object", `{"raw":5}`, "invalid:raw",
			func(e store.NewEventParams) bool { return bytes.Contains(e.Raw, []byte(`"tenant"`)) }},
		{"bad port in a syslog line", `{"message":"<134>Aug 20 12:44:56 fw01 action=deny spt=abc"}`, "invalid:spt",
			func(e store.NewEventParams) bool { return e.SrcPort == nil && deref(e.Action) == "deny" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := at(sampleNow).Normalize(testRecord(tc.record), Defaults{})
			if err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if !slices.Contains(got.Tags, tc.tag) {
				t.Errorf("tags = %q, want %s", got.Tags, tc.tag)
			}
			if !tc.ok(got) {
				t.Errorf("unexpected event: %s", dump(got))
			}
		})
	}
}

func TestNormalizeCoercion(t *testing.T) {
	for _, tc := range []struct {
		name, record string
		ok           func(store.NewEventParams) bool
	}{
		{"numeric string severity", `{"severity":"7"}`,
			func(e store.NewEventParams) bool { return e.Severity != nil && *e.Severity == 7 }},
		{"whole float severity", `{"severity":8.0}`,
			func(e store.NewEventParams) bool { return e.Severity != nil && *e.Severity == 8 }},
		{"numeric string port", `{"dst_port":"443"}`,
			func(e store.NewEventParams) bool { return e.DstPort != nil && *e.DstPort == 443 }},
		{"number as text", `{"rule_id":1234}`,
			func(e store.NewEventParams) bool { return deref(e.RuleID) == "1234" }},
		{"protocol lowercased", `{"protocol":"TCP"}`,
			func(e store.NewEventParams) bool { return deref(e.Protocol) == "tcp" }},
		{"IPv4-mapped IPv6 unmapped", `{"src_ip":"::ffff:10.0.0.1"}`,
			func(e store.NewEventParams) bool { return e.SrcIP != nil && e.SrcIP.String() == "10.0.0.1" }},
		{"IPv6 kept", `{"dst_ip":"2001:db8::1"}`,
			func(e store.NewEventParams) bool { return e.DstIP != nil && e.DstIP.String() == "2001:db8::1" }},
		{"action synonym", `{"action":"Blocked"}`,
			func(e store.NewEventParams) bool { return deref(e.Action) == "deny" }},
		{"unknown action lowercased", `{"action":"Quarantine"}`,
			func(e store.NewEventParams) bool { return deref(e.Action) == "quarantine" }},
		{"empty string is absent", `{"user":"  "}`,
			func(e store.NewEventParams) bool { return e.UserName == nil }},
		{"null is absent", `{"host":null}`,
			func(e store.NewEventParams) bool { return e.Host == nil }},
		{"sender tags first, trimmed and deduplicated", `{"_tags":[" vip ","","vip","b"],"event_type":"user_login"}`,
			func(e store.NewEventParams) bool {
				return slices.Equal(e.Tags, []string{"vip", "b", TagAuthSuccess})
			}},
		{"message is not parsed for other sources", `{"source":"api","message":"src=10.0.0.1"}`,
			func(e store.NewEventParams) bool {
				return e.SrcIP == nil && bytes.Contains(e.Raw, []byte(`"source"`))
			}},
		{"collector metadata left out of raw", `{"peer_ip":"10.9.9.9","input":"file","vendor":"x"}`,
			func(e store.NewEventParams) bool {
				return !bytes.Contains(e.Raw, []byte("peer_ip")) && !bytes.Contains(e.Raw, []byte("input")) &&
					bytes.Contains(e.Raw, []byte(`"vendor"`))
			}},
		{"peer address when a syslog line names no host", `{"message":"link flapped","peer_ip":"10.9.9.9"}`,
			func(e store.NewEventParams) bool { return deref(e.Host) == "10.9.9.9" }},
		{"key=value severity beats PRI", `{"message":"<134>Aug 20 12:44:56 fw01 action=deny severity=9"}`,
			func(e store.NewEventParams) bool { return e.Severity != nil && *e.Severity == 9 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := at(sampleNow).Normalize(testRecord(tc.record), Defaults{})
			if err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if !tc.ok(got) {
				t.Errorf("unexpected event: %s", dump(got))
			}
			for _, tag := range got.Tags {
				if strings.HasPrefix(tag, "invalid:") {
					t.Errorf("unexpected %s", tag)
				}
			}
		})
	}
}

func TestNormalizeTimestamp(t *testing.T) {
	now := ts("2026-09-16T12:00:00Z")
	for _, tc := range []struct {
		name, record string
		want         time.Time
		tag          string
	}{
		{"absent", `{}`, now, ""},
		{"absent, with the collector's receipt time", `{"received_at":"2026-09-16T11:59:00Z"}`, ts("2026-09-16T11:59:00Z"), ""},
		{"unparseable", `{"@timestamp":"yesterday"}`, now, "invalid:timestamp"},
		{"not a string", `{"@timestamp":1789560000}`, now, "invalid:timestamp"},
		{"older than retention", `{"@timestamp":"2025-08-20T07:20:00Z"}`, now, TagRebasedTime},
		{"over an hour ahead", `{"@timestamp":"2026-09-16T14:00:00Z"}`, now, TagRebasedTime},
		{"a little ahead", `{"@timestamp":"2026-09-16T12:30:00Z"}`, ts("2026-09-16T12:30:00Z"), ""},
		{"offset converted to UTC", `{"@timestamp":"2026-09-15T10:00:00+07:00"}`, ts("2026-09-15T03:00:00Z"), ""},
		{"RFC 3164 header ignored", `{"message":"<13>Sep 16 16:48:31 tofu mairuu: hi"}`, now, ""},
		{"RFC 5424 header used", fmt.Sprintf(`{"message":%q}`, loggerLine), ts("2026-09-16T09:48:31.447979Z"), ""},
		{"@timestamp beats the header", `{"message":"<13>1 2026-09-16T08:00:00Z h a - - - x","@timestamp":"2026-09-16T10:00:00Z"}`, ts("2026-09-16T10:00:00Z"), ""},
		{"unparseable @timestamp falls back to the header", `{"message":"<13>1 2026-09-16T08:00:00Z h a - - - x","@timestamp":"soon"}`, ts("2026-09-16T08:00:00Z"), "invalid:timestamp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := at(now).Normalize(testRecord(tc.record), Defaults{})
			if err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if !got.Ts.Equal(tc.want) || got.Ts.Location() != time.UTC {
				t.Errorf("ts = %v, want %v in UTC", got.Ts, tc.want)
			}
			switch {
			case tc.tag == "" && len(got.Tags) != 0:
				t.Errorf("tags = %q, want none", got.Tags)
			case tc.tag != "" && !slices.Equal(got.Tags, []string{tc.tag}):
				t.Errorf("tags = %q, want [%s]", got.Tags, tc.tag)
			}
		})
	}
}

func TestNormalizeDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, record   string
		defaults       Defaults
		tenant, source string
	}{
		{"defaults fill what the record omits", `{"event_type":"x"}`, Defaults{"demoB", "aws"}, "demoB", "aws"},
		{"the record's own values win", `{"tenant":"demoA","source":"api"}`, Defaults{"demoB", "aws"}, "demoA", "api"},
		{"source is case-insensitive", `{"tenant":"demoA","source":"AWS"}`, Defaults{}, "demoA", "aws"},
		{"plain syslog text is network", `{"message":"hello"}`, Defaults{Tenant: "demoA"}, "demoA", "network"},
		{"firewall keys mean firewall", `{"message":"action=allow src=10.0.0.1"}`, Defaults{Tenant: "demoA"}, "demoA", "firewall"},
		{"firewall keys in uppercase", `{"message":"SRC=10.0.0.1 DPT=53"}`, Defaults{Tenant: "demoA"}, "demoA", "firewall"},
		{"default source still takes the syslog path", `{"message":"<134>Aug 20 12:44:56 fw01 hello"}`, Defaults{"demoA", "network"}, "demoA", "network"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := at(sampleNow).Normalize([]byte(tc.record), tc.defaults)
			if err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if got.TenantID != tc.tenant || got.Source != tc.source {
				t.Errorf("got %s/%s, want %s/%s", got.TenantID, got.Source, tc.tenant, tc.source)
			}
		})
	}
}

func TestAuthClassification(t *testing.T) {
	for _, tc := range []struct {
		name, record, action, tag string
	}{
		{"ad 4624", `{"source":"ad","event_id":4624,"event_type":"LogonSuccess"}`, "login", TagAuthSuccess},
		{"ad 4625", `{"source":"ad","event_id":4625,"event_type":"LogonFailed"}`, "login", TagAuthFailure},
		{"ad 4634", `{"source":"ad","event_id":4634}`, "logout", ""},
		{"ad other event", `{"source":"ad","event_id":4720,"event_type":"UserCreated"}`, "", ""},
		{"m365 failed login", `{"source":"m365","event_type":"UserLoginFailed"}`, "login", TagAuthFailure},
		{"m365 status overrides the operation", `{"source":"m365","event_type":"UserLoggedIn","status":"Failed"}`, "login", TagAuthFailure},
		{"m365 other operation", `{"source":"m365","event_type":"FileAccessed","status":"Success"}`, "", ""},
		{"api failed login", `{"source":"api","event_type":"app_login_failed"}`, "login", TagAuthFailure},
		{"api login", `{"source":"api","event_type":"user_login"}`, "login", TagAuthSuccess},
		{"api sign-in with a failure outcome", `{"source":"api","event_type":"SignIn","outcome":"failure"}`, "login", TagAuthFailure},
		{"api logout", `{"source":"api","event_type":"user_logout"}`, "logout", ""},
		{"api unrelated", `{"source":"api","event_type":"order_created"}`, "", ""},
		{"aws console login", `{"source":"aws","event_type":"ConsoleLogin"}`, "login", TagAuthSuccess},
		{"aws console login failure", `{"source":"aws","raw":{"eventName":"ConsoleLogin","responseElements":{"ConsoleLogin":"Failure"}}}`, "login", TagAuthFailure},
		{"aws CreateLoginProfile is not a login", `{"source":"aws","event_type":"CreateLoginProfile"}`, "create", ""},
		{"aws DeleteUser", `{"source":"aws","event_type":"DeleteUser"}`, "delete", ""},
		{"sender's action is kept, normalized", `{"source":"api","event_type":"user_login","action":"Logon"}`, "login", TagAuthSuccess},
		{"name heuristic is for api only", `{"source":"crowdstrike","event_type":"login_failed"}`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := at(sampleNow).Normalize(testRecord(tc.record), Defaults{})
			if err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if a := deref(got.Action); a != tc.action {
				t.Errorf("action = %q, want %q", a, tc.action)
			}
			var auth []string
			for _, tag := range got.Tags {
				if strings.HasPrefix(tag, "auth_") {
					auth = append(auth, tag)
				}
			}
			if want := []string{tc.tag}; tc.tag == "" && auth != nil || tc.tag != "" && !slices.Equal(auth, want) {
				t.Errorf("auth tags = %q, want %q", auth, tc.tag)
			}
		})
	}
}

type sample struct {
	name   string
	record []byte
}

// loadSamples returns every sample as an ingest path delivers it: JSON files
// as they are, and each syslog line wrapped the way the collector forwards it.
func loadSamples(t *testing.T) []sample {
	t.Helper()
	jsonFiles, _ := filepath.Glob("../../../samples/json/*.json")
	logFiles, _ := filepath.Glob("../../../samples/syslog/*.log")
	if len(jsonFiles) == 0 || len(logFiles) == 0 {
		t.Fatal("samples not found")
	}

	var out []sample
	for _, p := range jsonFiles {
		out = append(out, sample{"json/" + filepath.Base(p), readFile(t, p)})
	}
	for _, p := range logFiles {
		for i, line := range strings.Split(string(readFile(t, p)), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			record, _ := json.Marshal(map[string]string{"tenant": "demoA", "message": line})
			out = append(out, sample{fmt.Sprintf("syslog/%s:%d", filepath.Base(p), i+1), record})
		}
	}
	return out
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// assertEvent compares raw as JSON and every other field exactly.
func assertEvent(t *testing.T, got, want store.NewEventParams) {
	t.Helper()
	var gotRaw, wantRaw any
	if err := json.Unmarshal(got.Raw, &gotRaw); err != nil {
		t.Fatalf("raw is not JSON: %v", err)
	}
	if err := json.Unmarshal(want.Raw, &wantRaw); err != nil {
		t.Fatalf("expected raw is not JSON: %v", err)
	}
	if !reflect.DeepEqual(gotRaw, wantRaw) {
		t.Errorf("raw\n got: %s\nwant: %s", got.Raw, want.Raw)
	}
	got.Raw, want.Raw = nil, nil
	if g, w := dump(got), dump(want); g != w {
		t.Errorf("event\n got: %s\nwant: %s", g, w)
	}
}

// dump renders an event with its pointers followed, for comparison and for
// readable failures.
func dump(e store.NewEventParams) string {
	b, _ := json.MarshalIndent(e, "", "  ")
	return string(b)
}

// testRecord completes a test record: tenant demoA unless it names one, and
// source api unless it names one or carries a syslog message.
func testRecord(record string) []byte {
	b := []byte(record)
	if !strings.Contains(record, `"tenant"`) {
		b = addKey(b, `"tenant":"demoA"`)
	}
	if !strings.Contains(record, `"source"`) && !strings.Contains(record, `"message"`) {
		b = addKey(b, `"source":"api"`)
	}
	return b
}

func addKey(record []byte, kv string) []byte {
	rest := bytes.TrimPrefix(record, []byte("{"))
	if !bytes.HasPrefix(rest, []byte("}")) {
		kv += ","
	}
	return append([]byte("{"+kv), rest...)
}

func at(now time.Time) Normalizer {
	return Normalizer{Now: func() time.Time { return now }}
}

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func messageRaw(line string) []byte {
	b, _ := json.Marshal(map[string]string{"message": line})
	return b
}

func ptr[T any](v T) *T { return &v }

func addr(s string) *netip.Addr {
	a := netip.MustParseAddr(s)
	return &a
}
