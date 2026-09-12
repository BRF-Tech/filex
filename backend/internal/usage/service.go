package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/dbsetting"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// The settings this feature owns. They are deliberately a handful of plain
// rows rather than one blob: an operator fixing a typo in an account id should
// not have to re-paste a JSON document, and the admin form can validate each
// field where it is typed.
var (
	// SettingProvider is which provider publishes the report, or "" for none.
	SettingProvider = dbsetting.StringSpec{
		Key:       "usage.provider",
		EnvVar:    "FILEX_USAGE_PROVIDER",
		Default:   "",
		Normalize: func(raw string) string { return strings.ToLower(strings.TrimSpace(raw)) },
		Check: func(v string) error {
			switch v {
			case "", ProviderB2:
				return nil
			}
			return fmt.Errorf("unknown usage provider %q (known: %s)", v, ProviderB2)
		},
	}
	// SettingReportStorage names the filex STORAGE whose root is the provider's
	// report bucket.
	//
	// ⚠ A storage rather than a second set of credentials, on purpose: the
	// operator already knows how to add one, it is already encrypted the same
	// way, and the report bucket is genuinely just a bucket. Make it read-only
	// and it cannot be written to by anything here — this package only lists
	// and reads.
	SettingReportStorage = dbsetting.StringSpec{
		Key:     "usage.report_storage",
		EnvVar:  "FILEX_USAGE_REPORT_STORAGE",
		Default: "",
	}
	// SettingAccountID is the provider's account id. Optional for B2 — it only
	// names the standalone file — but it is what an operator recognises.
	SettingAccountID = dbsetting.StringSpec{
		Key:     "usage.account_id",
		EnvVar:  "FILEX_USAGE_ACCOUNT_ID",
		Default: "",
	}
	// SettingPrefix is where the dated folders live inside that storage.
	SettingPrefix = dbsetting.StringSpec{
		Key:     "usage.prefix",
		EnvVar:  "FILEX_USAGE_PREFIX",
		Default: "",
	}
	// SettingPricing is the price table, as JSON. It is configuration because
	// prices change; see Pricing.
	SettingPricing = dbsetting.StringSpec{
		Key:     "usage.pricing",
		EnvVar:  "FILEX_USAGE_PRICING",
		Default: "",
		Check: func(v string) error {
			if strings.TrimSpace(v) == "" {
				return nil
			}
			var p Pricing
			if err := json.Unmarshal([]byte(v), &p); err != nil {
				return fmt.Errorf("pricing must be a JSON object: %w", err)
			}
			return nil
		},
	}
)

// Known providers.
const ProviderB2 = "b2"

// Settings is the resolved configuration.
type Settings struct {
	Provider      string  `json:"provider"`
	ReportStorage string  `json:"report_storage"`
	AccountID     string  `json:"account_id"`
	Prefix        string  `json:"prefix"`
	Pricing       Pricing `json:"pricing"`
	// PricingIsDefault says the operator has not saved a price table, so the
	// numbers are ours and carry the date we read them. The page must say so:
	// an estimate computed from somebody else's guess at a price is exactly
	// the kind of number that gets quoted back later as fact.
	PricingIsDefault bool `json:"pricing_is_default"`
}

// Configured reports whether there is enough to fetch anything.
func (s Settings) Configured() bool {
	return s.Provider != "" && s.ReportStorage != ""
}

// StorageLookup resolves a storage NAME to an initialized driver. The server
// has one; tests pass their own.
type StorageLookup func(ctx context.Context, name string) (storage.Driver, error)

// Service answers the admin surface's questions, with a small cache in front
// of the provider so opening a page does not re-download a month of reports.
type Service struct {
	Get     dbsetting.Getter
	Lookup  StorageLookup
	TTL     time.Duration
	nowFunc func() time.Time

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	at   time.Time
	days []Day
}

// NewService constructs the service. ttl <= 0 defaults to an hour, which is
// far below the daily cadence of the report and far above a page refresh.
func NewService(get dbsetting.Getter, lookup StorageLookup, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &Service{Get: get, Lookup: lookup, TTL: ttl, cache: map[string]cached{}}
}

func (s *Service) now() time.Time {
	if s.nowFunc != nil {
		return s.nowFunc()
	}
	return time.Now()
}

