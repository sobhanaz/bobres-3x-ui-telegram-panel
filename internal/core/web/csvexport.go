package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// csvExport describes one CSV answer that is not a report (ledger.csv,
// audit.csv). streamCSV gives it everything the reports' exports have: a place
// among the running exports, the export deadline, a file that starts with its
// first row, a note when it ends short, and an audit entry saying how it ended.
type csvExport struct {
	name   string     // the file's name
	action string     // the audit action ("export.ledger")
	query  url.Values // the request's filters, kept in the audit entry
	header []string
	// each reads the rows (newest first) and calls row for each one; it
	// reports whether more matched than it was asked for.
	each func(ctx context.Context, row func(cells ...string) error) (more bool, err error)
	// moreNote is the last line of a file that holds only part of the matches.
	moreNote string
}

// exportAuditDetail is what an export's audit entry says: the request's filters
// as they always were (a filter named outcome or rows does not exist), plus how
// the export ended and how many data rows the file holds.
func exportAuditDetail(q url.Values, outcome string, rows int) map[string]any {
	d := make(map[string]any, len(q)+2)
	for k, v := range q {
		d[k] = v
	}
	d["outcome"], d["rows"] = outcome, rows
	return d
}

// exportTimeoutErr tells an export that ran out of time (PostgreSQL's limit or
// the deadline of the whole export) from a failure, as the reports' do.
func exportTimeoutErr(err error) error {
	if errors.Is(err, store.ErrReportTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", domain.ErrReportTimeout, err)
	}
	return err
}

// streamCSV runs x. Nothing is sent (no header, no byte-order mark) until the
// first data row exists: a failure before it is an error answer (503 when the
// export ran out of time or a backend is down), never a 200 with a header-only
// file. A failure after it ends the file with "# export incomplete: <reason>".
// No match at all is a file with its header only. The entry in the audit log
// is written last, with the outcome (complete, incomplete, failed) and the
// rows, even when the client has gone.
func (s *Server) streamCSV(w http.ResponseWriter, r *http.Request, x csvExport) {
	window := s.exportWindow()
	stretch(w, window+reportGrace)
	release, ok := s.takeExport(w)
	if !ok {
		return
	}
	defer release()
	done := func(outcome string, rows int) {
		s.audit(context.WithoutCancel(r.Context()), actor(r), x.action, exportAuditDetail(x.query, outcome, rows))
	}
	f := &csvFile{w: w, name: x.name}
	ctx, cancel := context.WithTimeout(r.Context(), window)
	defer cancel()
	more, err := x.each(ctx, func(cells ...string) error {
		// Rows already received are read without a look at the deadline.
		if err := ctx.Err(); err != nil {
			return err
		}
		if !f.started {
			if err := f.write(x.header...); err != nil {
				return err
			}
		}
		if s.rowPause > 0 {
			time.Sleep(s.rowPause)
		}
		return f.data(cells...)
	})
	err = exportTimeoutErr(err)
	switch {
	case err != nil && !f.started:
		// Nothing has been sent: answer with the error, not with an empty file.
		s.cfg.Log.Error("dashboard api: export "+x.action, "err", err)
		done("failed", 0)
		if errors.Is(err, domain.ErrReportTimeout) {
			writeError(w, http.StatusServiceUnavailable, "timeout",
				"the export took too long: narrow the filter or choose a shorter range")
			return
		}
		s.fail(w, x.action, err)
		return
	case err != nil:
		// The file is out: it ends with a line that says it is not whole.
		s.cfg.Log.Error("dashboard api: export "+x.action, "err", err)
		_ = f.write("# export incomplete: " + exportReason(err, window))
		f.flush()
		done("incomplete", f.rows)
		return
	}
	if !f.started {
		_ = f.write(x.header...) // nothing matches: a file with its header only
	}
	if more {
		_ = f.write(x.moreNote)
	}
	f.flush()
	done("complete", f.rows)
}

// moreEntriesNote is the last line of an export cut at max rows.
func moreEntriesNote(max int) string {
	return "# more entries match: narrow the filter (at most " + strconv.Itoa(max) + " rows per file)"
}
