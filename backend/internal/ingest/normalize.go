// Package ingest turns vendor records into rows of the common event schema.
//
// Normalize is the only way in. It takes one JSON object, the shape every
// ingest path delivers, and never rejects a record over a single bad field:
// a value that cannot be normalized is left empty, the event is tagged
// `invalid:<key>`, and the original stays in raw. Only a record that cannot be
// stored at all is rejected. See docs/architecture.md for the mapping.
package ingest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mairuu/loghub/backend/internal/api/gen"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/store"
)

// DefaultRetention is how long events are kept. serve drops partitions older
// than this, and the normalizer rebases event times older than this.
const DefaultRetention = 7 * 24 * time.Hour

// futureSkew is how far ahead of receipt an event time may be before it is
// treated as a wrong clock.
const futureSkew = time.Hour

// Derived tags that are not about a single field.
const (
	TagAuthSuccess = "auth_success"
	TagAuthFailure = "auth_failure"
	TagRebasedTime = "rebased:timestamp"
)

// maxText is the longest text value a field or tag takes, in bytes. event_type
// and user_name are btree-indexed and tags GIN-indexed, and Postgres cannot
// index an entry over about 2.7 kB.
const maxText = 2048

// Same rule as the CHECK on tenants.id.
var tenantPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// collectorKeys are added to every record by the collector (ADR 0004). They
// are read as fallbacks and are not part of the vendor payload.
var collectorKeys = []string{"received_at", "peer_ip", "input"}

// Defaults apply to records that do not name their own tenant or source.
type Defaults struct {
	Tenant string
	Source string
}

// Validate refuses defaults no record could be stored with, so a caller can
// report them once rather than reject every record that falls back on them.
// The error is errors.Malformed with code invalid_parameter.
func (d Defaults) Validate() error {
	if d.Tenant != "" && !tenantPattern.MatchString(d.Tenant) {
		return errors.Malformed("invalid_parameter", "tenant must be 1 to 64 letters, digits, '-' or '_'")
	}
	if d.Source != "" && !gen.Source(strings.ToLower(d.Source)).Valid() {
		return errors.Malformed("invalid_parameter", fmt.Sprintf("source %q is not a known source category", clip(d.Source)))
	}
	return nil
}

type Normalizer struct {
	// Retention is how old an event may be before its time is replaced with
	// the receipt time. Zero means DefaultRetention.
	Retention time.Duration
	// Now is the receipt time. Nil means time.Now.
	Now func() time.Time
}

// Normalize maps one record to a storable event. The returned error is an
// *errors.Error whose code says why the record was rejected.
func (n Normalizer) Normalize(record []byte, d Defaults) (store.NewEventParams, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(record, &fields); err != nil || fields == nil {
		return store.NewEventParams{}, errors.Malformed("invalid_json", "record is not a JSON object")
	}
	b := &builder{fields: fields}

	tenant, ok := b.identity("tenant", d.Tenant)
	switch {
	case !ok:
		return store.NewEventParams{}, errors.Invalid("invalid_tenant", "tenant must be a string")
	case tenant == "":
		return store.NewEventParams{}, errors.Invalid("missing_tenant", "tenant is required")
	case !tenantPattern.MatchString(tenant):
		return store.NewEventParams{}, errors.Invalid("invalid_tenant", "tenant must be 1 to 64 letters, digits, '-' or '_'")
	}

	source, ok := b.identity("source", d.Source)
	source = strings.ToLower(source)
	switch {
	case !ok:
		return store.NewEventParams{}, errors.Invalid("unknown_source", "source must be a string")
	case source != "" && !gen.Source(source).Valid():
		return store.NewEventParams{}, errors.Invalid("unknown_source", fmt.Sprintf("source %q is not a known source category", clip(source)))
	}

	line, isSyslog := b.syslogLine(source)
	if source == "" && !isSyslog {
		return store.NewEventParams{}, errors.Invalid("missing_source", "source is required unless message holds a syslog line")
	}

	b.ev.TenantID = tenant
	var headerTime *time.Time
	if isSyslog {
		headerTime = b.fromSyslog(line, source)
	} else {
		b.ev.Source = source
		b.fromJSON(record)
	}

	now := time.Now
	if n.Now != nil {
		now = n.Now
	}
	retention := n.Retention
	if retention <= 0 {
		retention = DefaultRetention
	}
	b.ev.Ts = b.timestamp(now().UTC(), retention, headerTime)

	b.enrich()
	normalizeAction(b.ev.Action)
	b.ev.Tags = b.assembleTags()
	b.ev.Raw = storableJSON(b.ev.Raw)
	return b.ev, nil
}

