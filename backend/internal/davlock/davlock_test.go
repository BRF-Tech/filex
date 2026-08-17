package davlock_test

import (
	"os"
	"testing"
	"time"

	"golang.org/x/net/webdav"

	"github.com/brf-tech/filex/backend/internal/davlock"
)

func infinite(root string, zeroDepth bool) webdav.LockDetails {
	return webdav.LockDetails{Root: root, Duration: time.Hour, ZeroDepth: zeroDepth}
}

// ⚠⚠ The whole reason this package exists. A client takes a lock, filex is
// restarted (deploy, container reschedule, crash), and the client presents the
// SAME token on its PUT. If the token no longer names the lock, the save fails
// with 412 and the server would meanwhile let somebody else lock the same file.
func TestALockSurvivesARestartWithItsTokenIntact(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	first, err := davlock.New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	token, err := first.Create(now, infinite("/main/report.docx", true))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Everything the process knew is gone; only the directory remains.
	second, err := davlock.New(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := second.Len(); got != 1 {
		t.Fatalf("locks after restart = %d, want 1", got)
	}
	release, err := second.Confirm(now, "/main/report.docx", "", webdav.Condition{Token: token})
	if err != nil {
		t.Fatalf("the client's token stopped naming its lock across a restart: %v", err)
	}
	release()

	// And the file is still exclusive: nobody else may take it.
	if _, err := second.Create(now, infinite("/main/report.docx", true)); err != webdav.ErrLocked {
		t.Fatalf("a second client locked a file that was already locked: err=%v", err)
	}
}

// A lock whose time ran out while the process was down must NOT come back.
// Durability that resurrects expired locks would leave files locked by nobody.
func TestAnExpiredLockDoesNotComeBack(t *testing.T) {
	dir := t.TempDir()
	past := time.Now().Add(-2 * time.Hour)

	first, err := davlock.New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := first.Create(past, webdav.LockDetails{
		Root: "/main/stale.txt", Duration: time.Minute, ZeroDepth: true,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	second, err := davlock.New(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := second.Len(); got != 0 {
		t.Fatalf("locks after restart = %d, want 0 — an expired lock was restored", got)
	}
	if _, err := second.Create(time.Now(), infinite("/main/stale.txt", true)); err != nil {
		t.Fatalf("a file locked by an expired lock could not be relocked: %v", err)
	}
}

// The tree rules, which are what makes a lock on a folder mean anything.
func TestDepthRules(t *testing.T) {
	now := time.Now()
	s := davlock.NewMemory()

	// An infinite-depth lock on a collection.
	if _, err := s.Create(now, infinite("/main/projects", false)); err != nil {
		t.Fatalf("lock the folder: %v", err)
	}
	// Nothing under it may be locked separately.
	if _, err := s.Create(now, infinite("/main/projects/acme/plan.md", true)); err != webdav.ErrLocked {
		t.Fatalf("a file under an infinite-depth folder lock was lockable: err=%v", err)
	}
	// A sibling is untouched.
	if _, err := s.Create(now, infinite("/main/other/plan.md", true)); err != nil {
		t.Fatalf("a sibling outside the locked folder was refused: %v", err)
	}

	// The other direction: a file is locked, so an infinite-depth lock on its
	// ANCESTOR must be refused — it would claim exclusivity it cannot have.
	s2 := davlock.NewMemory()
	if _, err := s2.Create(now, infinite("/main/docs/a.txt", true)); err != nil {
		t.Fatalf("lock the file: %v", err)
	}
	if _, err := s2.Create(now, infinite("/main/docs", false)); err != webdav.ErrLocked {
		t.Fatalf("an infinite-depth lock was granted over an already-locked file: err=%v", err)
	}
	// …but a ZERO-depth lock on the parent is fine: it claims only the folder.
	if _, err := s2.Create(now, infinite("/main/docs", true)); err != nil {
		t.Fatalf("a zero-depth lock on the parent was refused: %v", err)
	}
}

// An infinite-depth lock covers descendants for Confirm too — that is how a
// client locks a folder once and then writes twenty files inside it.
func TestAnInfiniteDepthLockConfirmsItsDescendants(t *testing.T) {
	now := time.Now()
	s := davlock.NewMemory()
	token, err := s.Create(now, infinite("/main/projects", false))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	release, err := s.Confirm(now, "/main/projects/acme/plan.md", "", webdav.Condition{Token: token})
	if err != nil {
		t.Fatalf("the folder lock did not cover a file inside it: %v", err)
	}
	release()

	// A zero-depth lock does not.
	s2 := davlock.NewMemory()
	t2, err := s2.Create(now, infinite("/main/projects", true))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s2.Confirm(now, "/main/projects/acme/plan.md", "", webdav.Condition{Token: t2}); err == nil {
		t.Fatal("a ZERO-depth folder lock was accepted for a file inside the folder")
	}
}

// A confirmed lock is claimed for the length of one request: it cannot be
// confirmed again, refreshed or unlocked until the request lets go.
func TestAConfirmedLockIsHeldUntilReleased(t *testing.T) {
	now := time.Now()
	s := davlock.NewMemory()
	token, err := s.Create(now, infinite("/main/a.txt", true))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	release, err := s.Confirm(now, "/main/a.txt", "", webdav.Condition{Token: token})
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if _, err := s.Confirm(now, "/main/a.txt", "", webdav.Condition{Token: token}); err == nil {
		t.Fatal("a lock already claimed by one request was claimed by a second")
	}
	if err := s.Unlock(now, token); err != webdav.ErrLocked {
		t.Fatalf("unlock during a request = %v, want ErrLocked", err)
	}
	release()
	if err := s.Unlock(now, token); err != nil {
		t.Fatalf("unlock after release: %v", err)
	}
}

// ⚠ A held lock must not be expired out from under the request holding it.
func TestAHeldLockIsNotExpired(t *testing.T) {
	now := time.Now()
	s := davlock.NewMemory()
	token, err := s.Create(now, webdav.LockDetails{
		Root: "/main/a.txt", Duration: time.Second, ZeroDepth: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	release, err := s.Confirm(now, "/main/a.txt", "", webdav.Condition{Token: token})
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	// Time passes well beyond the lock's life while the request is running.
	later := now.Add(time.Hour)
	if _, err := s.Create(later, infinite("/main/a.txt", true)); err != webdav.ErrLocked {
		t.Fatalf("a lock claimed by a running request was expired away: err=%v", err)
	}
	release()
	if _, err := s.Create(later, infinite("/main/a.txt", true)); err != nil {
		t.Fatalf("the lock did not expire after its request finished: %v", err)
	}
}

// Tokens must be absolute URIs (RFC 4918 §6.5) and must not repeat. memLS
// hands out "1", "2", "3" — persisting that would make two runs collide.
func TestTokensAreOpaqueURIsAndUnique(t *testing.T) {
	now := time.Now()
	s := davlock.NewMemory()
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		token, err := s.Create(now, webdav.LockDetails{
			Root: "/main/f" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Duration: time.Hour, ZeroDepth: true,
		})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		if len(token) < 20 || token[:16] != "opaquelocktoken:" {
			t.Fatalf("token %q is not an opaquelocktoken URI", token)
		}
		if seen[token] {
			t.Fatalf("token %q was handed out twice", token)
		}
		seen[token] = true
	}
}

// A corrupt lock file must not stop /dav from serving.
func TestACorruptLockFileStartsEmptyRatherThanFailing(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir+"/dav-locks.json", "{not json at all"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s, err := davlock.New(dir)
	if err != nil {
		t.Fatalf("a corrupt lock file made the lock system refuse to start: %v", err)
	}
	if got := s.Len(); got != 0 {
		t.Fatalf("locks = %d, want 0", got)
	}
	if _, err := s.Create(time.Now(), infinite("/main/a.txt", true)); err != nil {
		t.Fatalf("cannot lock after a corrupt file: %v", err)
	}
}

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}
