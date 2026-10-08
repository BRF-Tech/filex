package notify

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// The notification digest (task #166, migration 00087).
//
// A person chooses which kinds of notification are URGENT; those reach them
// the moment they happen, as before. ⚠ Out of the box EVERY kind is urgent
// (DefaultUrgentEvents): the digest is opt-in, and an install that nobody
// touches tells every notification at once, exactly as before 0.53. An
// administrator may hold kinds by default for their tenant, a person for
// themselves (the owner's decision, 2026-10-06). A kind that is held: the row is
// written as always (the history, the admin list, the audit log and the
// webhooks see each event on its own), but it is QUIET for that person — in
// their list, as read, counted by neither the unread list nor the badge, so
// neither the browser nor the desktop app raises a pop-up for it. When the
// window ends (one minute after the first quiet row, 1-15 set by an
// administrator) ONE row of `notification.digest` is written to that person,
// unread, saying folder by folder what changed — that row is the notification:
// the badge moves by one, the browser and the desktop app raise one pop-up,
// and when a held row asked for an email, one email goes.
//
// ⚠⚠ Why the rows are held by READ and not by delay. Every reader of the bell
// — the bell itself, the browser's pop-up, the desktop's native one, an agent
// through MCP — reads the one feed (GET /api/notifications). Deciding "quiet"
// in that read, in SQL, beside the mute list, is what makes every channel take
// the same digest without a line of channel-specific code, and keeps the badge
// and the list in agreement. A row is never delayed, so nothing a test or an
// integration reads from the list moves.
//
// ⚠⚠ Who may read what. A digest is made from the rows the person's OWN bell
// shows — their own rows, and the broadcasts their bell takes and the per-row
// pass keeps (bell.go, handlers/notifications.go bellJudge) — through a View
// handed in by the HTTP layer. It cannot name a file, or count one, that the
// person could not have read in their bell; another person's or another
// tenant's rows never enter it.
//
// ⚠⚠ Restarts. The window is in the database (notify_digest_state): the
// person's digest point `through_id` (every row at or below it has been told)
// and `due_at` (when the open window ends). A server that stops keeps both,
// and the next one finds the due window and tells it. A digest is written in
// ONE transaction with a compare-and-set of the point, so two servers, two
// tabs or a restart in the middle cannot write it twice; the rows it carries
// are marked read for the person in the same transaction. What a digest sets
// off after the commit — its webhook and its email — is fire-and-forget, like
// every other event's.
//
// ⚠ Nothing is lost when a digest cannot carry a row. A quiet row is a row
// above the point; once the point passes it, a row no digest carried is
// simply unread again and told on its own. So a row a background pass could
// not judge (no View wired) reaches the person the old way, never not at all.

// Digest window bounds, in minutes.
const (
	DigestWindowDefault = 1
	DigestWindowMin     = 1
	DigestWindowMax     = 15
)

const (
	// digestScanMax bounds the rows one digest is made of. A larger backlog
	// is told oldest first, in more than one digest.
	digestScanMax = 2000
	// digestGroupsMax is how many folders a digest names; the rest are summed.
	digestGroupsMax = 20
	// digestLoopEvery is how often the background pass looks for windows
	// that ended.
	digestLoopEvery = 10 * time.Second
	// digestPersonTTL is how long a person's resolved choice is kept in
	// memory: the bell is polled every 15 seconds.
	digestPersonTTL = 20 * time.Second
)

// DigestConfig switches the digest on (Config.Digest). Nil leaves every
// notification told on its own, as before 0.53 — what a test of something
// else wants.
type DigestConfig struct {
	// MultiTenant reads a person's defaults from their tenant (scope = its
	// provider id); otherwise from the instance (scope 0).
	MultiTenant bool
	// Every overrides how often the background pass runs (tests).
	Every time.Duration
}

// View is how one person's bell reads: which broadcasts it takes (Bell) and
// the per-row pass after the store (Keep; nil reads the broadcasts as stored).
// The HTTP layer's own rule — handlers/notifications.go bellFor and bellJudge.
type View struct {
	Bell Bell
	Keep func(*model.Notification) bool
}

// Viewer answers a person's View without a request (the background pass).
type Viewer func(ctx context.Context, userID int64) (View, error)

