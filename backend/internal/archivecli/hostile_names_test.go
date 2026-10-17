package archivecli

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

// Every name a filex door may create is a member path as it is (task #238):
// an archive filex lists or extracts holds the names the person sees.
func TestCleanMemberPath_HostileNamesSurvive(t *testing.T) {
	for _, n := range hostilenames.Creatable() {
		if got, err := CleanMemberPath(n.Name); err != nil || got != n.Name {
			t.Errorf("%s: CleanMemberPath(%q) = %q, %v", n.ID, n.Name, got, err)
		}
	}
	d := hostilenames.Deep()
	if got, err := CleanMemberPath(d.Path); err != nil || got != d.Path {
		t.Errorf("deep: CleanMemberPath = %q, %v", got, err)
	}
}

// A name that BEGINS with two dots is a name, a colon in second place is a
// drive letter only after a letter. Before 0.57 both refused the WHOLE
// archive as unsafe (Preflight).
func TestCleanMemberPath_DotsAndColonsThatAreNames(t *testing.T) {
	for _, ok := range []string{"..notes", "..hidden/a.txt", "1:1 notes.txt", "9:30 call.md"} {
		if got, err := CleanMemberPath(ok); err != nil || got != ok {
			t.Errorf("CleanMemberPath(%q) = %q, %v; want it kept", ok, got, err)
		}
	}
	for _, bad := range []string{"..", "../x", "a/../../x", "C:x", `c:\x`, "Z:/x"} {
		if got, err := CleanMemberPath(bad); err == nil {
			t.Errorf("CleanMemberPath(%q) = %q, want a refusal", bad, got)
		}
	}
}

// 7-Zip's listing keeps a member's spaces: ` notes.txt ` is not `notes.txt`,
// and a selection made from the trimmed listing extracted nothing.
func TestParseTechnicalList_KeepsSpacesInNames(t *testing.T) {
	out := "Path = /tmp/x.7z\nType = 7z\n\n" +
		"Path =  notes.txt \nSize = 1\nModified = 2026-10-15 10:00:00\nAttributes = A....\n\n" +
		"Path = double  space.txt\r\nSize = 2\r\nAttributes = A....\r\n"
	entries := parseTechnicalList(out, "/tmp/x.7z")
	require.Len(t, entries, 2)
	assert.Equal(t, " notes.txt ", entries[0].Name)
	assert.EqualValues(t, 1, entries[0].Size)
	assert.Equal(t, "double  space.txt", entries[1].Name, "a CR line end is not part of the name")
	assert.EqualValues(t, 2, entries[1].Size)
}

// A member named on 7-Zip's command line is a name, not a wildcard (-spd):
// selecting `a*b.txt` must not extract `aXb.txt` too.
func TestSevenZipExtractTakesMemberNamesLiterally(t *testing.T) {
	svc, calls := recordSevenZip(t, nil)
	archive := filepath.Join(t.TempDir(), "upload.tmp")
	require.NoError(t, os.WriteFile(archive, []byte("7z\xbc\xaf\x27\x1c"), 0o600))
	_ = svc.ExtractAs(context.Background(), archive, "bundle.7z", t.TempDir(), "", []string{hostilenames.Get("asterisk").Name})
	var extract []string
	for _, c := range calls() {
		if len(c.Args) > 0 && c.Args[0] == "x" {
			extract = c.Args
		}
	}
	require.NotEmpty(t, extract, "7-Zip was not asked to extract")
	dashes := slices.Index(extract, "--")
	require.Positive(t, dashes, "%q", extract)
	assert.Contains(t, extract[:dashes], "-spd", "%q", extract)
	assert.Equal(t, hostilenames.Get("asterisk").Name, extract[len(extract)-1])
}

