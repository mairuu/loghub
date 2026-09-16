package store

import (
	"net/netip"
	"time"
)

// NewEventParams is one normalized event, ready to insert. Nil fields are
// stored as NULL. internal/ingest builds these from vendor records.
type NewEventParams struct {
	Ts             time.Time
	TenantID       string
	Source         string
	Vendor         *string
	Product        *string
	EventType      *string
	EventSubtype   *string
	Severity       *int16
	Action         *string
	SrcIP          *netip.Addr
	SrcPort        *int32
	DstIP          *netip.Addr
	DstPort        *int32
	Protocol       *string
	UserName       *string
	Host           *string
	Process        *string
	URL            *string
	HTTPMethod     *string
	StatusCode     *int32
	RuleName       *string
	RuleID         *string
	CloudAccountID *string
	CloudRegion    *string
	CloudService   *string
	// Raw is a JSON object.
	Raw []byte
	// Tags is never nil: the column is NOT NULL, and pgx sends a nil slice as NULL.
	Tags []string
}
