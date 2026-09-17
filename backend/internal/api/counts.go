package api

// The dashboard's counts. They count what SearchEvents would return, so they
// take its filters and refuse what it refuses.

import (
	"net/http"

	"github.com/mairuu/loghub/backend/internal/api/gen"
	"github.com/mairuu/loghub/backend/internal/auth"
	"github.com/mairuu/loghub/backend/internal/store"
)

const topLimitFormat = "an integer between 1 and 100"

func (s *Server) TopValues(w http.ResponseWriter, r *http.Request, p gen.TopValuesParams) {
	caller := auth.CallerFrom(r.Context())
	f, err := s.eventFilter(caller, p.Source, store.EventFilter{
		Tenant:      deref(p.Tenant),
		From:        p.From,
		To:          p.To,
		EventType:   deref(p.EventType),
		Action:      deref(p.Action),
		SeverityMin: p.SeverityMin,
		SeverityMax: p.SeverityMax,
		SrcIP:       deref(p.SrcIP),
		User:        deref(p.User),
		Host:        deref(p.Host),
		Tags:        deref(p.Tag),
		Query:       deref(p.Q),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}

	q := store.TopParams{EventFilter: f, Field: string(p.Field)}
	if p.Limit != nil {
		// The store reads zero as the default.
		if *p.Limit < 1 {
			s.fail(w, r, invalidParam("limit must be "+topLimitFormat))
			return
		}
		q.Limit = int(*p.Limit)
	}

	top, err := s.events.Top(r.Context(), scopeOf(caller), q)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	out := gen.TopValueList{
		Field: p.Field,
		From:  top.From.UTC(),
		To:    top.To.UTC(),
		Items: make([]gen.TopValue, len(top.Values)),
	}
	for i, v := range top.Values {
		out.Items[i] = gen.TopValue{Value: v.Value, Count: v.Count}
	}
	s.respond(w, r, http.StatusOK, out)
}

func (s *Server) Timeline(w http.ResponseWriter, r *http.Request, p gen.TimelineParams) {
	caller := auth.CallerFrom(r.Context())
	f, err := s.eventFilter(caller, p.Source, store.EventFilter{
		Tenant:      deref(p.Tenant),
		From:        p.From,
		To:          p.To,
		EventType:   deref(p.EventType),
		Action:      deref(p.Action),
		SeverityMin: p.SeverityMin,
		SeverityMax: p.SeverityMax,
		SrcIP:       deref(p.SrcIP),
		User:        deref(p.User),
		Host:        deref(p.Host),
		Tags:        deref(p.Tag),
		Query:       deref(p.Q),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}

	tl, err := s.events.Timeline(r.Context(), scopeOf(caller), store.TimelineParams{
		EventFilter: f,
		Interval:    string(deref(p.Interval)),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}

	out := gen.Timeline{
		From:     tl.From.UTC(),
		To:       tl.To.UTC(),
		Interval: gen.TimelineInterval(tl.Interval),
		Buckets:  make([]gen.TimelineBucket, len(tl.Buckets)),
	}
	for i, b := range tl.Buckets {
		out.Buckets[i] = gen.TimelineBucket{Start: b.Start.UTC(), Count: b.Count}
	}
	s.respond(w, r, http.StatusOK, out)
}