type builder struct {
	fields     map[string]json.RawMessage
	ev         store.NewEventParams
	senderTags []string
	derived    []string
}

func (b *builder) tag(t string)       { b.derived = append(b.derived, t) }
func (b *builder) invalid(key string) { b.tag("invalid:" + key) }
func (b *builder) extra(key string) string {
	raw, ok := b.value(key)
	if !ok {
		return ""
	}
	s, _ := scalar(raw)
	return strings.TrimSpace(s)
}

// value returns a field unless it is absent or JSON null.
func (b *builder) value(key string) (json.RawMessage, bool) {
	raw, ok := b.fields[key]
	if !ok || string(raw) == "null" {
		return nil, false
	}
	return raw, true
}

// identity reads tenant or source: the record's own value, else the default.
// ok is false when the record holds something other than a string.
func (b *builder) identity(key, fallback string) (string, bool) {
	raw, present := b.value(key)
	if !present {
		return fallback, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	if s = strings.TrimSpace(s); s == "" {
		return fallback, true
	}
	return s, true
}

// syslogLine reports whether the record is a syslog line to parse: a string
// message whose source is unknown or one of the two syslog categories.
func (b *builder) syslogLine(source string) (string, bool) {
	switch gen.Source(source) {
	case "", gen.SourceFirewall, gen.SourceNetwork:
	default:
		return "", false
	}
	raw, ok := b.value("message")
	if !ok || len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var line string
	if err := json.Unmarshal(raw, &line); err != nil {
		return "", false
	}
	return line, true
}

// jsonKeys are the record keys the JSON path maps, in precedence order:
// src_ip is read before its alias ip, so an explicit src_ip wins.
var jsonKeys = []string{
	"vendor", "product", "event_type", "event_subtype", "severity", "action",
	"src_ip", "ip", "src_port", "dst_ip", "dst_port", "protocol",
	"user", "host", "process", "url", "http_method", "status_code",
	"rule_name", "rule_id",
}

var jsonAliases = map[string]string{"ip": "src_ip"}

func (b *builder) fromJSON(record []byte) {
	for _, key := range jsonKeys {
		raw, ok := b.value(key)
		if !ok {
			continue
		}
		field := key
		if alias, ok := jsonAliases[key]; ok {
			field = alias
		}
		v, ok := scalar(raw)
		if !ok || !setters[field](&b.ev, v) {
			b.invalid(key)
		}
	}

	if raw, ok := b.value("cloud"); ok {
		var cloud map[string]json.RawMessage
		if err := json.Unmarshal(raw, &cloud); err != nil || cloud == nil {
			b.invalid("cloud")
		} else {
			for key, dst := range map[string]**string{
				"account_id": &b.ev.CloudAccountID,
				"region":     &b.ev.CloudRegion,
				"service":    &b.ev.CloudService,
			} {
				raw, ok := cloud[key]
				if !ok || string(raw) == "null" {
					continue
				}
				if v, ok := scalar(raw); !ok || !setText(dst, v) {
					b.invalid("cloud." + key)
				}
			}
		}
	}

	b.readSenderTags()

	if raw, ok := b.value("raw"); ok {
		if raw[0] == '{' {
			b.ev.Raw = raw
		} else {
			b.invalid("raw")
		}
	}
	if b.ev.Raw == nil {
		b.ev.Raw = b.wholeRecord(record)
	}
}

// kvAliases are the key=value spellings syslog senders commonly use. Keys
// already named like a schema field map to it directly.
var kvAliases = map[string]string{
	"src":    "src_ip",
	"dst":    "dst_ip",
	"spt":    "src_port",
	"sport":  "src_port",
	"dpt":    "dst_port",
	"dport":  "dst_port",
	"proto":  "protocol",
	"policy": "rule_name",
	"event":  "event_type",
	"reason": "event_subtype",
}

// firewallKeys mark a key=value body as firewall traffic when the record does
// not say which source it is.
var firewallKeys = map[string]bool{
	"action": true, "src": true, "dst": true, "spt": true, "dpt": true,
	"proto": true, "policy": true, "src_ip": true, "dst_ip": true,
}

// fromSyslog fills the event from a syslog line and returns the header time,
// if the header carried a trustworthy one.
func (b *builder) fromSyslog(line, source string) *time.Time {
	m := parseSyslog(line)
	pairs := parseKV(m.Body)

	if source == "" {
		source = string(gen.SourceNetwork)
		for _, p := range pairs {
			if firewallKeys[strings.ToLower(p.Key)] {
				source = string(gen.SourceFirewall)
				break
			}
		}
	}
	b.ev.Source = source

	for _, p := range pairs {
		key := strings.ToLower(p.Key)
		field := key
		if alias, ok := kvAliases[key]; ok {
			field = alias
		}
		set, ok := setters[field]
		if !ok {
			continue
		}
		if !set(&b.ev, p.Value) {
			b.invalid(p.Key)
		}
	}

	setText(&b.ev.Host, m.Host)
	setText(&b.ev.Host, b.extra("peer_ip"))
	setText(&b.ev.Process, m.App)
	if b.ev.Severity == nil && m.Severity != nil {
		sev := syslogSeverity[*m.Severity]
		b.ev.Severity = &sev
	}

	b.readSenderTags()
	b.ev.Raw, _ = json.Marshal(map[string]string{"message": line})
	return m.Time
}

// syslogSeverity maps syslog severity (0 emergency .. 7 debug) onto 0..10.
var syslogSeverity = [8]int16{10, 9, 8, 7, 5, 3, 2, 0}

func (b *builder) readSenderTags() {
	raw, ok := b.value("_tags")
	if !ok {
		return
	}
	if err := json.Unmarshal(raw, &b.senderTags); err != nil {
		b.senderTags = nil
		b.invalid("_tags")
	}
}

// wholeRecord is the record as raw, less the collector's metadata. The input
// is copied, since callers may reuse the buffer for the next record.
func (b *builder) wholeRecord(record []byte) []byte {
	for _, key := range collectorKeys {
		if _, ok := b.fields[key]; ok {
			stripped := maps.Clone(b.fields)
			for _, key := range collectorKeys {
				delete(stripped, key)
			}
			out, _ := json.Marshal(stripped)
			return out
		}
	}
	return bytes.Clone(record)
}

// timestamp picks the event time: `@timestamp`, then the RFC 5424 header,
// then the collector's receipt time, then now. A time outside the retention
// window is replaced with now, so the event is neither purged on arrival nor
// parked in a partition that does not exist yet.
func (b *builder) timestamp(now time.Time, retention time.Duration, header *time.Time) time.Time {
	ts := now
	if t, err := time.Parse(time.RFC3339, b.extra("received_at")); err == nil {
		ts = t
	}
	if header != nil {
		ts = *header
	}
	if raw, ok := b.value("@timestamp"); ok {
		s, _ := scalar(raw)
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(s)); err == nil {
			ts = t
		} else {
			b.invalid("timestamp")
		}
	}

	if ts.Before(now.Add(-retention)) || ts.After(now.Add(futureSkew)) {
		b.tag(TagRebasedTime)
		return now
	}
	return ts.UTC()
}

