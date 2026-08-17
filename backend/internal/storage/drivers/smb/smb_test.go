package smb

import (
	"context"
	"testing"
)

func mustInit(t *testing.T, cfg map[string]any) *Driver {
	t.Helper()
	d := &Driver{}
	if err := d.Init(context.Background(), cfg); err != nil {
		t.Fatalf("init: %v", err)
	}
	return d
}

func TestInit_Defaults(t *testing.T) {
	d := mustInit(t, map[string]any{"host": "nas.local", "share": "media", "user": "ada"})
	if d.port != 445 {
		t.Errorf("port = %d, want 445", d.port)
	}
	if d.share != "media" {
		t.Errorf("share = %q, want media", d.share)
	}
	// No base path means the whole share, and join must then produce the
	// share root rather than a leading separator.
	if got := d.join(""); got != "." {
		t.Errorf("join(\"\") = %q, want \".\"", got)
	}
}

// ⚠ A Windows user types `\\nas\media` and a NAS admin page shows `media`.
// Both have to land on the same share name, and the leading separators must be
// stripped — go-smb2 sends the name verbatim and a stray backslash produces
// STATUS_BAD_NETWORK_NAME, which says nothing about what is wrong.
func TestInit_ShareAndRootAreNormalised(t *testing.T) {
	d := mustInit(t, map[string]any{
		"host": "nas.local", "share": `\media\`, "user": "u", "root": `\projects\acme\`,
	})
	if d.share != "media" {
		t.Errorf("share = %q, want media", d.share)
	}
	if d.root != "projects/acme" {
		t.Errorf("root = %q, want projects/acme", d.root)
	}
}

// The legacy spellings the descriptor declares as aliases have to be READ, not
// merely declared — that is exactly how the SFTP driver silently ignored the
// admin form's `base_path` for a release.
func TestInit_LegacyKeySpellings(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
	}{
		{"canonical", map[string]any{"host": "h", "share": "s", "user": "u", "root": "sub"}},
		{"admin form", map[string]any{"host": "h", "share_name": "s", "username": "u", "base_path": "sub"}},
		{"third spelling", map[string]any{"host": "h", "share": "s", "user": "u", "path": "sub"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := mustInit(t, tc.cfg)
			if d.user != "u" {
				t.Errorf("user = %q, want u", d.user)
			}
			if d.share != "s" {
				t.Errorf("share = %q, want s", d.share)
			}
			if d.root != "sub" {
				t.Errorf("root = %q, want sub", d.root)
			}
		})
	}
}

// ⚠ The library refuses an empty user with "Anonymous account is not supported
// yet", an error that tells the operator nothing about what to type. Init has
// to catch it and name the fix.
func TestInit_RequiredFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{"no host", map[string]any{"share": "s", "user": "u"}, "host and share required"},
		{"no share", map[string]any{"host": "h", "user": "u"}, "host and share required"},
		{"no user", map[string]any{"host": "h", "share": "s"}, "guest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &Driver{}
			err := d.Init(context.Background(), tc.cfg)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}

// ⚠⚠ Paths go on the wire backslash-separated. A forward-slash path reaches
// some servers and is rejected by others with a name-invalid error that reads
// like the file is missing — the worst kind, because it looks like the file
// simply is not there.
func TestJoin_UsesBackslashesAndHonoursTheBasePath(t *testing.T) {
	d := mustInit(t, map[string]any{
		"host": "h", "share": "media", "user": "u", "root": "projects",
	})
	cases := map[string]string{
		"":                    `projects`,
		"/":                   `projects`,
		"acme":                `projects\acme`,
		"/acme/plan.md":       `projects\acme\plan.md`,
		`acme\plan.md`:        `projects\acme\plan.md`,
		"acme/../other/a.txt": `projects\other\a.txt`,
	}
	for in, want := range cases {
		if got := d.join(in); got != want {
			t.Errorf("join(%q) = %q, want %q", in, got, want)
		}
	}
}

// ⚠ A path that tries to climb out of the base path must not. `..` is cleaned
// away before the root is prefixed, so the worst a caller can reach is the
// configured folder itself.
func TestJoin_CannotEscapeTheBasePath(t *testing.T) {
	d := mustInit(t, map[string]any{
		"host": "h", "share": "media", "user": "u", "root": "projects",
	})
	for _, in := range []string{"../../etc", "/../secrets", `..\..\secrets`} {
		got := d.join(in)
		if got != "projects" && !contains(got, `projects\`) {
			t.Errorf("join(%q) = %q — escaped the base path", in, got)
		}
	}
}

// The vocabulary filex speaks. ⚠ os.IsNotExist alone is not enough: go-smb2
// wraps the NT status in its own type, and a missing PARENT directory produces
// OBJECT_PATH_NOT_FOUND — which without this reads as an unexplained failure
// rather than "not there", and the caller then retries forever.
func TestMapErr(t *testing.T) {
	for _, tc := range []struct {
		msg  string
		want string
	}{
		{"STATUS_OBJECT_NAME_NOT_FOUND", "not found"},
		{"STATUS_OBJECT_PATH_NOT_FOUND", "not found"},
		{"STATUS_ACCESS_DENIED", "read-only"},
	} {
		got := mapErr(errString(tc.msg))
		if got == nil || !contains(got.Error(), tc.want) {
			t.Errorf("mapErr(%s) = %v, want something meaning %q", tc.msg, got, tc.want)
		}
	}
	if mapErr(nil) != nil {
		t.Error("mapErr(nil) must stay nil")
	}
}

func TestCapabilities(t *testing.T) {
	d := &Driver{}
	c := d.Capabilities()
	if !c.Read || !c.Range || !c.Write || !c.Move || !c.Copy || !c.Delete || !c.Mkdir {
		t.Fatalf("SMB should support everything but presign: %+v", c)
	}
	// ⚠ Presign is an HTTP idea and has no SMB equivalent; claiming it would
	// make the share hand out URLs that never resolve.
	if c.Presign {
		t.Error("SMB cannot presign")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
