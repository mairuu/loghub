package ingest

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// syslogMsg is what can be read from a syslog header. Every field is optional:
// a line with no recognisable header is all Body.
type syslogMsg struct {
	// Severity is the syslog severity, 0 (emergency) to 7 (debug).
	Severity *int
	// Time is set only for RFC 5424. An RFC 3164 timestamp has no year and
	// no zone, so it is not trusted.
	Time *time.Time
	Host string
	App  string
	Body string
}

// parseSyslog reads an RFC 5424 or RFC 3164 header. It never fails: whatever
// does not parse as a header is left in Body.
func parseSyslog(line string) syslogMsg {
	line = strings.TrimRight(line, "\r\n")
	pri, rest, ok := parsePRI(line)
	if !ok {
		return syslogMsg{Body: line}
	}
	sev := pri & 7

	if after, ok := strings.CutPrefix(rest, "1 "); ok {
		if m, ok := parse5424(after); ok {
			m.Severity = &sev
			return m
		}
	}
	if m, ok := parse3164(rest); ok {
		m.Severity = &sev
		return m
	}
	return syslogMsg{Severity: &sev, Body: rest}
}

// parsePRI reads `<N>` with N from 0 to 191 (facility 23, severity 7).
func parsePRI(line string) (int, string, bool) {
	if !strings.HasPrefix(line, "<") {
		return 0, "", false
	}
	end := strings.IndexByte(line, '>')
	if end < 2 || end > 4 {
		return 0, "", false
	}
	n, err := strconv.Atoi(line[1:end])
	if err != nil || n < 0 || n > 191 {
		return 0, "", false
	}
	return n, line[end+1:], true
}

// parse5424 reads `TIMESTAMP HOSTNAME APP-NAME PROCID MSGID SD [MSG]`, the
// part after `<PRI>1 `.
func parse5424(s string) (syslogMsg, bool) {
	var m syslogMsg
	var fields [5]string
	for i := range fields {
		f, rest, ok := strings.Cut(s, " ")
		if !ok {
			return m, false
		}
		fields[i], s = f, rest
	}

	if ts := fields[0]; ts != "-" {
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return m, false
		}
		m.Time = &t
	}
	m.Host = nilValue(fields[1])
	m.App = nilValue(fields[2])

	n, ok := structuredDataEnd(s)
	if !ok {
		return m, false
	}
	msg := strings.TrimPrefix(s[n:], " ")
	m.Body = strings.TrimPrefix(msg, "\uFEFF")
	return m, true
}

// structuredDataEnd returns where the STRUCTURED-DATA field at the start of s
// ends: `-`, or one or more `[id name="value" ...]` elements. Inside a value,
// `"`, `\` and `]` are escaped with a backslash.
func structuredDataEnd(s string) (int, bool) {
	if strings.HasPrefix(s, "-") {
		return 1, len(s) == 1 || s[1] == ' '
	}
	i := 0
	for i < len(s) && s[i] == '[' {
		inValue := false
		for i++; i < len(s); i++ {
			c := s[i]
			if c == '\\' && inValue {
				i++
				continue
			}
			if c == '"' {
				inValue = !inValue
			}
			if c == ']' && !inValue {
				break
			}
		}
		if i == len(s) {
			return 0, false
		}
		i++
	}
	return i, i > 0 && (i == len(s) || s[i] == ' ')
}

// tag3164 is the optional `app[pid]:` that starts an RFC 3164 message.
var tag3164 = regexp.MustCompile(`^([A-Za-z0-9_./-]{1,48})(?:\[[0-9]+\])?:(?: |$)`)

// parse3164 reads `Mmm dd hh:mm:ss HOSTNAME [TAG:] MSG`, the part after
// `<PRI>`. The hostname is optional, as some senders leave it out.
func parse3164(s string) (syslogMsg, bool) {
	var m syslogMsg
	if len(s) < len(time.Stamp)+1 || s[len(time.Stamp)] != ' ' {
		return m, false
	}
	if _, err := time.Parse(time.Stamp, s[:len(time.Stamp)]); err != nil {
		return m, false
	}
	s = s[len(time.Stamp)+1:]

	if !tag3164.MatchString(s) {
		host, rest, _ := strings.Cut(s, " ")
		m.Host, s = host, rest
	}
	if loc := tag3164.FindStringSubmatchIndex(s); loc != nil {
		m.App = s[loc[2]:loc[3]]
		s = s[loc[1]:]
	}
	m.Body = s
	return m, true
}

func nilValue(s string) string {
	if s == "-" {
		return ""
	}
	return s
}