func (b *builder) assembleTags() []string {
	for _, t := range b.senderTags {
		if len(cleanText(t)) > maxText {
			b.invalid("_tags")
			break
		}
	}

	tags := make([]string, 0, len(b.senderTags)+len(b.derived))
	seen := make(map[string]bool, cap(tags))
	for _, t := range slices.Concat(b.senderTags, b.derived) {
		if t = cleanText(t); t != "" && len(t) <= maxText && !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}
	return tags
}

// storableJSON returns raw in a form Postgres accepts as jsonb, which refuses
// invalid UTF-8, unpaired surrogates and the escaped NUL, \u0000. Decoding
// replaces the first two with U+FFFD, as it already does for every string the
// normalizer reads, and cleanNUL does the same for NUL. Anything else is
// returned as it is.
func storableJSON(raw []byte) []byte {
	if utf8.Valid(raw) && !riskyEscape.Match(raw) {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cleanJSON(v)); err != nil {
		return raw
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n"))
}

// riskyEscape also matches an escaped backslash followed by u0000, and valid
// surrogate pairs. Re-encoding those is harmless.
var riskyEscape = regexp.MustCompile(`(?i)\\u(0000|d[89a-f])`)

func cleanJSON(v any) any {
	switch v := v.(type) {
	case string:
		return cleanNUL(v)
	case []any:
		for i := range v {
			v[i] = cleanJSON(v[i])
		}
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[cleanNUL(k)] = cleanJSON(e)
		}
		return out
	}
	return v
}

