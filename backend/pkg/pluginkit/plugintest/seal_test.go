package plugintest_test

import (
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/plugintest"
)

// The kit's seal and locks keep the host's promises: one seal key, never
// destroyed, handed out from jobs only; a lock with no end.
func TestKit_PlatformSealAndLockUntilLifted(t *testing.T) {
	m := manifest()
	m.Permissions = []string{"sign", "files:lock"}
	h := plugintest.NewHost(m)
	if _, err := h.PlatformSeal(); err == nil {
		t.Fatal("a screen is not handed the seal")
	}
	h.EnterJob()
	a, err := h.PlatformSeal()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := h.PlatformSeal()
	if a.KeyRef != b.KeyRef || a.CertPEM != b.CertPEM {
		t.Error("the same seal on every call")
	}
	if err := h.KeyDestroy(a.KeyRef); err == nil {
		t.Error("the seal key is the host's; destroying it is refused")
	}
	if _, err := h.HostSign(a.KeyRef, make([]byte, 32)); err != nil {
		t.Errorf("the seal still signs: %v", err)
	}

	in := h.AddInput(plugintest.File{Name: "a.pdf", Data: []byte("%PDF-1.7")})
	until, err := h.FileLock(in.Ref, pluginkit.LockUntilLifted, "sealed")
	if err != nil || !until.IsZero() {
		t.Fatalf("a lock until lifted has no end: %v %v", until, err)
	}
	if _, locked := h.Locked(in.Ref); !locked {
		t.Error("the file is locked")
	}
	if _, err := h.FileLock(in.Ref, -2, ""); err == nil {
		t.Error("-2 days is not a lock")
	}
}
