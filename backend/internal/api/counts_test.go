package api_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/store"
)

func TestCountParams(t *testing.T) {
	const filters = "tenant=demoA&from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00%2B07:00" +
		"&source=aws&source=AD&event_type=CreateUser&action=create" +
		"&severity_min=1&severity_max=9&src_ip=10.0.0.1&user=alice&host=h" +
		"&tag=a&tag=b%20c&q=50%25+off"
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 2, 0, 0, 0, 0, time.FixedZone("", 7*3600))
	// Every filter search takes, as search reads it.
	filtered := store.EventFilter{
		Tenant: "demoA", From: &from, To: &to,
		Sources: []string{"aws", "AD"}, EventType: "CreateUser", Action: "create",
		SeverityMin: new(1), SeverityMax: new(9),
		SrcIP: "10.0.0.1", User: "alice", Host: "h",
		Tags: []string{"a", "b c"}, Query: "50% off",
	}

	for _, tc := range []struct {
		name, target string
		// want is a store.TopParams or a store.TimelineParams.
		want any
	}{
		{"top, filtered", "/api/v1/events/top?field=user&limit=100&" + filters, store.TopParams{EventFilter: filtered, Field: "user", Limit: 100}},
		{"top, unfiltered", "/api/v1/events/top?field=src_ip", store.TopParams{Field: "src_ip"}},
		// The store refuses these.
		{"top, other values", "/api/v1/events/top?field=nope&limit=101", store.TopParams{Field: "nope", Limit: 101}},
		{"timeline, filtered", "/api/v1/events/timeline?interval=1h&" + filters, store.TimelineParams{EventFilter: filtered, Interval: "1h"}},
		{"timeline, unfiltered", "/api/v1/events/timeline", store.TimelineParams{}},
		{"timeline, other values", "/api/v1/events/timeline?interval=1w", store.TimelineParams{Interval: "1w"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events fakeEvents
			res := call(t, newHandler(t, &events), "GET", tc.target, "", nil)
			if res.status != 200 {
				t.Fatalf("got %d %v", res.status, res.body)
			}
			var got, want store.EventFilter
			var gotRest, wantRest any
			switch w := tc.want.(type) {
			case store.TopParams:
				if len(events.tops) != 1 || len(events.timelines) != 0 {
					t.Fatalf("%d tops and %d timelines", len(events.tops), len(events.timelines))
				}
				g := events.tops[0]
				got, want = g.EventFilter, w.EventFilter
				g.EventFilter, w.EventFilter = store.EventFilter{}, store.EventFilter{}
				gotRest, wantRest = g, w
			case store.TimelineParams:
				if len(events.timelines) != 1 || len(events.tops) != 0 {
					t.Fatalf("%d tops and %d timelines", len(events.tops), len(events.timelines))
				}
				g := events.timelines[0]
				got, want = g.EventFilter, w.EventFilter
				g.EventFilter, w.EventFilter = store.EventFilter{}, store.EventFilter{}
				gotRest, wantRest = g, w
			}
			// Times are compared as instants.
			if !sameTime(got.From, want.From) || !sameTime(got.To, want.To) {
				t.Errorf("window = %v..%v, want %v..%v", got.From, got.To, want.From, want.To)
			}
			got.From, got.To, want.From, want.To = nil, nil, nil, nil
			if !reflect.DeepEqual(got, want) {
				t.Errorf("filter\n%+v\nwant\n%+v", got, want)
			}
			if !reflect.DeepEqual(gotRest, wantRest) {
				t.Errorf("counted with %+v, want %+v", gotRest, wantRest)
			}
			if events.scopes[0] != store.AdminScope {
				t.Errorf("scope = %+v", events.scopes[0])
			}
		})
	}
}

