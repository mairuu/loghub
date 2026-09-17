package api

import (
	"bufio"
	"bytes"
	"cmp"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"slices"
	"strings"

	"github.com/mairuu/loghub/backend/internal/api/gen"
	"github.com/mairuu/loghub/backend/internal/auth"
	"github.com/mairuu/loghub/backend/internal/authz"
	"github.com/mairuu/loghub/backend/internal/ingest"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/store"
)

const (
	// maxRecordBytes bounds one event: a whole single-event body, or one
	// NDJSON line.
	maxRecordBytes = 1 << 20
	// maxNDJSONBytes bounds a batch or file body. Every event in it is held
	// in memory until the insert, so this is also the memory one request
	// may pin.
	maxNDJSONBytes = 32 << 20
	// maxReported caps errors[] in a response; `rejected` still counts all.
	maxReported = 100
)

func (s *Server) IngestEvent(w http.ResponseWriter, r *http.Request) {
	b, ok := s.startBatch(w, r)
	if !ok {
		return
	}
	if err := requireMediaType(r, "application/json"); err != nil {
		s.failWith(w, r, http.StatusUnsupportedMediaType, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRecordBytes))
	if err != nil {
		s.failRead(w, r, err, maxRecordBytes)
		return
	}

	ev, err := s.normalizer.Normalize(body, ingest.Defaults{})
	switch {
	case errors.KindOf(err) == errors.KindMalformed:
		// Not an event at all, so it is the request that failed rather than
		// a record in it.
		s.fail(w, r, errors.Malformed("invalid_json", "request body is not a JSON object").Wrapping(err))
		return
	case err != nil:
		b.reject(0, err)
	default:
		b.add(0, ev)
	}
	s.store(w, r, b)
}

func (s *Server) IngestBatch(w http.ResponseWriter, r *http.Request) {
	if b, ok := s.startBatch(w, r); ok {
		s.ingestNDJSON(w, r, b, ingest.Defaults{})
	}
}

func (s *Server) IngestFile(w http.ResponseWriter, r *http.Request, p gen.IngestFileParams) {
	b, ok := s.startBatch(w, r)
	if !ok {
		return
	}
	d := ingest.Defaults{
		Tenant: strings.TrimSpace(deref(p.Tenant)),
		Source: strings.TrimSpace(string(deref(p.Source))),
	}
	if err := d.Validate(); err != nil {
		s.fail(w, r, err)
		return
	}
	s.ingestNDJSON(w, r, b, d)
}

// startBatch answers a caller that may not create events for any tenant,
// and otherwise returns a batch that rejects each record whose tenant the
// caller may not write to. An anonymous request goes no further than this.
func (s *Server) startBatch(w http.ResponseWriter, r *http.Request) (*batch, bool) {
	caller := auth.CallerFrom(r.Context())
	if s.authz.Scopes(caller, authz.Events, authz.Create).Empty() {
		s.fail(w, r, authz.Deny(caller))
		return nil, false
	}
	return &batch{may: func(tenant string) bool {
		return s.authz.Can(caller, authz.Events, authz.Create, tenant)
	}}, true
}

func (s *Server) ingestNDJSON(w http.ResponseWriter, r *http.Request, b *batch, d ingest.Defaults) {
	if err := requireMediaType(r, "application/x-ndjson"); err != nil {
		s.failWith(w, r, http.StatusUnsupportedMediaType, err)
		return
	}

	err := eachRecord(http.MaxBytesReader(w, r.Body, maxNDJSONBytes), func(index int, record []byte) {
		if record == nil {
			b.reject(index, errors.Invalid("record_too_large", fmt.Sprintf("record exceeds %s", byteSize(maxRecordBytes))))
			return
		}
		ev, err := s.normalizer.Normalize(record, d)
		if err != nil {
			b.reject(index, err)
			return
		}
		b.add(index, ev)
	})
	if err != nil {
		s.failRead(w, r, err, maxNDJSONBytes)
		return
	}
	s.store(w, r, b)
}

// store inserts the batch's events and answers with what happened to every
// record. Nothing is stored if the insert fails.
func (s *Server) store(w http.ResponseWriter, r *http.Request, b *batch) {
	if len(b.events) > 0 {
		refused, err := s.events.Insert(r.Context(), b.events)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		b.merge(refused)
	}

	res := b.result()
	if b.rejected > 0 {
		// A collector never reads the response, so this is the only place
		// its rejected records show.
		first := b.reported[0]
		s.logger.LogAttrs(r.Context(), slog.LevelWarn, "records rejected",
			slog.String("request_id", requestID(r.Context())),
			slog.String("path", r.URL.Path),
			slog.Int("accepted", int(res.Accepted)),
			slog.Int("rejected", b.rejected),
			slog.Any("codes", b.codes),
			slog.Group("first",
				slog.Int("index", int(first.Index)),
				slog.String("code", first.Code),
				slog.String("message", first.Message),
			),
		)
	}
	s.respond(w, r, http.StatusOK, res)
}