// Digests is the digest half of the service. The concrete service implements
// it; a test double of Service does not need to.
type Digests interface {
	// SetViewer hands in the HTTP layer's View of a person, for the
	// background pass. Without one that pass judges a person's own rows only
	// (their broadcasts are then told on their own, see the file header).
	SetViewer(v Viewer)
	// StartDigests runs the background pass until ctx ends or Stop.
	StartDigests(ctx context.Context)
	// FlushDueDigests runs one background pass now and answers how many
	// digests it wrote.
	FlushDueDigests(ctx context.Context) (int, error)
	// SettleDigest tells userID's held notifications through view if their
	// window has ended — or at once, with force (before their choices
	// change). It answers whether it wrote a digest.
	SettleDigest(ctx context.Context, userID int64, view View, force bool) (bool, error)
	// DigestPolicy is the defaults of one scope as they apply (the built-in
	// ones where nothing was saved), and whether any were saved.
	DigestPolicy(ctx context.Context, scope int64) (model.DigestPolicy, bool, error)
	// SaveDigestPolicy replaces the defaults of p.Scope. The window is
	// clamped to DigestWindowMin..DigestWindowMax; Urgent nil restores the
	// built-in list. Unknown event ids are dropped.
	SaveDigestPolicy(ctx context.Context, p model.DigestPolicy) (model.DigestPolicy, error)
	// PersonDigest is what the settings pane shows a person.
	PersonDigest(ctx context.Context, userID int64) (PersonDigestView, error)
	// DigestScope is which scope a person's defaults are read from.
	DigestScope(ctx context.Context, userID int64) int64
	// ForgetDigest drops what is remembered of a person's choice.
	ForgetDigest(userID int64)
}

var _ Digests = (*service)(nil)

// ErrDigestOff answers a digest question on a service that runs without one
// (Config.Digest nil).
var ErrDigestOff = errors.New("notify: the digest is off")

// PersonDigestView is a person's digest as the settings pane draws it.
type PersonDigestView struct {
	// WindowMinutes is how long their non-urgent notifications are held.
	WindowMinutes int `json:"window_minutes"`
	// Urgent is the kinds told to them at once: their own choices over the
	// administrator's defaults.
	Urgent []string `json:"urgent_events"`
	// Defaults is the administrator's urgent list, the one a kind they did
	// not choose follows.
	Defaults []string `json:"default_urgent"`
	// Events is every kind a choice can be made about, and AdminEvents the
	// administrator alerts among them (one switch in the pane).
	Events      []string `json:"events"`
	AdminEvents []string `json:"admin_events"`
}

// AdminAlertEvents are the administrator alerts: the operator alarms and the
// notices about a tenant's own address. One switch in the settings pane.
func AdminAlertEvents() []string {
	out := eventIDs(operatorEvents)
	return append(out, string(EventTenantDomainSuspended), string(EventTenantDomainRestored))
}

// digestEvents is every kind a person may hold for the digest. A kind not
// listed here (an event a later version adds and nobody has decided about)
// is never quiet: it is told at once, the safe direction.
var digestEvents = func() []string {
	out := []string{
		string(EventFileUploaded), string(EventFileUpdated), string(EventFileUploadFailed),
		string(EventFileInfected), string(EventFileDeleted), string(EventFileTrashed), string(EventFileMoved),
		string(EventArchiveCreated), string(EventArchiveExtracted),
		string(EventShareCreated), string(EventDropReceived), string(EventCommentAdded),
		string(EventE2EEscrowUsed), string(EventE2EPasswordChanged),
		string(EventE2ERequestCreated), string(EventE2ERequestDecided),
		string(EventPluginNotice), string(EventAdminTest),
	}
	return append(out, AdminAlertEvents()...)
}()

// DigestEvents is every kind a person may hold for the digest — the catalogue
// a choice, the person's or an administrator's, is made from.
func DigestEvents() []string { return append([]string(nil), digestEvents...) }

// DefaultUrgentEvents is the built-in urgent list: EVERY kind. The owner's
// decision (2026-10-06): "hepsi acil türde kalsın defaultta, admin ya da
// kullanıcı girip kısabilir olsun" - everything stays urgent by default, and
// an administrator (for their tenant) or a person (for themselves) turns kinds
// off to hold them for the digest. So an upgrade changes nobody's
// notifications, and a person whose administrator and who themselves chose
// nothing has no digest point, no window and no extra query on a write.
func DefaultUrgentEvents() []string { return DigestEvents() }

// KnownDigestEvent reports whether a choice can be made about event.
func KnownDigestEvent(event string) bool { return hasString(digestEvents, event) }

func hasString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ClampDigestWindow keeps a window in DigestWindowMin..DigestWindowMax.
func ClampDigestWindow(minutes int) int {
	switch {
	case minutes < DigestWindowMin:
		return DigestWindowMin
	case minutes > DigestWindowMax:
		return DigestWindowMax
	}
	return minutes
}