func TestCountsReject(t *testing.T) {
	const limitMessage = "limit must be an integer between 1 and 100"
	for _, tc := range []struct {
		name, target string
		events       fakeEvents
		status       int
		want         string
		wantCount    bool
	}{
		{name: "no field", target: "/api/v1/events/top", status: 400, want: errorBody("invalid_parameter", "field is required")},
		{name: "limit zero", target: "/api/v1/events/top?field=user&limit=0", status: 400, want: errorBody("invalid_parameter", limitMessage)},
		{name: "limit not a number", target: "/api/v1/events/top?field=user&limit=ten", status: 400, want: errorBody("invalid_parameter", limitMessage)},
		{name: "top severity not a number", target: "/api/v1/events/top?field=user&severity_min=high", status: 400,
			want: errorBody("invalid_parameter", "severity_min must be an integer between 0 and 10")},
		{name: "top unknown source", target: "/api/v1/events/top?field=user&source=syslog", status: 400,
			want: errorBody("invalid_parameter", `source "syslog" is not a known source category`)},
		{name: "timeline unknown source", target: "/api/v1/events/timeline?source=syslog", status: 400,
			want: errorBody("invalid_parameter", `source "syslog" is not a known source category`)},
		{name: "timeline from not a time", target: "/api/v1/events/timeline?from=yesterday", status: 400,
			want: errorBody("invalid_parameter", "from must be an RFC 3339 timestamp, with '+' in the offset escaped as %2B")},
		{
			name: "store refuses a top parameter", target: "/api/v1/events/top?field=nope",
			events: fakeEvents{countErr: errors.Malformed("invalid_parameter", "field must be one of src_ip")},
			status: 400, want: errorBody("invalid_parameter", "field must be one of src_ip"), wantCount: true,
		},
		{
			name: "store refuses a timeline parameter", target: "/api/v1/events/timeline?interval=1w",
			events: fakeEvents{countErr: errors.Malformed("invalid_parameter", "interval must be one of 1m")},
			status: 400, want: errorBody("invalid_parameter", "interval must be one of 1m"), wantCount: true,
		},
		{
			name: "count times out", target: "/api/v1/events/timeline?q=x",
			events: fakeEvents{countErr: errors.Unavailable("search_timeout", "the search took longer than 10s")},
			status: 503, want: errorBody("search_timeout", "the search took longer than 10s"), wantCount: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call(t, newHandler(t, &tc.events), "GET", tc.target, "", nil).check(t, tc.status, tc.want)
			if counted := tc.events.read(); counted != tc.wantCount {
				t.Errorf("counted = %v, want %v", counted, tc.wantCount)
			}
		})
	}

	// Search's limit is still its own.
	call(t, newHandler(t, &fakeEvents{}), "GET", "/api/v1/events?limit=ten", "", nil).
		check(t, 400, errorBody("invalid_parameter", "limit must be an integer between 1 and 1000"))
}

func TestCountsResponse(t *testing.T) {
	bangkok := time.FixedZone("ICT", 7*3600)
	from := time.Date(2026, 1, 1, 7, 0, 0, 0, bangkok)
	to := time.Date(2026, 1, 2, 7, 0, 0, 500, bangkok)

	t.Run("top", func(t *testing.T) {
		events := fakeEvents{top: store.Top{From: from, To: to, Values: []store.TopValue{
			{Value: "203.0.113.77", Count: 9007199254740993},
			{Value: "10.0.0.1", Count: 1},
		}}}
		res := call(t, newHandler(t, &events), "GET", "/api/v1/events/top?field=src_ip", "", nil)
		res.check(t, 200, `{
			"field": "src_ip", "from": "2026-01-01T00:00:00Z", "to": "2026-01-02T00:00:00.0000005Z",
			"items": [{"value": "203.0.113.77", "count": 9007199254740993}, {"value": "10.0.0.1", "count": 1}]
		}`)
		// check compares numbers as floats, which can't tell these apart.
		if !strings.Contains(res.raw, `"count":9007199254740993`) {
			t.Errorf("count lost precision: %s", res.raw)
		}
	})

	t.Run("top of nothing", func(t *testing.T) {
		events := fakeEvents{top: store.Top{From: from, To: to}}
		call(t, newHandler(t, &events), "GET", "/api/v1/events/top?field=user", "", nil).check(t, 200, `{
			"field": "user", "from": "2026-01-01T00:00:00Z", "to": "2026-01-02T00:00:00.0000005Z", "items": []
		}`)
	})

	t.Run("timeline", func(t *testing.T) {
		events := fakeEvents{timeline: store.Timeline{From: from, To: to, Interval: "12h", Buckets: []store.Bucket{
			{Start: time.Date(2026, 1, 1, 7, 0, 0, 0, bangkok), Count: 3},
			{Start: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), Count: 0},
			{Start: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Count: 1},
		}}}
		call(t, newHandler(t, &events), "GET", "/api/v1/events/timeline", "", nil).check(t, 200, `{
			"from": "2026-01-01T00:00:00Z", "to": "2026-01-02T00:00:00.0000005Z", "interval": "12h",
			"buckets": [
				{"start": "2026-01-01T00:00:00Z", "count": 3},
				{"start": "2026-01-01T12:00:00Z", "count": 0},
				{"start": "2026-01-02T00:00:00Z", "count": 1}
			]
		}`)
	})

	t.Run("timeline of nothing", func(t *testing.T) {
		events := fakeEvents{timeline: store.Timeline{From: from, To: to, Interval: "1d"}}
		call(t, newHandler(t, &events), "GET", "/api/v1/events/timeline", "", nil).check(t, 200, `{
			"from": "2026-01-01T00:00:00Z", "to": "2026-01-02T00:00:00.0000005Z", "interval": "1d", "buckets": []
		}`)
	})
}
