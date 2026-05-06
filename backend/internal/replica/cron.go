package replica

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/robfig/cron/v3"
)

// CronScheduler wraps robfig/cron with a Reload primitive so the
// admin UI can change the cron spec at runtime without a restart.
type CronScheduler struct {
	mu       sync.Mutex
	cron     *cron.Cron
	entryID  cron.EntryID
	currentS string

	svc *Service
}

// NewCronScheduler binds a scheduler to the replica Service.
func NewCronScheduler(svc *Service) *CronScheduler {
	return &CronScheduler{
		svc:  svc,
		cron: cron.New(),
	}
}

// Start launches the underlying cron loop. Must be called once before
// the first Reload — Reload is a no-op until Start.
func (c *CronScheduler) Start() {
	c.cron.Start()
}

// Stop blocks until any in-flight job returns and the scheduler tear
// down completes.
func (c *CronScheduler) Stop() {
	ctx := c.cron.Stop()
	<-ctx.Done()
}

// Reload re-reads the replica_settings row and reschedules. Empty
// cron spec or report_enabled=false removes any active schedule.
//
// Safe to call concurrently; the mutex serializes spec swaps so the
// scheduler never has two entries for the same job.
func (c *CronScheduler) Reload(ctx context.Context) error {
	st, err := c.svc.store.GetReplicaSettings(ctx)
	if err != nil {
		return fmt.Errorf("replica cron reload: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Tear down any prior entry before installing a new one.
	if c.entryID != 0 {
		c.cron.Remove(c.entryID)
		c.entryID = 0
		c.currentS = ""
	}

	if !st.ReportEnabled || st.ReportCron == "" {
		slog.Info("replica cron: disabled or empty spec", slog.Bool("enabled", st.ReportEnabled), slog.String("spec", st.ReportCron))
		return nil
	}
	id, err := c.cron.AddFunc(st.ReportCron, func() {
		jobCtx, cancel := context.WithTimeout(context.Background(), 5*60*1e9) // 5min
		defer cancel()
		if err := c.svc.GenerateReport(jobCtx); err != nil {
			slog.Warn("replica cron: GenerateReport failed", slog.String("err", err.Error()))
		}
	})
	if err != nil {
		return fmt.Errorf("replica cron: invalid spec %q: %w", st.ReportCron, err)
	}
	c.entryID = id
	c.currentS = st.ReportCron
	slog.Info("replica cron: scheduled", slog.String("spec", st.ReportCron))
	return nil
}