// digestRuntime is the digest's state in memory: what is remembered of
// people's choices. Everything that must survive a restart is in the database.
type digestRuntime struct {
	cfg DigestConfig

	mu     sync.Mutex
	people map[int64]personDigest
}

// personDigest is one person's resolved choice.
type personDigest struct {
	window time.Duration
	urgent map[string]bool
	// quiet is the kinds this person holds (digestEvents less urgent), in
	// catalogue order. Empty: nothing is held.
	quiet []string
	at    time.Time
}

func (p personDigest) holds(event string) bool { return hasString(p.quiet, event) }

func (s *service) clock() time.Time {
	if s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

// person resolves userID's choice: their own over their scope's defaults over
// the built-in list. ⚠ It fails OPEN: a choice that cannot be read holds
// nothing, so a read error tells everything at once rather than holding a
// security alert back.
func (s *service) person(ctx context.Context, userID int64) personDigest {
	d := s.dig
	now := time.Now()
	d.mu.Lock()
	if p, ok := d.people[userID]; ok && now.Sub(p.at) < digestPersonTTL {
		d.mu.Unlock()
		return p
	}
	d.mu.Unlock()

	pol, _, err := s.DigestPolicy(ctx, s.DigestScope(ctx, userID))
	if err != nil {
		slog.Warn("notify: digest policy unreadable; nothing is held", slog.Int64("user_id", userID), slog.String("err", err.Error()))
		return personDigest{window: time.Minute}
	}
	urgent := make(map[string]bool, len(pol.Urgent))
	for _, e := range pol.Urgent {
		urgent[e] = true
	}
	st, err := s.store.GetNotificationSettings(ctx, userID)
	if err != nil {
		slog.Warn("notify: notification settings unreadable; nothing is held", slog.Int64("user_id", userID), slog.String("err", err.Error()))
		return personDigest{window: time.Minute}
	}
	for e, on := range st.UrgentOverrides() {
		urgent[e] = on
	}
	p := personDigest{window: time.Duration(pol.WindowMinutes) * time.Minute, urgent: urgent, at: now}
	for _, e := range digestEvents {
		if !urgent[e] {
			p.quiet = append(p.quiet, e)
		}
	}
	d.mu.Lock()
	if d.people == nil {
		d.people = map[int64]personDigest{}
	}
	d.people[userID] = p
	d.mu.Unlock()
	return p
}

func (s *service) ForgetDigest(userID int64) {
	if s.dig == nil {
		return
	}
	s.dig.mu.Lock()
	delete(s.dig.people, userID)
	s.dig.mu.Unlock()
}

func (s *service) forgetEverybody() {
	if s.dig == nil {
		return
	}
	s.dig.mu.Lock()
	s.dig.people = map[int64]personDigest{}
	s.dig.mu.Unlock()
}

func (s *service) DigestScope(ctx context.Context, userID int64) int64 {
	if s.dig == nil || !s.dig.cfg.MultiTenant || s.store == nil {
		return 0
	}
	u, err := s.store.GetUser(ctx, userID)
	if err != nil || u == nil || u.ProviderID == nil {
		return 0
	}
	return *u.ProviderID
}

func (s *service) DigestPolicy(ctx context.Context, scope int64) (model.DigestPolicy, bool, error) {
	out := model.DigestPolicy{Scope: scope, WindowMinutes: DigestWindowDefault, Urgent: DefaultUrgentEvents()}
	p, err := s.store.GetDigestPolicy(ctx, scope)
	if err != nil {
		return out, false, err
	}
	if p == nil {
		return out, false, nil
	}
	out.WindowMinutes = ClampDigestWindow(p.WindowMinutes)
	out.UpdatedAt = p.UpdatedAt
	if p.Urgent != nil {
		out.Urgent = knownOnly(p.Urgent)
	}
	return out, true, nil
}

func (s *service) SaveDigestPolicy(ctx context.Context, p model.DigestPolicy) (model.DigestPolicy, error) {
	p.WindowMinutes = ClampDigestWindow(p.WindowMinutes)
	if p.Urgent != nil {
		p.Urgent = knownOnly(p.Urgent)
	}
	if err := s.store.SaveDigestPolicy(ctx, &p); err != nil {
		return p, err
	}
	s.forgetEverybody()
	out, _, err := s.DigestPolicy(ctx, p.Scope)
	return out, err
}

// knownOnly keeps the kinds a choice can be made about, once each, never nil.
func knownOnly(list []string) []string {
	out := []string{}
	for _, e := range list {
		e = strings.TrimSpace(e)
		if KnownDigestEvent(e) && !hasString(out, e) {
			out = append(out, e)
		}
	}
	return out
}

func (s *service) PersonDigest(ctx context.Context, userID int64) (PersonDigestView, error) {
	if s.dig == nil {
		return PersonDigestView{}, ErrDigestOff
	}
	pol, _, err := s.DigestPolicy(ctx, s.DigestScope(ctx, userID))
	if err != nil {
		return PersonDigestView{}, err
	}
	s.ForgetDigest(userID)
	p := s.person(ctx, userID)
	out := PersonDigestView{
		WindowMinutes: int(p.window / time.Minute),
		Defaults:      pol.Urgent,
		Events:        DigestEvents(),
		AdminEvents:   AdminAlertEvents(),
		Urgent:        []string{},
	}
	for _, e := range digestEvents {
		if p.urgent[e] {
			out.Urgent = append(out.Urgent, e)
		}
	}
	return out, nil
}

func (s *service) SetViewer(v Viewer) {
	s.mu.Lock()
	s.viewer = v
	s.mu.Unlock()
}

// viewOf is the View the background pass judges a person's rows through.
// Without a Viewer — or when it fails — only the person's own rows: their
// broadcasts are then told on their own (see the file header).
func (s *service) viewOf(ctx context.Context, userID int64) View {
	s.mu.RLock()
	v := s.viewer
	s.mu.RUnlock()
	if v != nil {
		view, err := v(ctx, userID)
		if err == nil {
			return view
		}
		slog.Warn("notify: digest view unavailable; own rows only", slog.Int64("user_id", userID), slog.String("err", err.Error()))
	}
	return View{Bell: MemberBell, Keep: func(n *model.Notification) bool { return n.UserID != nil }}
}

// holdFor is the person's digest when they hold this event: the addressee of
// a row, never a broadcast's readers (their windows are opened by their own
// reads). nil when the digest is off or the event is told at once.
//
// ⚠ Their digest point is made BEFORE the row is written, so a person's first
// held row is above it.
func (s *service) holdFor(ctx context.Context, e Event) *personDigest {
	if s.dig == nil || e.UserID == nil || e.Event == EventNotificationDigest {
		return nil
	}
	p := s.person(ctx, *e.UserID)
	if !p.holds(string(e.Event)) {
		return nil
	}
	if _, err := s.store.EnsureDigestState(ctx, *e.UserID); err != nil {
		slog.Warn("notify: digest point not made; told at once", slog.Int64("user_id", *e.UserID), slog.String("err", err.Error()))
		return nil
	}
	return &p
}

// openWindow records when the person's window ends, unless one that ends
// earlier is open already.
func (s *service) openWindow(ctx context.Context, userID int64, p *personDigest) {
	if err := s.store.SetDigestDue(ctx, userID, s.clock().Add(p.window), false); err != nil {
		slog.Warn("notify: digest window not recorded", slog.Int64("user_id", userID), slog.String("err", err.Error()))
	}
}

// quietFilter is the reader's digest filter for a read of their bell, after
// telling a window that has ended (settle). nil when nothing is quiet for
// them — the read is then the one it always was.
func (s *service) quietFilter(ctx context.Context, userID int64, view View, settle bool) *model.DigestFilter {
	if s.dig == nil {
		return nil
	}
	if settle {
		if _, err := s.SettleDigest(ctx, userID, view, false); err != nil {
			slog.Warn("notify: digest not settled", slog.Int64("user_id", userID), slog.String("err", err.Error()))
		}
	}
	p := s.person(ctx, userID)
	if len(p.quiet) == 0 {
		return nil
	}
	through, _, found, err := s.store.DigestState(ctx, userID)
	if err != nil || !found {
		return nil
	}
	return &model.DigestFilter{After: through, Quiet: p.quiet}
}

// quietRead gives a quiet row the read state it has for its reader: read, as
// of when it was made. It is in the list; the digest is what is unread.
func quietRead(rows []*model.Notification, f *model.DigestFilter) {
	for _, n := range rows {
		if n.ReadAt == nil && f.IsQuiet(n) {
			t := n.CreatedAt
			n.ReadAt = &t
		}
	}
}

// errDigestTold ends a digest's transaction when another server or request
// told the same rows first.
var errDigestTold = errors.New("notify: digest already told")

func (s *service) SettleDigest(ctx context.Context, userID int64, view View, force bool) (bool, error) {
	if s.dig == nil || s.store == nil {
		return false, nil
	}
	p := s.person(ctx, userID)
	through, due, found, err := s.store.DigestState(ctx, userID)
	if err != nil {
		return false, err
	}
	now := s.clock()
	if !found {
		if len(p.quiet) == 0 {
			return false, nil
		}
		// A fresh point holds nothing yet.
		_, err := s.store.EnsureDigestState(ctx, userID)
		return false, err
	}
	if len(p.quiet) == 0 && !force {
		if due != nil {
			_ = s.store.ClearDigestDue(ctx, userID, now)
		}
		return false, nil
	}
	// A window that ends later is not looked at again before it does.
	if !force && due != nil && due.After(now) {
		return false, nil
	}

	uid := userID
	muted := s.mutedOf(ctx, userID)
	f := bellFilter(&uid, view.Bell)
	f.Digest = &model.DigestFilter{After: through, Quiet: p.quiet, Only: true}
	_, total, err := s.store.ListNotifications(ctx, &uid, false, muted, hiddenBodies(), f, 1, 0)
	if err != nil {
		return false, err
	}
	if total == 0 {
		if force {
			if upTo, err := s.store.NewestNotificationID(ctx); err == nil && upTo > through {
				_, _ = s.store.AdvanceDigest(ctx, userID, through, upTo, now)
			}
		} else if due != nil {
			_ = s.store.ClearDigestDue(ctx, userID, now)
		}
		return false, nil
	}

	// The rows this digest may carry stop here: a row written while it is
	// being made waits for the next window rather than shifting the pages.
	upTo, err := s.store.NewestNotificationID(ctx)
	if err != nil {
		return false, err
	}
	f.Digest.UpTo = upTo
	_, total, err = s.store.ListNotifications(ctx, &uid, false, muted, hiddenBodies(), f, 1, 0)
	if err != nil || total == 0 {
		return false, err
	}
	// The oldest quiet row opened the window (the list is newest first).
	oldest, _, err := s.store.ListNotifications(ctx, &uid, false, muted, hiddenBodies(), f, 1, int(total-1))
	if err != nil || len(oldest) == 0 {
		return false, err
	}
	ends := oldest[0].CreatedAt.UTC().Add(p.window)
	if !force && ends.After(now) {
		// Not over yet: say when it is, so the background pass tells it
		// should the person stop reading. A window recorded as ending
		// earlier (the administrator lengthened it since) is corrected.
		exact := due != nil && due.Before(ends)
		if err := s.store.SetDigestDue(ctx, userID, ends, exact); err != nil {
			return false, err
		}
		return false, nil
	}

	// The window, oldest first when it is larger than one digest carries.
	start := 0
	if total > digestScanMax {
		start = int(total) - digestScanMax
	}
	var scanned []*model.Notification
	for off := start; off < int(total); off += storePageMax {
		rows, _, err := s.store.ListNotifications(ctx, &uid, false, muted, hiddenBodies(), f, storePageMax, off)
		if err != nil {
			return false, err
		}
		scanned = append(scanned, rows...)
		if len(rows) < storePageMax {
			break
		}
	}
	to := upTo
	if start > 0 {
		to = 0
		for _, n := range scanned {
			to = max(to, n.ID)
		}
	}
	var kept []*model.Notification
	for _, n := range scanned {
		if !sanitizeRow(n) {
			continue
		}
		if n.UserID == nil && view.Keep != nil && !view.Keep(n) {
			continue
		}
		n.HydrateTarget()
		kept = append(kept, n)
	}
	// Oldest first: the order the digest tells them in.
	sort.Slice(kept, func(i, j int) bool { return kept[i].ID < kept[j].ID })
	made, err := s.tell(ctx, userID, p, through, to, kept, now)
	if err != nil {
		return false, err
	}
	if start > 0 {
		// More than one digest's worth: the rest is told at the next pass.
		_ = s.store.SetDigestDue(ctx, userID, now, false)
	}
	return made, nil
}

// mutedOf is the person's mute list: a kind they muted is not in their bell,
// so it is in no digest either.
func (s *service) mutedOf(ctx context.Context, userID int64) []string {
	st, err := s.store.GetNotificationSettings(ctx, userID)
	if err != nil || st == nil {
		return nil
	}
	return st.MutedList()
}

// tell writes the digest of kept, moves the person's point from `from` to `to`
// and marks the rows read for them — in one transaction, after a
// compare-and-set on the point, so it happens once. Then the digest goes to
// the webhooks that asked for it, and an email when a row asked for one.
func (s *service) tell(ctx context.Context, userID int64, p personDigest, from, to int64, kept []*model.Notification, now time.Time) (bool, error) {
	var (
		ev    Event
		input *model.NotificationInput
	)
	silenced := false
	if st, err := s.store.GetNotificationSettings(ctx, userID); err == nil && st != nil {
		silenced = !st.InAppEnabled
	}
	if len(kept) > 0 && !silenced {
		var ok bool
		var err error
		ev, input, ok, err = s.prepare(ctx, s.digestEvent(ctx, userID, kept, now))
		if err != nil {
			return false, err
		}
		if !ok {
			input = nil
		}
	}
	var own, broadcasts []int64
	for _, n := range kept {
		if n.UserID != nil {
			own = append(own, n.ID)
		} else {
			broadcasts = append(broadcasts, n.ID)
		}
	}
	var digestID int64
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		ok, err := s.store.AdvanceDigest(ctx, userID, from, to, now)
		if err != nil {
			return err
		}
		if !ok {
			return errDigestTold
		}
		if input != nil {
			id, err := s.store.InsertNotification(ctx, input)
			if err != nil {
				return err
			}
			digestID = id
		}
		if err := s.store.MarkOwnNotificationsRead(ctx, userID, own); err != nil {
			return err
		}
		return s.store.MarkBroadcastsRead(ctx, userID, broadcasts)
	})
	if errors.Is(err, errDigestTold) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if digestID > 0 {
		s.dispatch(digestID, ev)
		// The digest row is the notification: it reaches the person's devices
		// as it reaches their bell (push.go).
		s.wakePush(&userID)
	}
	if len(kept) > 0 {
		s.mailDigest(ctx, userID, kept, p)
	}
	// The window after this one, for the person's own rows written meanwhile.
	if next, err := s.store.OldestQuietOwn(ctx, userID, to, p.quiet); err == nil && next != nil {
		_ = s.store.SetDigestDue(ctx, userID, next.Add(p.window), false)
	}
	return digestID > 0, nil
}

