package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mairuu/loghub/backend/internal/api/gen"
	"github.com/mairuu/loghub/backend/internal/store"
)

func (s *Server) SearchEvents(w http.ResponseWriter, r *http.Request, p gen.SearchEventsParams) {
	q := store.SearchParams{
		EventFilter: store.EventFilter{
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
		},
		Order:  store.Order(deref(p.Order)),
		Cursor: deref(p.Cursor),
	}
	for _, source := range deref(p.Source) {
		// Blank means no filter, as it does for every other parameter.
		if v := strings.ToLower(strings.TrimSpace(string(source))); v != "" && !gen.Source(v).Valid() {
			s.fail(w, r, invalidParam(fmt.Sprintf("source %q is not a known source category", source)))
			return
		}
		q.Sources = append(q.Sources, string(source))
	}
	if p.Limit != nil {
		// The store reads zero as the default page size.
		if *p.Limit < 1 {
			s.fail(w, r, invalidParam("limit must be "+paramFormats["limit"]))
			return
		}
		q.Limit = int(*p.Limit)
	}

	// There is no caller identity until authentication lands (ADR 0009), so
	// every search may read every tenant, as the spec says for `tenant`.
	page, err := s.events.Search(r.Context(), store.AdminScope, q)
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