// Settings resolves the stored configuration.
func (s *Service) Settings(ctx context.Context) Settings {
	out := Settings{
		Provider:      SettingProvider.Resolve(ctx, s.Get),
		ReportStorage: SettingReportStorage.Resolve(ctx, s.Get),
		AccountID:     SettingAccountID.Resolve(ctx, s.Get),
		Prefix:        SettingPrefix.Resolve(ctx, s.Get),
	}
	raw := SettingPricing.Resolve(ctx, s.Get)
	if strings.TrimSpace(raw) == "" {
		out.Pricing, out.PricingIsDefault = defaultPricing(out.Provider), true
		return out
	}
	var p Pricing
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		// A stored table we cannot read must not silently become "free".
		out.Pricing, out.PricingIsDefault = defaultPricing(out.Provider), true
		return out
	}
	out.Pricing = p
	return out
}

func defaultPricing(provider string) Pricing {
	switch provider {
	case ProviderB2:
		return BackblazeB2()
	default:
		return Pricing{Currency: "USD"}
	}
}

// Report is everything the admin page renders.
type Report struct {
	Settings Settings `json:"settings"`
	// Configured is false when nothing is set up yet. The page then shows the
	// form, not an empty chart that looks like "you spent nothing".
	Configured bool   `json:"configured"`
	From       string `json:"from"`
	To         string `json:"to"`
	Days       []Day  `json:"days"`
	Totals     Totals `json:"totals"`
	Cost       Cost   `json:"cost"`
	// Notes are things the operator should know about THIS answer: a range
	// with no published report yet, a cached result, a provider that reports
	// nothing for some days.
	Notes []string `json:"notes,omitempty"`
}

// Report fetches (or serves from cache) and summarizes a date range.
func (s *Service) Report(ctx context.Context, from, to time.Time) (Report, error) {
	set := s.Settings(ctx)
	rep := Report{
		Settings:   set,
		Configured: set.Configured(),
		From:       from.UTC().Format("2006-01-02"),
		To:         to.UTC().Format("2006-01-02"),
	}
	if !set.Configured() {
		return rep, nil
	}

	days, fresh, err := s.days(ctx, set, from, to)
	if err != nil {
		return rep, err
	}
	rep.Days = days
	rep.Totals = SumBuckets(days)
	rep.Cost = Estimate(rep.Totals, set.Pricing)
	if !fresh {
		rep.Notes = append(rep.Notes, "served from cache")
	}
	if len(days) == 0 {
		rep.Notes = append(rep.Notes,
			"the provider has published no report for this range yet — B2 writes each day's file the following day")
	}
	if set.PricingIsDefault {
		rep.Notes = append(rep.Notes,
			"prices are filex's defaults ("+set.Pricing.Note+"), not your contract — check them against your invoice")
	}
	return rep, nil
}

func (s *Service) days(ctx context.Context, set Settings, from, to time.Time) ([]Day, bool, error) {
	key := fmt.Sprintf("%s|%s|%s|%s", set.Provider, set.ReportStorage,
		from.UTC().Format("2006-01-02"), to.UTC().Format("2006-01-02"))

	s.mu.Lock()
	if c, ok := s.cache[key]; ok && s.now().Sub(c.at) < s.TTL {
		s.mu.Unlock()
		return c.days, false, nil
	}
	s.mu.Unlock()

	src, err := s.source(ctx, set)
	if err != nil {
		return nil, false, err
	}
	days, err := src.Fetch(ctx, from, to)
	if err != nil {
		return nil, false, err
	}

	s.mu.Lock()
	s.cache[key] = cached{at: s.now(), days: days}
	s.mu.Unlock()
	return days, true, nil
}

// source builds the provider source from the settings.
func (s *Service) source(ctx context.Context, set Settings) (Source, error) {
	if s.Lookup == nil {
		return nil, errors.New("usage: no storage lookup wired")
	}
	drv, err := s.Lookup(ctx, set.ReportStorage)
	if err != nil {
		return nil, fmt.Errorf("usage: report storage %q: %w", set.ReportStorage, err)
	}
	switch set.Provider {
	case ProviderB2:
		return B2{Driver: drv, AccountID: set.AccountID, Prefix: set.Prefix}, nil
	default:
		return nil, fmt.Errorf("usage: unknown provider %q", set.Provider)
	}
}

// Invalidate drops the cache — called when the settings change, so an
// operator who fixes an account id sees the effect on the next load rather
// than an hour later.
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.cache = map[string]cached{}
	s.mu.Unlock()
}