// digestGroup is one folder of a digest: where, and how many of each kind.
type digestGroup struct {
	Storage string `json:"storage,omitempty"`
	Path    string `json:"path"`
	Name    string `json:"name,omitempty"`
	// Encrypted: the folder is inside an end-to-end encrypted folder whose
	// names are encrypted — its name here is ciphertext, and no reader prints
	// it (E2ERoot says which encrypted folder; the bell may know the name).
	Encrypted bool           `json:"encrypted,omitempty"`
	E2ERoot   string         `json:"e2e_root,omitempty"`
	Count     int            `json:"count"`
	Counts    map[string]int `json:"counts"`
	// Parts is Counts as the line says it (partsOf).
	Parts []digestPart `json:"parts"`

	first int // order of first appearance
}

// rowPlaceMeta is what a stored row says about where it is.
type rowPlaceMeta struct {
	Node *struct {
		StorageID int64  `json:"storage_id"`
		Path      string `json:"path"`
	} `json:"node"`
	Target *struct {
		Kind    string `json:"kind"`
		Storage string `json:"storage"`
		Path    string `json:"path"`
	} `json:"target"`
	E2ERoot string `json:"e2e_root"`
	Mail    *Mail  `json:"mail"`
}

// parentOf is the folder a storage-relative path is in ("" for the root).
func parentOf(p string) string {
	clean := strings.Trim(strings.ReplaceAll(p, "\\", "/"), "/")
	d := path.Dir(clean)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

// rowFolder places a row in a folder: the folder of the file a click opens,
// the folder itself, or where a trashed item came from. ok is false for a
// row that names no folder (an alarm, an app's notice about a list).
func (s *service) rowFolder(ctx context.Context, n *model.Notification, names map[int64]string) (storage, folder string, meta rowPlaceMeta, ok bool) {
	_ = json.Unmarshal(n.MetaJSON, &meta)
	if t := meta.Target; t != nil && t.Storage != "" {
		switch t.Kind {
		case string(TargetFile), string(TargetTrash):
			return t.Storage, parentOf(t.Path), meta, true
		case string(TargetDir):
			return t.Storage, strings.Trim(t.Path, "/"), meta, true
		}
	}
	if nd := meta.Node; nd != nil && nd.StorageID != 0 && strings.Trim(nd.Path, "/") != "" {
		name, seen := names[nd.StorageID]
		if !seen {
			if st, err := s.store.GetStorage(ctx, nd.StorageID); err == nil && st != nil {
				name = st.Name
			}
			names[nd.StorageID] = name
		}
		if name != "" {
			return name, parentOf(nd.Path), meta, true
		}
	}
	return "", "", meta, false
}

// belowE2ERoot reports whether folder (in storage) is strictly inside the
// encrypted folder root ("<storage>://<path>"): its own name is ciphertext.
func belowE2ERoot(root, storage, folder string) bool {
	i := strings.Index(root, "://")
	if i < 0 || root[:i] != storage {
		return false
	}
	r := strings.Trim(root[i+3:], "/")
	f := strings.Trim(folder, "/")
	if r == "" {
		return f != ""
	}
	return strings.HasPrefix(f, r+"/")
}

// digestFolders sums kept (oldest first) folder by folder: the folders, the
// most changed first and at most digestGroupsMax of them, how many more there
// were and how many rows they held, and the counts of the rows that name no
// folder. The ONE grouping: the row, its webhook and the email say the same.
func (s *service) digestFolders(ctx context.Context, kept []*model.Notification) (list []*digestGroup, other map[string]int, moreFolders, moreCount int) {
	names := map[int64]string{}
	groups := map[string]*digestGroup{}
	other = map[string]int{}
	for i, n := range kept {
		storage, folder, meta, ok := s.rowFolder(ctx, n, names)
		if !ok {
			other[n.Event]++
			continue
		}
		key := storage + "\x00" + folder
		g := groups[key]
		if g == nil {
			name := path.Base(folder)
			if folder == "" {
				name = storage
			}
			g = &digestGroup{Storage: storage, Path: folder, Name: name, Counts: map[string]int{}, first: i}
			if meta.E2ERoot != "" && belowE2ERoot(meta.E2ERoot, storage, folder) {
				g.Encrypted, g.E2ERoot, g.Name = true, meta.E2ERoot, ""
			}
			groups[key] = g
		}
		g.Count++
		g.Counts[n.Event]++
	}
	list = make([]*digestGroup, 0, len(groups))
	for _, g := range groups {
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Count != list[j].Count {
			return list[i].Count > list[j].Count
		}
		return list[i].first < list[j].first
	})
	if len(list) > digestGroupsMax {
		for _, g := range list[digestGroupsMax:] {
			moreFolders++
			moreCount += g.Count
		}
		list = list[:digestGroupsMax]
	}
	for _, g := range list {
		g.Parts = partsOf(g.Counts)
	}
	return list, other, moreFolders, moreCount
}

