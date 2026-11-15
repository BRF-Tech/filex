package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/brf-tech/filex/backend/internal/watchproto"
)

// tickEvery is how often the sender looks at the queue.
const tickEvery = 200 * time.Millisecond

// runAgent watches cfg.Root and reports to filex until ctx ends.
//
// The order of things, and why:
//
//  1. The watches go in first, over the whole tree. Then the first report
//     asks for a resync: whatever changed while no agent was running is
//     unknown, and filex brings the storage up to date once.
//  2. A burst of changes is sent when it has been quiet for `quiet` (or has
//     lasted `max_wait`), as many reports as it takes, each numbered (seq).
//  3. A report filex did not take is sent again, under the same number,
//     after a growing pause (1 s doubling to 5 min). The folders that arrive
//     meanwhile wait in the bounded queue; past its bound they become a
//     resync. A report that may have been lost on the way (no answer, a 5xx:
//     filex may have restarted) makes the next one a resync too.
//  4. With nothing to say, a heartbeat every `heartbeat` keeps the key's
//     "last seen" current on the storage page.
//  5. A subtree the operating system would not let us watch is asked to be
//     re-read every `subtree_resync`.
func runAgent(ctx context.Context, cfg *Config, log *slog.Logger) error {
	q := newQueue(cfg.QueueMax)
	tw, err := newTreeWatcher(cfg.Root, q, log)
	if err != nil {
		return err
	}
	defer tw.close()
	cl, err := newClient(cfg)
	if err != nil {
		return err
	}
	if cfg.InsecureSkipTLSVerify {
		log.Warn("filex's certificate is not checked (insecure_skip_tls_verify): use it only for a self-signed certificate on a network you trust")
	}

	started := time.Now()
	tw.addTree(cfg.Root, false)
	q.markResync(time.Now())
	log.Info("filex-watch started",
		slog.String("version", version), slog.String("server", cfg.Server),
		slog.Int64("storage", cfg.Storage), slog.String("root", cfg.Root),
		slog.Int("watched_folders", len(tw.w.WatchList())), slog.Duration("took", time.Since(started)))

	stop := make(chan struct{})
	defer close(stop)
	go tw.loop(stop)

	s := &sender{cfg: cfg, cl: cl, q: q, log: log, info: agentInfo(cfg)}
	tick := time.NewTicker(tickEvery)
	defer tick.Stop()
	resyncTick := time.NewTicker(time.Duration(cfg.SubtreeResync))
	defer resyncTick.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("filex-watch stopped")
			return nil
		case now := <-resyncTick.C:
			for _, p := range tw.unwatchedList() {
				q.addSubtree(p, now)
			}
		case now := <-tick.C:
			if n := q.takeOverflows(); n > 0 {
				log.Warn("more folders changed than the queue holds (queue_max); asking filex for a full resync instead")
			}
			s.step(ctx, now)
		}
	}
}

// sender is the delivering half: one report in flight at a time, retried
// under the same number until filex takes it.
type sender struct {
	cfg  *Config
	cl   *client
	q    *queue
	log  *slog.Logger
	info watchproto.Agent

	seq uint64
	// inflight is the report being delivered (nil: none), seq its number.
	inflight *batch
	inSeq    uint64
	// needResync: a report may have been lost; the next one asks for a resync.
	needResync bool
	backoff    time.Duration
	retryAt    time.Time
	lastSent   time.Time
	// failing: the last attempt failed (its first success is logged).
	failing bool
	// instance is the filex process that took the last report
	// (watchproto.Answer.Instance); "" until one answered.
	instance string
}

// step sends what is due, if anything.
func (s *sender) step(ctx context.Context, now time.Time) {
	if now.Before(s.retryAt) {
		return
	}
	if s.inflight == nil {
		var b batch
		switch {
		case s.q.due(now, time.Duration(s.cfg.Quiet), time.Duration(s.cfg.MaxWait)):
			b = s.q.take(reportLimit)
		case now.Sub(s.lastSent) >= time.Duration(s.cfg.Heartbeat):
			// a heartbeat: nothing changed
		default:
			return
		}
		s.seq++
		s.inflight, s.inSeq = &b, s.seq
	}
	rep := watchproto.Report{
		Protocol: watchproto.Protocol,
		Agent:    s.info,
		Seq:      s.inSeq,
		Folders:  s.inflight.folders,
		Subtrees: s.inflight.subtrees,
		Resync:   s.inflight.resync || s.needResync,
	}
	ans, err := s.cl.send(ctx, rep)
	if err == nil {
		if s.failing {
			s.log.Info("filex takes reports again", slog.Uint64("seq", rep.Seq))
		}
		s.log.Debug("report taken", slog.Uint64("seq", ans.Seq), slog.Int("folders", len(rep.Folders)),
			slog.Int("subtrees", len(rep.Subtrees)), slog.Bool("resync", rep.Resync),
			slog.Int("queued", ans.Queued), slog.Int("ignored", ans.Ignored))
		s.inflight, s.needResync, s.backoff, s.failing = nil, false, 0, false
		s.retryAt, s.lastSent = time.Time{}, now
		s.noteInstance(ans.Instance, rep.Resync, now)
		return
	}
	if ctx.Err() != nil {
		return
	}
	se := asSendError(err)
	if se.badReport() {
		// filex refused this report as such: sending it again cannot help.
		// It is dropped, and the next report asks for a resync instead.
		s.log.Error("filex refused a report; asking for a full resync instead", slog.String("err", se.Error()))
		s.inflight = nil
		s.q.markResync(now)
		s.retryAt = now.Add(backoffMin)
		return
	}
	if se.transport() {
		s.needResync = true
	}
	s.backoff = nextBackoff(s.backoff)
	wait := max(jitter(s.backoff), se.RetryAfter)
	s.retryAt = now.Add(wait)
	// Said once when it starts failing, then at the longest pause: an
	// agent that cannot reach filex for a day does not fill a disk with it.
	if !s.failing || s.backoff == backoffMax {
		s.log.Warn("a report was not delivered; trying again", slog.String("err", se.Error()), slog.Duration("in", wait))
	}
	s.failing = true
}

// noteInstance remembers which filex process answered. A new one means filex
// started again since the last answer: folders it had taken but not yet handed
// to its catalogue died with the old process, so the next report asks for one
// resync - unless the report just taken was one already.
func (s *sender) noteInstance(instance string, wasResync bool, now time.Time) {
	if instance == "" {
		return
	}
	if s.instance != "" && instance != s.instance && !wasResync {
		s.log.Info("filex started again since the last report; asking it for a full resync")
		s.q.markResync(now)
	}
	s.instance = instance
}
