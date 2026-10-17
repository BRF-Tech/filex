package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"mime/multipart"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/archivecli"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/leafname"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/archivetest"
	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

// The upload names (task #238): every name the list lets a door create is
// taken as it is, from `name[]` or from the part's filename, and the first
// refused one refuses the batch with leafname's reason.
func TestUploadNames_FollowTheHostileList(t *testing.T) {
	for _, n := range hostilenames.Creatable() {
		files := []*multipart.FileHeader{{Filename: "part.bin"}}
		form := &multipart.Form{Value: map[string][]string{uploadNameField: {n.Name}}}
		got, err := uploadNames(form, files, nil)
		require.NoError(t, err, n.ID)
		assert.Equal(t, []string{n.Name}, got, n.ID)
	}
	for _, n := range hostilenames.Refused() {
		files := []*multipart.FileHeader{{Filename: "ok.txt"}, {Filename: "part.bin"}}
		form := &multipart.Form{Value: map[string][]string{uploadNameField: {"ok.txt", n.Name}}}
		_, err := uploadNames(form, files, nil)
		require.Error(t, err, n.ID)
		assert.ErrorIs(t, err, leafname.ErrRefused, n.ID)
	}
	// A count that does not match the files: the filenames, as before.
	got, err := uploadNames(&multipart.Form{Value: map[string][]string{uploadNameField: {"a", "b"}}},
		[]*multipart.FileHeader{{Filename: "only.txt"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"only.txt"}, got)
}

// A mail names a file the way a page does: a bidi control is shown, never
// obeyed, so the RLO name keeps its true extension in the subject and the
// body (leafname.Visible).
func TestMailTexts_BidiControlIsShownNotObeyed(t *testing.T) {
	rlo := hostilenames.Get("rlo-extension").Name
	shown := "invoice" + string(unicode.ReplacementChar) + "fdp.exe"
	subject, body := shareMailText("en", "", rlo, false, 10, "https://files.example.com/s/t", "", false, 7)
	for what, text := range map[string]string{"subject": subject, "body": body} {
		assert.NotContains(t, text, string(rune(0x202E)), what)
		assert.Contains(t, text, shown, what)
	}
	subject, body = dropInviteMailText("en", "", rlo, "https://files.example.com/d/t", "", false, 7, 0, 0, nil)
	for what, text := range map[string]string{"subject": subject, "body": body} {
		assert.NotContains(t, text, string(rune(0x202E)), "drop invite "+what)
	}
	_, body = itemGrantText("en", rlo, "https://files.example.com/")
	assert.NotContains(t, body, string(rune(0x202E)), "grant")
}

// The archive's download name is cut at a rune boundary.
func TestSanitizeArchiveName_CutsAtARuneBoundary(t *testing.T) {
	got := sanitizeArchiveName("a" + strings.Repeat("ş", 60))
	assert.True(t, utf8.ValidString(got), "%q", got)
	assert.LessOrEqual(t, len(got), 120)
	assert.Equal(t, "a"+strings.Repeat("ş", 59), got)
}

// mapDriverErr reads the error's own words, not the path it carries: a file
// named "already there.txt" failing for another reason is not a conflict.
func TestMapDriverErr_NameInThePathDoesNotPickTheStatus(t *testing.T) {
	err := &fs.PathError{Op: "open", Path: "/srv/root/already exists here.txt", Err: syscall.EIO}
	assert.Equal(t, 500, mapDriverErr(err))
	tooLong := &fs.PathError{Op: "open", Path: "/srv/root/x", Err: syscall.ENAMETOOLONG}
	assert.Equal(t, 400, mapDriverErr(tooLong))
}

// A ZIP name in a DOS code page is decoded by the account's language: IBM
// 857 for Turkish, IBM 437 (the ZIP specification's) for English; a name
// carrying the UTF-8 flag, or valid UTF-8 without it, is kept as it is.
func TestZipNames_DecodeByAccountLanguage(t *testing.T) {
	raw := archivetest.RawNameZip(t,
		archivetest.RawMember{Name: "\x9ei\x9fli \x8d\x98\xa6\xa7.txt", Body: "x"},
		archivetest.RawMember{Name: "Bütçe.txt", Body: "y"},
	)
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	require.NoError(t, err)
	tr := auth.WithUser(context.Background(), &model.User{ID: 1, Locale: "tr"})
	en := auth.WithUser(context.Background(), &model.User{ID: 1, Locale: "en"})
	assert.Equal(t, "Şişli ıİĞğ.txt", zipNamesFor(tr).name(zr.File[0]))
	assert.Equal(t, string(rune(0x20A7))+"i"+string(rune(0x0192))+"li "+string([]rune{0x00EC, 0x00FF, 0x00AA, 0x00BA})+".txt",
		zipNamesFor(en).name(zr.File[0]))
	assert.Equal(t, "Bütçe.txt", zipNamesFor(tr).name(zr.File[1]))
	assert.Equal(t, "Bütçe.txt", zipNamesFor(en).name(zr.File[1]))
}

// The members an extractor is handed when one is too long for this
// server's disk: every file of the selection but the long ones, by name; with
// nothing too long, the selection as it was.
func TestExternalMembers_LeaveOutWhatTheDiskCannotHold(t *testing.T) {
	long := strings.Repeat("ğ", 128) + ".txt"
	entries := []archivecli.Entry{
		{Name: "docs", IsDir: true},
		{Name: "docs/a.txt"},
		{Name: "docs/" + long},
		{Name: "b.txt"},
	}
	members, skipped := externalMembers(entries, nil, false)
	assert.Equal(t, []string{"docs/a.txt", "b.txt"}, members)
	assert.Equal(t, []string{"docs/" + long}, skipped)

	members, skipped = externalMembers(entries, []string{"docs"}, false)
	assert.Equal(t, []string{"docs/a.txt"}, members)
	assert.Equal(t, []string{"docs/" + long}, skipped)

	members, skipped = externalMembers(entries[:2], []string{"docs"}, false)
	assert.Equal(t, []string{"docs"}, members, "nothing too long: the selection as it was")
	assert.Empty(t, skipped)

	// 132 characters fit a Windows disk (255 UTF-16 units).
	_, skipped = externalMembers(entries, nil, true)
	assert.Empty(t, skipped)
}

func TestMemberBatches_BoundTheCommandLine(t *testing.T) {
	assert.Equal(t, [][]string{nil}, memberBatches(nil, 10))
	got := memberBatches([]string{"aaaa", "bbbb", "cccc", strings.Repeat("d", 20)}, 10)
	assert.Equal(t, [][]string{{"aaaa", "bbbb"}, {"cccc"}, {strings.Repeat("d", 20)}}, got)
}

// A backslash separates only on a Windows host: on Linux `a\b.txt` is one
// name a shared folder lists and serves.
func TestCleanShareRel_BackslashIsANameOffWindows(t *testing.T) {
	got, ok := cleanShareRel(`docs\a\b.txt`)
	require.True(t, ok)
	if runtime.GOOS == "windows" {
		assert.Equal(t, "docs/a/b.txt", got)
		return
	}
	assert.Equal(t, `docs\a\b.txt`, got)
	_, ok = cleanShareRel(`../x`)
	assert.False(t, ok)
}

// The job's text names what it left out, at most five, and counts the rest.
func TestArchiveSkipsError_SaysWhatWasLeftOut(t *testing.T) {
	var s memberSkips
	for i := 0; i < memberSkipsShown+3; i++ {
		s.add(fmt.Sprintf("m%d.txt", i), skipNameNotAllowed)
	}
	assert.Len(t, s.List, memberSkipsShown)
	assert.Equal(t, 3, s.More)
	e := &archiveSkipsError{skips: s}
	assert.Equal(t, memberSkipsShown+3, e.Skips())
	assert.Contains(t, e.Error(), `"m0.txt" (a name filex does not allow)`)
	assert.Contains(t, e.Error(), fmt.Sprintf("and %d more", memberSkipsShown+3-archiveSkipsNamed))
}

// Only a refusal for a control character gives way to an existing file.
func TestKeepsExistingName_OnlyControlNames(t *testing.T) {
	yes := func() bool { return true }
	assert.True(t, keepsExistingName(leafname.Check("a\tb.txt"), yes))
	assert.False(t, keepsExistingName(leafname.Check("a\tb.txt"), func() bool { return false }))
	for _, bad := range []string{"..", "...", `a\b`, "a\x00b"} {
		assert.False(t, keepsExistingName(leafname.Check(bad), yes), bad)
	}
}
