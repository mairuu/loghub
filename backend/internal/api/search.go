package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mairuu/loghub/backend/internal/api/gen"
	"github.com/mairuu/loghub/backend/internal/auth"
	"github.com/mairuu/loghub/backend/internal/authz"
	"github.com/mairuu/loghub/backend/internal/store"
)

func (s *Server) SearchEvents(w http.ResponseWriter, r *http.Request, p gen.SearchEventsParams) {
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

	q := store.SearchParams{
		EventFilter: f,
		Order:       store.Order(deref(p.Order)),
		Cursor:      deref(p.Cursor),
	}
	if p.Limit != nil {
		// The store reads zero as the default page size.
		if *p.Limit < 1 {
			s.fail(w, r, invalidParam("limit must be "+paramFormats["limit"]))
			return
		}
		q.Limit = int(*p.Limit)
	}

	page, err := s.events.Search(r.Context(), scopeOf(caller), q)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	out := gen.EventPage{Items: make([]gen.Event, len(page.Events))}
	for i, e := range page.Events {
		out.Items[i] = toEvent(e)
	}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	s.respond(w, r, http.StatusOK, out)
}

// eventFilter completes the filter a request for events names, or refuses
// it: the caller must be allowed to read some tenant's events, the sources
// must be known ones, and the tenant is narrowed within what the caller may
// read.
func (s *Server) eventFilter(caller authz.Caller, sources *[]gen.Source, f store.EventFilter) (store.EventFilter, error) {
	readable := s.authz.Scopes(caller, authz.Events, authz.Read)
	if readable.Empty() {
		return store.EventFilter{}, authz.Deny(caller)
	}
	for _, source := range deref(sources) {
		// Blank means no filter, as it does for every other parameter.
		if v := strings.ToLower(strings.TrimSpace(string(source))); v != "" && !gen.Source(v).Valid() {
			return store.EventFilter{}, invalidParam(fmt.Sprintf("source %q is not a known source category", source))
		}
		f.Sources = append(f.Sources, string(source))
	}

	// The tenant filter narrows within what the caller may read, and is that
	// tenant when it is the only one.
	tenant := strings.TrimSpace(f.Tenant)
	only, one := readable.Only()
	switch {
	case tenant != "" && !readable.Contains(tenant):
		return store.EventFilter{}, authz.TenantNotPermitted(tenant)
	case tenant == "" && one:
		f.Tenant = only
	}
	return f, nil
}

// scopeOf is the row-level security context for the caller's queries. It
// comes from who the caller is, never from a policy answer (ADR 0009): the
// tenant is the caller's own, and only an admin reads every tenant.
func scopeOf(c authz.Caller) store.Scope {
	return store.Scope{TenantID: c.Tenant, Admin: c.Role == authz.Admin}
}

func toEvent(e store.Event) gen.Event {
	out := gen.Event{
		ID:           new(strconv.FormatInt(e.ID, 10)),
		Timestamp:    e.Ts.UTC(),
		ReceivedAt:   e.ReceivedAt.UTC(),
		Tenant:       e.TenantID,
		Source:       gen.Source(e.Source),
		Vendor:       e.Vendor,
		Product:      e.Product,
		EventType:    e.EventType,
		EventSubtype: e.EventSubtype,
		Action:       e.Action,
		SrcIP:        e.SrcIP,
		SrcPort:      e.SrcPort,
		DstIP:        e.DstIP,
		DstPort:      e.DstPort,
		Protocol:     e.Protocol,
		UserName:     e.UserName,
		Host:         e.Host,
		Process:      e.Process,
		URL:          e.URL,
		HTTPMethod:   e.HTTPMethod,
		StatusCode:   e.StatusCode,
		RuleName:     e.RuleName,
		RuleID:       e.RuleID,
		Raw:          json.RawMessage(e.Raw),
		Tags:         e.Tags,
	}
	if e.Severity != nil {
		out.Severity = new(int(*e.Severity))
	}
	if e.CloudAccountID != nil || e.CloudRegion != nil || e.CloudService != nil {
		out.Cloud = &gen.CloudContext{
			AccountID: e.CloudAccountID,
			Region:    e.CloudRegion,
			Service:   e.CloudService,
		}
	}
	if out.Tags == nil {
		// Required by the schema, so an empty list rather than null.
		out.Tags = []string{}
	}
	return out
}