// digestEvent is the digest of kept (oldest first) for userID.
func (s *service) digestEvent(ctx context.Context, userID int64, kept []*model.Notification, now time.Time) Event {
	severity := SeverityInfo
	for _, n := range kept {
		severity = louder(severity, Severity(n.Severity))
	}
	list, other, moreFolders, moreCount := s.digestFolders(ctx, kept)
	first, last := kept[0], kept[len(kept)-1]
	meta := map[string]any{
		"count":        len(kept),
		"groups":       list,
		"window_start": first.CreatedAt.UTC().Format(time.RFC3339),
		"window_end":   last.CreatedAt.UTC().Format(time.RFC3339),
		"recipient":    map[string]any{"id": userID},
	}
	if len(other) > 0 {
		meta["other"] = other
		meta["other_parts"] = partsOf(other)
	}
	if moreFolders > 0 {
		meta["more_folders"] = moreFolders
		meta["more_count"] = moreCount
	}
	ev := Event{
		Event:    EventNotificationDigest,
		Severity: severity,
		Meta:     meta,
		TS:       now,
		UserID:   &userID,
		Target:   &Target{Kind: TargetNone},
	}
	if len(kept) == 1 {
		// A digest of one says what that one says and opens what it opens.
		only := kept[0]
		meta["item"] = map[string]any{"id": only.ID, "event": only.Event, "title": only.Title, "body": only.Body, "meta": json.RawMessage(only.MetaJSON)}
		if t := only.Target; t != nil {
			tt := *t
			ev.Target = &tt
		}
	} else if len(list) == 1 && len(other) == 0 && !list[0].Encrypted {
		// One folder: a click opens it.
		ev.Target = &Target{Kind: TargetDir, Storage: list[0].Storage, Path: list[0].Path}
	}
	// The row's own title and body - what a reader falls back on, and what a
	// webhook gets - are the digest said in the instance's language, by the
	// same code every reader's words come from (say.go).
	said := SayEvent(srvtext.Pick(), ev)
	ev.Title, ev.Body = said.Title, said.Body
	return ev
}