// A TAR member whose name the workspace disk cannot hold (over 255 bytes) is
// skipped and the rest extracted. One long name used to fail the whole job.
func TestTarExtractSkipsANameTheDiskCannotHold(t *testing.T) {
	long := hostilenames.TooLongForLocalDisk()
	if len(long) == 0 {
		t.Skip("this file system holds every name of the list")
	}
	svc := New(memorySettings{}, Config{SevenZipBin: filepath.Join(t.TempDir(), "no-7zip-here"), WorkDir: t.TempDir()})
	members := []tarMember{{name: "before.txt", typeflag: tar.TypeReg, body: "before"}}
	for _, n := range long {
		members = append(members, tarMember{name: n.Name, typeflag: tar.TypeReg, body: "long"})
	}
	members = append(members, tarMember{name: "after.txt", typeflag: tar.TypeReg, body: "after"})
	archive := filepath.Join(t.TempDir(), "upload.tmp")
	require.NoError(t, os.WriteFile(archive, buildTar(t, false, members...), 0o600))

	dest := t.TempDir()
	require.NoError(t, svc.ExtractAs(context.Background(), archive, "names.tar", dest, "", nil))
	for name, want := range map[string]string{"before.txt": "before", "after.txt": "after"} {
		got, err := os.ReadFile(filepath.Join(dest, name))
		require.NoError(t, err, name)
		assert.Equal(t, want, string(got))
	}
}

// Every hostile name a local disk holds survives a TAR round trip: listed
// and extracted under its own bytes.
func TestTarHostileNamesRoundTrip(t *testing.T) {
	svc := New(memorySettings{}, Config{SevenZipBin: filepath.Join(t.TempDir(), "no-7zip-here"), WorkDir: t.TempDir()})
	names := hostilenames.ForLocalDisk()
	var members []tarMember
	for _, n := range names {
		members = append(members, tarMember{name: n.Name, typeflag: tar.TypeReg, body: n.ID})
	}
	archive := filepath.Join(t.TempDir(), "upload.tmp")
	require.NoError(t, os.WriteFile(archive, buildTar(t, false, members...), 0o600))

	entries, err := svc.ListAs(context.Background(), archive, "names.tar", "")
	require.NoError(t, err)
	listed := entryNames(entries)
	dest := t.TempDir()
	require.NoError(t, svc.ExtractAs(context.Background(), archive, "names.tar", dest, "", nil))
	for _, n := range names {
		assert.Contains(t, listed, n.Name, "%s is not listed", n.ID)
		got, err := os.ReadFile(filepath.Join(dest, n.Name))
		if assert.NoError(t, err, "%s: not extracted as %q", n.ID, n.Name) {
			assert.Equal(t, n.ID, strings.TrimSpace(string(got)))
		}
	}
}

// A member name may hold a line break, even two: the -slt listing keeps it
// whole, and the block ends at the next `Path = `, not at a blank line.
func TestParseTechnicalList_KeepsLineBreaksInNames(t *testing.T) {
	out := "Path = /tmp/x.7z\nType = 7z\nPhysical Size = 99\n\n----------\n" +
		"Path = line\nbreak.txt\nSize = 3\nAttributes = A....\n\n" +
		"Path = two\n\nblank lines.txt\nSize = 4\nAttributes = A....\n\n" +
		"Path = forged\nSize = 1\nSize = 5\nAttributes = A....\n"
	entries := parseTechnicalList(out, "/tmp/x.7z")
	require.Len(t, entries, 3)
	assert.Equal(t, "line\nbreak.txt", entries[0].Name)
	assert.EqualValues(t, 3, entries[0].Size)
	assert.Equal(t, "two\n\nblank lines.txt", entries[1].Name)
	assert.EqualValues(t, 4, entries[1].Size)
	// A field that seems to come from the name is overwritten by 7-Zip's own.
	assert.Equal(t, "forged", entries[2].Name)
	assert.EqualValues(t, 5, entries[2].Size)
}

// The real 7-Zip lists and extracts a member whose name holds a line break.
func TestSevenZipLineBreakNameRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses a line break in a file name")
	}
	bin := supportedSevenZip(t)
	root := t.TempDir()
	source := filepath.Join(root, "source")
	require.NoError(t, os.Mkdir(source, 0o700))
	name := hostilenames.Get("control-newline").Name
	require.NoError(t, os.WriteFile(filepath.Join(source, name), []byte("lf"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(source, "plain.txt"), []byte("plain"), 0o600))

	svc := New(memorySettings{}, Config{SevenZipBin: bin, WorkDir: root})
	archive := filepath.Join(root, "names.7z")
	require.NoError(t, svc.Create(context.Background(), source, archive, CreateOptions{Format: "7z"}))
	entries, err := svc.List(context.Background(), archive, "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{name, "plain.txt"}, entryNames(entries))

	dest := t.TempDir()
	require.NoError(t, svc.Extract(context.Background(), archive, dest, "", []string{name}))
	got, err := os.ReadFile(filepath.Join(dest, name))
	require.NoError(t, err)
	assert.Equal(t, "lf", string(got))
}