// batch collects the outcome of each record in a request.
type batch struct {
	// may reports whether the caller may write to a tenant.
	may    func(tenant string) bool
	events []store.NewEventParams
	// lines holds each event's index in the request.
	lines []int
	// rejected counts every rejected record, and refused the events among
	// them that the insert turned away.
	rejected, refused int
	// codes counts the rejections by code.
	codes map[string]int
	// reported is the first maxReported rejections, in request order.
	reported []gen.IngestRejection
}

// add queues ev for the insert, unless the caller may not write to its
// tenant. The record's tenant is trusted only that far (ADR 0009); the insert
// itself runs as that tenant, so nothing beneath checks the caller.
func (b *batch) add(index int, ev store.NewEventParams) {
	if !b.may(ev.TenantID) {
		b.reject(index, authz.TenantNotPermitted(ev.TenantID))
		return
	}
	b.events = append(b.events, ev)
	b.lines = append(b.lines, index)
}

func (b *batch) reject(index int, err error) {
	if r := b.count(index, err); len(b.reported) < maxReported {
		b.reported = append(b.reported, r)
	}
}

// count adds a rejection to the totals and returns its report.
func (b *batch) count(index int, err error) gen.IngestRejection {
	r := rejection(index, err)
	b.rejected++
	if b.codes == nil {
		b.codes = map[string]int{}
	}
	b.codes[r.Code]++
	return r
}

// merge adds the insert's rejections, keeping the report in request order.
// Both lists are already ordered, so the first maxReported of each are
// enough to find the first maxReported of both.
func (b *batch) merge(refused []error) {
	var late []gen.IngestRejection
	for i, err := range refused {
		if err == nil {
			continue
		}
		b.refused++
		if r := b.count(b.lines[i], err); len(late) < maxReported {
			late = append(late, r)
		}
	}
	if len(late) == 0 {
		return
	}
	b.reported = append(b.reported, late...)
	slices.SortFunc(b.reported, func(x, y gen.IngestRejection) int { return cmp.Compare(x.Index, y.Index) })
	b.reported = b.reported[:min(len(b.reported), maxReported)]
}

func (b *batch) result() gen.IngestResult {
	out := gen.IngestResult{
		Accepted: int32(len(b.events) - b.refused),
		Rejected: int32(b.rejected),
	}
	if len(b.reported) > 0 {
		out.Errors = &b.reported
	}
	return out
}

func rejection(index int, err error) gen.IngestRejection {
	return gen.IngestRejection{
		Index:   int32(index),
		Code:    errors.CodeOf(err),
		Message: errors.MessageOf(err),
	}
}

// eachRecord calls fn with every non-blank line of body, trimmed, and its
// zero-based line number. A line longer than maxRecordBytes, not counting its
// line ending, is passed as nil. The record is only valid until fn returns.
func eachRecord(body io.Reader, fn func(index int, record []byte)) error {
	// Room for the longest line and a CRLF, so a line that fits is always
	// read whole.
	br := bufio.NewReaderSize(body, maxRecordBytes+2)
	for index := 0; ; index++ {
		line, err := br.ReadSlice('\n')
		tooLong := false
		for errors.Is(err, bufio.ErrBufferFull) {
			tooLong = true
			_, err = br.ReadSlice('\n')
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
		switch record := bytes.TrimSpace(line); {
		case tooLong || len(line) > maxRecordBytes:
			fn(index, nil)
		case len(record) > 0:
			fn(index, record)
		}
		if err != nil {
			return nil
		}
	}
}

func requireMediaType(r *http.Request, want string) error {
	got, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || got != want {
		return errors.Malformed("unsupported_media_type", "expected "+want)
	}
	return nil
}

// failRead answers a request whose body could not be read.
func (s *Server) failRead(w http.ResponseWriter, r *http.Request, err error, limit int64) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		s.failWith(w, r, http.StatusRequestEntityTooLarge,
			errors.Malformed("body_too_large", "request body exceeds "+byteSize(limit)))
		return
	}
	s.fail(w, r, errors.Malformed("unreadable_body", "cannot read the request body").Wrapping(err))
}

// byteSize is n in MiB, or in KiB when it is less than one.
func byteSize(n int64) string {
	if n < 1<<20 {
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d MiB", n>>20)
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