// louder is the more serious of two severities.
func louder(a, b Severity) Severity {
	rank := map[Severity]int{SeverityInfo: 0, SeverityWarning: 1, SeverityError: 2, SeverityCritical: 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// digestPart is one phrase of a digest line: a catalogue key (the part after
// `server.notify.digest.`) and how many. The readers phrase it in their own
// language; the server says which phrase, so no reader needs a copy of which
// kinds are administrator alerts.
type digestPart struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// digestPartKey is the catalogue key of one kind's count in a digest. Every
// administrator alert shares one ("3 administrator alerts"); a kind the
// catalogue does not phrase is "other".
func digestPartKey(event string) string {
	switch {
	case hasString(AdminAlertEvents(), event):
		return "admin"
	case KnownDigestEvent(event) && event != string(EventAdminTest):
		return strings.ReplaceAll(event, ".", "_")
	}
	return "other"
}

// partsOf is counts by kind as phrases, in catalogue order, kinds that share a
// phrase summed.
func partsOf(counts map[string]int) []digestPart {
	byKey := map[string]int{}
	var order []string
	add := func(event string, n int) {
		k := digestPartKey(event)
		if _, seen := byKey[k]; !seen {
			order = append(order, k)
		}
		byKey[k] += n
	}
	for _, e := range digestEvents {
		if n := counts[e]; n > 0 {
			add(e, n)
		}
	}
	rest := make([]string, 0)
	for e := range counts {
		if !KnownDigestEvent(e) {
			rest = append(rest, e)
		}
	}
	sort.Strings(rest)
	for _, e := range rest {
		add(e, counts[e])
	}
	out := make([]digestPart, 0, len(order))
	for _, k := range order {
		out = append(out, digestPart{Key: k, Count: byKey[k]})
	}
	return out
}

// mailDigest emails the digest when a row it carries asked for an email
// (Event.Mail) - in the person's language, folder by folder, in the words the
// bell says it. Off the caller's
// path, counted with the deliveries so Stop waits for it.
func (s *service) mailDigest(ctx context.Context, userID int64, kept []*model.Notification, p personDigest) {
	if s.mail == nil {
		return
	}
	link := ""
	wanted := false
	for _, n := range kept {
		var m rowPlaceMeta
		if json.Unmarshal(n.MetaJSON, &m) == nil && m.Mail != nil {
			wanted = true
			if link == "" {
				link = m.Mail.Link
			}
		}
	}
	if !wanted {
		return
	}
	u, err := s.store.GetUser(ctx, userID)
	if err != nil || u == nil || strings.TrimSpace(u.Email) == "" {
		return
	}
	lang := PersonLang(u)
	// ⚠ The bell's own words: the digest row said in the person's language
	// (say.go) - its title is the subject, its lines stand one under the
	// other. The email adds only its frame: when, and how to make a kind
	// urgent.
	said := SayEvent(lang, s.digestEvent(ctx, userID, kept, s.clock()))
	subject := said.Title
	body := srvtext.Text(lang, "server.mail.digest.intro", srvtext.Vars{"minutes": strconv.Itoa(int(p.window / time.Minute))}) +
		"\n\n" + strings.Join(said.Lines, "\n")
	if link != "" {
		body += "\n\n" + link
	}
	body += "\n\n" + srvtext.Text(lang, "server.mail.digest.footer", nil)
	to := u.Email
	if !s.beginDelivery() {
		return
	}
	go func() {
		defer s.endDelivery()
		if err := s.mail(context.WithoutCancel(ctx), lang, to, subject, body); err != nil {
			slog.Debug("notify: digest email not sent", slog.Int64("user_id", userID), slog.String("err", err.Error()))
		}
	}()
}

func (s *service) FlushDueDigests(ctx context.Context) (int, error) {
	if s.dig == nil {
		return 0, nil
	}
	ids, err := s.store.DueDigests(ctx, s.clock(), 100)
	if err != nil {
		return 0, err
	}
	made := 0
	for _, uid := range ids {
		select {
		case <-s.stopCh:
			return made, nil
		default:
		}
		ok, err := s.SettleDigest(ctx, uid, s.viewOf(ctx, uid), false)
		if err != nil {
			slog.Warn("notify: digest not told", slog.Int64("user_id", uid), slog.String("err", err.Error()))
			continue
		}
		if ok {
			made++
		}
	}
	return made, nil
}

func (s *service) StartDigests(ctx context.Context) {
	if s.dig == nil {
		return
	}
	every := s.dig.cfg.Every
	if every <= 0 {
		every = digestLoopEvery
	}
	s.inflightMu.Lock()
	if s.stopped || s.loopDone != nil {
		s.inflightMu.Unlock()
		return
	}
	done := make(chan struct{})
	s.loopDone = done
	s.inflightMu.Unlock()
	go func() {
		defer close(done)
		t := time.NewTicker(every)
		defer t.Stop()
		// The windows a stopped server left open are told at the first
		// pass, not one interval later.
		if _, err := s.FlushDueDigests(ctx); err != nil {
			slog.Warn("notify: digest pass", slog.String("err", err.Error()))
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case <-t.C:
				if _, err := s.FlushDueDigests(ctx); err != nil {
					slog.Warn("notify: digest pass", slog.String("err", err.Error()))
				}
			}
		}
	}()
}
