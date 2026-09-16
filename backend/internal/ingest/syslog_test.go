package ingest

import (
	"testing"
	"time"
)

const (
	firewallLine = `<134>Aug 20 12:44:56 fw01 vendor=demo product=ngfw action=deny src=10.0.1.10 dst=8.8.8.8 spt=5353 dpt=53 proto=udp msg=DNS blocked policy=Block-DNS`
	routerLine   = `<190>Aug 20 13:01:02 r1 if=ge-0/0/1 event=link-down mac=aa:bb:cc:dd:ee:ff reason=carrier-loss`
	// What `logger -n host "hello from logger"` sends: util-linux defaults to RFC 5424.
	loggerLine = `<13>1 2026-09-16T16:48:31.447979+07:00 tofu mairuu - - [timeQuality tzKnown="1" isSynced="1" syncAccuracy="749000"] hello from logger`
)

func TestParseSyslog(t *testing.T) {
	sev := func(n int) *int { return &n }
	at := func(s string) *time.Time { v := ts(s); return &v }

	for _, tc := range []struct {
		name, line string
		want       syslogMsg
	}{
		{
			name: "logger default (RFC 5424)",
			line: loggerLine,
			want: syslogMsg{Severity: sev(5), Time: at("2026-09-16T16:48:31.447979+07:00"), Host: "tofu", App: "mairuu", Body: "hello from logger"},
		},
		{
			name: "RFC 5424 with every field nil",
			line: "<14>1 - - - - - -",
			want: syslogMsg{Severity: sev(6)},
		},
		{
			name: "RFC 5424 with escaped structured data",
			line: `<11>1 2026-09-16T00:00:00Z h app 42 ID47 [ex@1 a="x\"]y" b="c\\"][ex@2 z="1"] body text`,
			want: syslogMsg{Severity: sev(3), Time: at("2026-09-16T00:00:00Z"), Host: "h", App: "app", Body: "body text"},
		},
		{
			name: "RFC 5424 with a BOM before the message",
			line: "<13>1 2026-09-16T00:00:00Z h app - - - \xef\xbb\xbfhello",
			want: syslogMsg{Severity: sev(5), Time: at("2026-09-16T00:00:00Z"), Host: "h", App: "app", Body: "hello"},
		},
		{
			name: "RFC 5424 with a bad timestamp keeps the rest as body",
			line: "<13>1 yesterday h app - - - x",
			want: syslogMsg{Severity: sev(5), Body: "1 yesterday h app - - - x"},
		},
		{
			name: "sample firewall line (RFC 3164)",
			line: firewallLine,
			want: syslogMsg{Severity: sev(6), Host: "fw01", Body: "vendor=demo product=ngfw action=deny src=10.0.1.10 dst=8.8.8.8 spt=5353 dpt=53 proto=udp msg=DNS blocked policy=Block-DNS"},
		},
		{
			name: "sample router line (RFC 3164)",
			line: routerLine,
			want: syslogMsg{Severity: sev(6), Host: "r1", Body: "if=ge-0/0/1 event=link-down mac=aa:bb:cc:dd:ee:ff reason=carrier-loss"},
		},
		{
			name: "RFC 3164 with a tag and pid",
			line: "<13>Sep 16 16:48:31 tofu mairuu[123]: hello 3164",
			want: syslogMsg{Severity: sev(5), Host: "tofu", App: "mairuu", Body: "hello 3164"},
		},
		{
			name: "RFC 3164 with a space-padded day",
			line: "<13>Sep  6 16:48:31 tofu app: x",
			want: syslogMsg{Severity: sev(5), Host: "tofu", App: "app", Body: "x"},
		},
		{
			name: "RFC 3164 without a hostname",
			line: "<13>Sep 16 16:48:31 mairuu: hello",
			want: syslogMsg{Severity: sev(5), App: "mairuu", Body: "hello"},
		},
		{
			name: "trailing CRLF",
			line: "<13>Sep 16 16:48:31 tofu app: x\r\n",
			want: syslogMsg{Severity: sev(5), Host: "tofu", App: "app", Body: "x"},
		},
		{
			name: "PRI and nothing else recognisable",
			line: "<13>hello",
			want: syslogMsg{Severity: sev(5), Body: "hello"},
		},
		{
			name: "no header at all",
			line: "hello world",
			want: syslogMsg{Body: "hello world"},
		},
		{
			name: "PRI out of range",
			line: "<999>hello",
			want: syslogMsg{Body: "<999>hello"},
		},
		{
			name: "unterminated PRI",
			line: "<13 hello",
			want: syslogMsg{Body: "<13 hello"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSyslog(tc.line)
			if !equalPtr(got.Severity, tc.want.Severity, func(a, b int) bool { return a == b }) {
				t.Errorf("severity: got %v, want %v", show(got.Severity), show(tc.want.Severity))
			}
			if !equalPtr(got.Time, tc.want.Time, time.Time.Equal) {
				t.Errorf("time: got %v, want %v", show(got.Time), show(tc.want.Time))
			}
			if got.Host != tc.want.Host || got.App != tc.want.App || got.Body != tc.want.Body {
				t.Errorf("got host=%q app=%q body=%q\nwant host=%q app=%q body=%q",
					got.Host, got.App, got.Body, tc.want.Host, tc.want.App, tc.want.Body)
			}
		})
	}
}

func equalPtr[T any](a, b *T, eq func(T, T) bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return eq(*a, *b)
}

func show[T any](p *T) any {
	if p == nil {
		return "<nil>"
	}
	return *p
}
