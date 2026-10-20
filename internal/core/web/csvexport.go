package web

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// csvFile is a CSV answer. Nothing is sent (no header, no byte-order mark)
// until the file has something to say, so a failure before the first row is an
// error answer and never a 200 with an empty-looking file.
//
// The file is UTF-8 with a byte-order mark (so Excel reads Persian right),
// commas between cells, double quotes around a cell that needs them, and
// \r\n after every row (RFC 4180, what Excel writes itself); a line break inside
// a quoted cell is \r\n too, so no file mixes the two.
type csvFile struct {
	w       http.ResponseWriter
	name    string
	cw      *csv.Writer
	rows    int // data rows written, header and notes apart
	started bool
}

// start sends the headers and the byte-order mark that makes spreadsheet
// programs read Persian right.
func (f *csvFile) start() {
	if f.started {
		return
	}
	f.started = true
	attachment(f.w.Header(), f.name)
	_, _ = f.w.Write([]byte{0xEF, 0xBB, 0xBF})
	f.cw = csv.NewWriter(f.w)
	f.cw.UseCRLF = true
}

// write adds a row (starting the file if it has not started).
func (f *csvFile) write(cells ...string) error {
	f.start()
	return f.cw.Write(cells)
}

// data adds a data row and pushes the file out every few hundred rows, so a
// long export is a stream and the connection never sits idle.
func (f *csvFile) data(cells ...string) error {
	if err := f.write(cells...); err != nil {
		return err
	}
	if f.rows++; f.rows%500 == 0 {
		f.cw.Flush()
		_ = http.NewResponseController(f.w).Flush()
	}
	return nil
}

func (f *csvFile) flush() {
	if f.cw != nil {
		f.cw.Flush()
	}
}

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

// exportClock reads the zone and the calendar a ledger or audit file writes its
// dates in: ?tz= (an IANA name; the store's zone when missing, as the reports
// read it) and ?calendar=gregorian|jalali (gregorian when missing). Anything
// else is a 400, before any file is started.
func (s *Server) exportClock(w http.ResponseWriter, r *http.Request) (clock, bool) {
	q := r.URL.Query()
	calendar, err := domain.NormalizeCalendar(q.Get("calendar"))
	if err != nil {
		s.fail(w, "export calendar", err)
		return clock{}, false
	}
	loc, err := s.cfg.Domain.ExportZone(r.Context(), q.Get("tz"))
	if err != nil {
		s.fail(w, "export zone", err)
		return clock{}, false
	}
	return newClock(loc, calendar), true
}

// exportFileName names a ledger or audit file by the days it covers, the days
// of its zone and calendar: "ledger-2026-10-14-to-2026-10-20" for entries from
// the 14th, "ledger-all-to-2026-10-20" when nothing starts it, "...-jalali" with
// the Jalali days (1405-07-22) in a Jalali file. to is exclusive (the filter is
// [from, to)) and the day the file was made ends a range that has no end.
func exportFileName(kind string, c clock, from, to *time.Time, now time.Time) string {
	end := now
	if to != nil {
		end = to.Add(-time.Second)
	}
	day := func(t time.Time) string { return fileDay(t.In(c.loc).Format("2006-01-02"), c.jalali) }
	start := "all"
	if from != nil {
		start = day(*from)
	}
	name := kind + "-" + start + "-to-" + day(end)
	if c.jalali {
		name += "-jalali"
	}
	return name + ".csv"
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