// cleanText is the form every text value is stored in: trimmed, valid UTF-8,
// and free of NUL, which Postgres text cannot hold.
func cleanText(v string) string {
	return cleanNUL(strings.ToValidUTF8(strings.TrimSpace(v), string(utf8.RuneError)))
}

func cleanNUL(v string) string { return strings.ReplaceAll(v, "\x00", string(utf8.RuneError)) }

// setters store a textual value under a schema field name. Each reports
// false when the value cannot be coerced, and leaves a field that is already
// set alone, so the first source of a value wins.
var setters = map[string]func(e *store.NewEventParams, v string) bool{
	"vendor":        func(e *store.NewEventParams, v string) bool { return setText(&e.Vendor, v) },
	"product":       func(e *store.NewEventParams, v string) bool { return setText(&e.Product, v) },
	"event_type":    func(e *store.NewEventParams, v string) bool { return setText(&e.EventType, v) },
	"event_subtype": func(e *store.NewEventParams, v string) bool { return setText(&e.EventSubtype, v) },
	"action":        func(e *store.NewEventParams, v string) bool { return setText(&e.Action, v) },
	"protocol":      func(e *store.NewEventParams, v string) bool { return setText(&e.Protocol, strings.ToLower(v)) },
	"user":          func(e *store.NewEventParams, v string) bool { return setText(&e.UserName, v) },
	"host":          func(e *store.NewEventParams, v string) bool { return setText(&e.Host, v) },
	"process":       func(e *store.NewEventParams, v string) bool { return setText(&e.Process, v) },
	"url":           func(e *store.NewEventParams, v string) bool { return setText(&e.URL, v) },
	"http_method":   func(e *store.NewEventParams, v string) bool { return setText(&e.HTTPMethod, v) },
	"rule_name":     func(e *store.NewEventParams, v string) bool { return setText(&e.RuleName, v) },
	"rule_id":       func(e *store.NewEventParams, v string) bool { return setText(&e.RuleID, v) },
	"src_ip":        func(e *store.NewEventParams, v string) bool { return setAddr(&e.SrcIP, v) },
	"dst_ip":        func(e *store.NewEventParams, v string) bool { return setAddr(&e.DstIP, v) },
	"severity":      func(e *store.NewEventParams, v string) bool { return setInt(&e.Severity, v, 0, 10) },
	"src_port":      func(e *store.NewEventParams, v string) bool { return setInt(&e.SrcPort, v, 0, 65535) },
	"dst_port":      func(e *store.NewEventParams, v string) bool { return setInt(&e.DstPort, v, 0, 65535) },
	"status_code":   func(e *store.NewEventParams, v string) bool { return setInt(&e.StatusCode, v, 0, 999) },
}

// scalar renders a JSON string or number as text. Other JSON types have no
// text form and report false.
func scalar(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	switch c := raw[0]; {
	case c == '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", false
		}
		return s, true
	case c == '-' || isDigit(c):
		return string(raw), true
	}
	return "", false
}

// An empty value counts as absent, so it is never an error.

func setText(dst **string, v string) bool {
	if v = cleanText(v); *dst != nil || v == "" {
		return true
	}
	if len(v) > maxText {
		return false
	}
	*dst = &v
	return true
}

func setAddr(dst **netip.Addr, v string) bool {
	if v = strings.TrimSpace(v); *dst != nil || v == "" {
		return true
	}
	addr, err := netip.ParseAddr(v)
	if err != nil {
		return false
	}
	addr = addr.Unmap().WithZone("")
	*dst = &addr
	return true
}

func setInt[T int16 | int32](dst **T, v string, lo, hi int64) bool {
	if v = strings.TrimSpace(v); *dst != nil || v == "" {
		return true
	}
	n, ok := parseInt(v)
	if !ok || n < lo || n > hi {
		return false
	}
	t := T(n)
	*dst = &t
	return true
}

// parseInt accepts integers, including JSON's 8.0 and 1e3 spellings.
func parseInt(v string) (int64, bool) {
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return n, true
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
		return 0, false
	}
	return int64(f), true
}

// clip keeps a sender's value short enough to echo in a message.
func clip(s string) string {
	const max = 32
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}
