package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/brf-tech/filex/backend/internal/usage"
)

// usageIsInstanceWide is the refusal a tenant admin reads. There is one
// storage account behind the instance and one invoice for it; what it costs is
// the operator's business, and the report names every bucket on the account —
// including buckets that are nothing to do with the tenant asking.
const usageIsInstanceWide = "storage usage and cost apply to the whole instance and are managed by the platform operator"

// Usage serves /api/admin/usage.
//
// Configuration is written through the ordinary settings endpoint (the keys are
// ordinary settings rows, and `allowSettingWrite` already reserves every
// non-branding key to the supertenant); this handler only reads.
type Usage struct {
	Svc *usage.Service
}

// Report answers GET /api/admin/usage.
//
//	?days=30            the last N days ending today (default 30, max 400)
//	?from=&to=          an explicit range, YYYY-MM-DD, inclusive
//
// ⚠ The default range ENDS TODAY even though today's report does not exist
// yet: a page that silently shifted the window back a day would show a total
// that does not match the dates on its own axis. The absent day is a note in
// the payload instead.
func (h *Usage) Report(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, usageIsInstanceWide) {
		return
	}
	if h.Svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "usage reporting is not wired on this instance",
		})
		return
	}

	from, to, err := usageRange(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	rep, rerr := h.Svc.Report(r.Context(), from, to)
	if rerr != nil {
		// A misconfiguration (a storage that no longer exists, a provider that
		// refuses the key) is the operator's to fix, and the message names
		// what to look at. It is not a 500: nothing is broken here.
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":    rerr.Error(),
			"hint":     "check Settings → Usage: the report storage must exist and be readable",
			"settings": h.Svc.Settings(r.Context()),
		})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// usageRange parses the window, defaulting to the last 30 days.
func usageRange(r *http.Request) (time.Time, time.Time, error) {
	q := r.URL.Query()
	today := time.Now().UTC().Truncate(24 * time.Hour)

	if f, t := q.Get("from"), q.Get("to"); f != "" || t != "" {
		from, err := time.Parse("2006-01-02", f)
		if err != nil {
			return time.Time{}, time.Time{}, errBadDate("from")
		}
		to := today
		if t != "" {
			to, err = time.Parse("2006-01-02", t)
			if err != nil {
				return time.Time{}, time.Time{}, errBadDate("to")
			}
		}
		if to.Before(from) {
			from, to = to, from
		}
		return from.UTC(), to.UTC(), nil
	}

	days := 30
	if v := q.Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return time.Time{}, time.Time{}, errBadDays
		}
		// A ceiling, because each day is one request to the provider: a
		// mistyped `days=100000` would otherwise sit there issuing them.
		if n > 400 {
			n = 400
		}
		days = n
	}
	return today.AddDate(0, 0, -(days - 1)), today, nil
}

type usageErr string

func (e usageErr) Error() string { return string(e) }

const errBadDays = usageErr("days must be a positive number")

func errBadDate(field string) error {
	return usageErr(field + " must be a date, YYYY-MM-DD")
}
