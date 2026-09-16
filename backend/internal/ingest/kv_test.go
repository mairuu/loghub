package ingest

import (
	"slices"
	"testing"
)

func TestParseKV(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       [][2]string
	}{
		{
			name: "sample firewall body",
			body: "vendor=demo product=ngfw action=deny src=10.0.1.10 dst=8.8.8.8 spt=5353 dpt=53 proto=udp msg=DNS blocked policy=Block-DNS",
			want: [][2]string{
				{"vendor", "demo"}, {"product", "ngfw"}, {"action", "deny"},
				{"src", "10.0.1.10"}, {"dst", "8.8.8.8"}, {"spt", "5353"}, {"dpt", "53"},
				{"proto", "udp"}, {"msg", "DNS blocked"}, {"policy", "Block-DNS"},
			},
		},
		{
			name: "quoted value",
			body: `a="x y" b=2`,
			want: [][2]string{{"a", "x y"}, {"b", "2"}},
		},
		{
			name: "escapes inside quotes",
			body: `a="say \"hi\" \\o/" b=1`,
			want: [][2]string{{"a", `say "hi" \o/`}, {"b", "1"}},
		},
		{
			name: "empty value",
			body: "a= b=1",
			want: [][2]string{{"a", ""}, {"b", "1"}},
		},
		{
			name: "empty value followed by a word",
			body: "a= word b=1",
			want: [][2]string{{"a", "word"}, {"b", "1"}},
		},
		{
			name: "prose before the first pair",
			body: "link down if=ge-0/0/1",
			want: [][2]string{{"if", "ge-0/0/1"}},
		},
		{
			name: "uppercase keys, as iptables writes them",
			body: "SRC=1.2.3.4 DPT=53",
			want: [][2]string{{"SRC", "1.2.3.4"}, {"DPT", "53"}},
		},
		{
			name: "equals sign inside a value",
			body: "url=http://x/?a=b c=d",
			want: [][2]string{{"url", "http://x/?a=b"}, {"c", "d"}},
		},
		{
			name: "tab separated",
			body: "a=1\tb=2",
			want: [][2]string{{"a", "1"}, {"b", "2"}},
		},
		{
			name: "no pairs",
			body: "hello world",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got [][2]string
			for _, p := range parseKV(tc.body) {
				got = append(got, [2]string{p.Key, p.Value})
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("parseKV(%q)\n got: %q\nwant: %q", tc.body, got, tc.want)
			}
		})
	}
}
