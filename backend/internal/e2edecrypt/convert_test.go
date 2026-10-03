package e2edecrypt

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The in-place conversion (convert.go) and the name pass (namepass.go), Go
// twins of lib/e2econvert.ts and lib/e2enamepass.ts, against an in-memory
// folder that behaves like the server: raw listings, conditional writes, a
// rename that refuses a taken name.

const fakeRoot = "docs://Kasa"

type fakeNode struct {
	dir   bool
	data  []byte
	mtime int64
}

type fakeServer struct {
	nodes  map[string]*fakeNode
	clock  int64
	writes []string
	// expects is every precondition a conversion write carried.
	expects map[string]string
	// failOnce makes the next write to these paths fail (a dropped
	// connection after the download).
	failOnce map[string]bool
	stopAt   int
	fmk      []byte
	// onList, when set, runs after a folder is listed.
	onList func(dir string)
}

func newFakeServer(fmk []byte) *fakeServer {
	s := &fakeServer{nodes: map[string]*fakeNode{fakeRoot: {dir: true}}, expects: map[string]string{}, failOnce: map[string]bool{}, stopAt: -1, fmk: fmk}
	return s
}

func (s *fakeServer) mkdir(p string) { s.nodes[p] = &fakeNode{dir: true} }
func (s *fakeServer) put(p string, b []byte) {
	s.clock++
	s.nodes[p] = &fakeNode{data: b, mtime: 1_700_000_000_000 + s.clock}
}

func parentOf(p string) string { return p[:strings.LastIndexByte(p, '/')] }
func baseOf(p string) string   { return p[strings.LastIndexByte(p, '/')+1:] }

func (s *fakeServer) List(dir string) ([]ConvertRow, error) {
	n, ok := s.nodes[dir]
	if !ok || !n.dir {
		return nil, errors.New("404")
	}
	var rows []ConvertRow
	for p, c := range s.nodes {
		if p == dir || parentOf(p) != dir || baseOf(p) == MarkerName {
			continue
		}
		r := ConvertRow{Path: p, Name: baseOf(p), Dir: c.dir, Size: -1, Modified: -1}
		if !c.dir {
			r.Size, r.Modified = int64(len(c.data)), c.mtime
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].Name < rows[b].Name })
	if s.onList != nil {
		s.onList(dir)
	}
	return rows, nil
}

func (s *fakeServer) Head(p string) ([]byte, error) {
	n, ok := s.nodes[p]
	if !ok || n.dir {
		return nil, errors.New("404")
	}
	return n.data[:min(16, len(n.data))], nil
}

func (s *fakeServer) Convert(dir string, row ConvertRow, expect string) error {
	s.expects[row.Path] = expect
	n, ok := s.nodes[row.Path]
	if !ok || n.dir {
		return errors.New("404")
	}
	var out bytes.Buffer
	// What the CLI does: the download, encrypted as it arrives.
	if err := EncryptFile(&out, bytes.NewReader(n.data), row.Size, s.fmk, nil); err != nil {
		return err
	}
	if s.failOnce[row.Path] {
		delete(s.failOnce, row.Path)
		return errors.New("the connection dropped before the commit")
	}
	if expect != "" && expect != fmt.Sprintf("%d:%d", len(n.data), n.mtime) {
		return errors.New("412 precondition failed")
	}
	s.put(row.Path, out.Bytes())
	s.writes = append(s.writes, row.Path)
	return nil
}

func (s *fakeServer) Stopped() bool { return s.stopAt >= 0 && len(s.writes) >= s.stopAt }

func (s *fakeServer) ReadSidecar(dir, name string) ([]byte, bool) {
	n, ok := s.nodes[dir+"/"+name]
	if !ok || n.dir {
		return nil, false
	}
	return n.data, true
}

func (s *fakeServer) WriteSidecar(dir, name, content string) error {
	s.put(dir+"/"+name, []byte(content))
	return nil
}

func (s *fakeServer) Rename(dir string, row ConvertRow, to string) error {
	if _, taken := s.nodes[dir+"/"+to]; taken {
		return errors.New("409 taken")
	}
	from := row.Path
	for p, n := range s.nodes {
		if p == from || strings.HasPrefix(p, from+"/") {
			delete(s.nodes, p)
			s.nodes[dir+"/"+to+strings.TrimPrefix(p, from)] = n
		}
	}
	return nil
}

func plainFolder(fmk []byte) *fakeServer {
	s := newFakeServer(fmk)
	s.put(fakeRoot+"/"+MarkerName, []byte(`{"v":3}`))
	s.put(fakeRoot+"/bir.txt", []byte("bir"))
	s.mkdir(fakeRoot + "/Faturalar")
	s.put(fakeRoot+"/Faturalar/2024.pdf", []byte("%PDF 2024"))
	s.put(fakeRoot+"/Faturalar/boş.txt", nil)
	s.mkdir(fakeRoot + "/Faturalar/Eski yıllar")
	s.put(fakeRoot+"/Faturalar/Eski yıllar/2019.pdf", pattern(5000, 3))
	return s
}

func decrypted(t *testing.T, s *fakeServer, p string) []byte {
	t.Helper()
	n := s.nodes[p]
	require.NotNil(t, n, p)
	require.True(t, HasMagic(n.data), "%s still plaintext", p)
	var out bytes.Buffer
	require.NoError(t, DecryptFileStream(&out, bytes.NewReader(n.data), s.fmk, nil), p)
	return out.Bytes()
}

func TestRunConversion_EncryptsEveryFileInPlaceAndLeavesTheKeyFile(t *testing.T) {
	s := plainFolder(pattern(32, 1))
	var prog ConvertProgress
	require.NoError(t, RunConversion(fakeRoot, s, &prog))
	require.Equal(t, 4, prog.Total)
	require.Equal(t, 4, prog.Done)
	require.Zero(t, prog.Failed)
	require.Equal(t, `{"v":3}`, string(s.nodes[fakeRoot+"/"+MarkerName].data), "the key file is not a file to convert")
	require.Equal(t, "bir", string(decrypted(t, s, fakeRoot+"/bir.txt")))
	require.Empty(t, decrypted(t, s, fakeRoot+"/Faturalar/boş.txt"))
	require.Equal(t, pattern(5000, 3), decrypted(t, s, fakeRoot+"/Faturalar/Eski yıllar/2019.pdf"))
	// Every write carried the listing's own signature of the file.
	for p, e := range s.expects {
		require.Regexp(t, `^\d+:\d+$`, e, p)
	}
}

func TestRunConversion_ContinuesAfterAStopTouchingNothingItDid(t *testing.T) {
	s := plainFolder(pattern(32, 1))
	s.stopAt = 1
	var first ConvertProgress
	require.ErrorIs(t, RunConversion(fakeRoot, s, &first), ErrStopped)
	require.Equal(t, 1, first.Done)

	s.stopAt = -1
	var second ConvertProgress
	require.NoError(t, RunConversion(fakeRoot, s, &second))
	require.Equal(t, 3, second.Done)
	require.Equal(t, 1, second.Skipped)
	require.Len(t, s.writes, 4, "nothing written twice")

	var third ConvertProgress
	require.NoError(t, RunConversion(fakeRoot, s, &third))
	require.Zero(t, third.Done)
	require.Equal(t, 4, third.Skipped)
}

func TestRunConversion_AWriteThatBrokeOffLeavesThePlaintextForTheNextRun(t *testing.T) {
	s := plainFolder(pattern(32, 1))
	s.failOnce[fakeRoot+"/bir.txt"] = true
	var first ConvertProgress
	require.NoError(t, RunConversion(fakeRoot, s, &first))
	require.Equal(t, 1, first.Failed)
	require.Equal(t, fakeRoot+"/bir.txt", first.Failures[0].Path)
	require.Equal(t, "bir", string(s.nodes[fakeRoot+"/bir.txt"].data))

	var second ConvertProgress
	require.NoError(t, RunConversion(fakeRoot, s, &second))
	require.Equal(t, 1, second.Done)
	require.Zero(t, second.Failed)
	require.Equal(t, "bir", string(decrypted(t, s, fakeRoot+"/bir.txt")))
}

func TestRunConversion_DoesNotOverwriteAFileEditedMeanwhile(t *testing.T) {
	s := plainFolder(pattern(32, 1))
	s.onList = func(dir string) {
		if dir == fakeRoot+"/Faturalar" {
			s.put(fakeRoot+"/Faturalar/2024.pdf", []byte("%PDF 2024, edited after it was listed"))
		}
	}
	var prog ConvertProgress
	require.NoError(t, RunConversion(fakeRoot, s, &prog))
	require.Equal(t, 1, prog.Failed)
	require.Equal(t, "%PDF 2024, edited after it was listed", string(s.nodes[fakeRoot+"/Faturalar/2024.pdf"].data))
}

func TestRunConversion_SkipsWhatIsEncryptedAndTheSidecars(t *testing.T) {
	s := plainFolder(pattern(32, 1))
	already, err := EncryptContent(s.fmk, []byte("uploaded encrypted"), nil)
	require.NoError(t, err)
	s.put(fakeRoot+"/already.bin", already)
	s.put(fakeRoot+"/"+strings.Repeat("A", 43)+SidecarSuffix, []byte("sidecar"))
	var prog ConvertProgress
	require.NoError(t, RunConversion(fakeRoot, s, &prog))
	require.Equal(t, 5, prog.Total, "a sidecar is not a file of the folder")
	require.Equal(t, 1, prog.Skipped)
	require.Equal(t, "sidecar", string(s.nodes[fakeRoot+"/"+strings.Repeat("A", 43)+SidecarSuffix].data))
}

func TestRunConversion_AFileWithNoSizeIsNotGuessedAt(t *testing.T) {
	s := plainFolder(pattern(32, 1))
	io := noSize{s}
	var prog ConvertProgress
	require.NoError(t, RunConversion(fakeRoot, io, &prog))
	require.Equal(t, 4, prog.Failed)
	require.Zero(t, prog.Done)
	require.Equal(t, "bir", string(s.nodes[fakeRoot+"/bir.txt"].data))
}

type noSize struct{ *fakeServer }

func (n noSize) List(dir string) ([]ConvertRow, error) {
	rows, err := n.fakeServer.List(dir)
	for i := range rows {
		rows[i].Size = -1
	}
	return rows, err
}

// ── the name pass ─────────────────────────────────────────────────────────

func namesKey(t *testing.T) *NameKey {
	t.Helper()
	nk, err := NewNameKey(pattern(64, 9), NamesLongDefault, pattern(16, 10))
	require.NoError(t, err)
	return nk
}

// readBack decrypts every stored name of the tree, the way `filex decrypt`
// and the browser do, into plaintext paths.
func readBack(t *testing.T, s *fakeServer, nk *NameKey) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	var walk func(dir, plainDir string, id []byte)
	walk = func(dir, plainDir string, id []byte) {
		rows, err := s.List(dir)
		require.NoError(t, err)
		for _, r := range rows {
			name, state := nk.DecryptStoredName(r.Name, id, func(n string) ([]byte, bool) { return s.ReadSidecar(dir, n) })
			if state == NameSidecar {
				continue
			}
			require.Equal(t, NameDecrypted, state, "%s is not encrypted", r.Path)
			p := plainDir + "/" + name
			out[p] = true
			if r.Dir {
				walk(r.Path, p, nk.EffectiveDirID(id, r.Name))
			}
		}
	}
	walk(fakeRoot, "", nk.RootID)
	return out
}

func TestRunNamePass_EncryptsEveryNameAndTheTreeReadsBack(t *testing.T) {
	s := plainFolder(pattern(32, 1))
	long := strings.Repeat("çok uzun bir ad ", 12) + ".txt"
	s.put(fakeRoot+"/Faturalar/"+long, []byte("x"))
	nk := namesKey(t)
	var prog NamePassProgress
	require.NoError(t, RunNamePass(nk, fakeRoot, s, &prog))
	require.Zero(t, prog.Failed)
	require.Equal(t, 7, prog.Renamed)
	require.Equal(t, map[string]bool{
		"/bir.txt": true, "/Faturalar": true, "/Faturalar/2024.pdf": true, "/Faturalar/boş.txt": true,
		"/Faturalar/Eski yıllar": true, "/Faturalar/Eski yıllar/2019.pdf": true, "/Faturalar/" + long: true,
	}, readBack(t, s, nk))
	require.Contains(t, s.nodes, fakeRoot+"/"+MarkerName, "the key file keeps its name")

	// Idempotent: a second pass finds nothing to do.
	var again NamePassProgress
	require.NoError(t, RunNamePass(nk, fakeRoot, s, &again))
	require.Zero(t, again.Seen)
}

func TestRunNamePass_RepairsANameMovedFromAnotherFolder(t *testing.T) {
	s := plainFolder(pattern(32, 1))
	nk := namesKey(t)
	var prog NamePassProgress
	require.NoError(t, RunNamePass(nk, fakeRoot, s, &prog))
	// Move an encrypted file from Faturalar to the root, outside filex: its
	// name is sealed for Faturalar and does not open at the root.
	var from string
	for p := range s.nodes {
		if parentOf(p) != fakeRoot && strings.Count(strings.TrimPrefix(p, fakeRoot), "/") == 2 && !s.nodes[p].dir {
			name, state := nk.DecryptStoredName(baseOf(p), nk.EffectiveDirID(nk.RootID, baseOf(parentOf(p))), nil)
			if state == NameDecrypted && name == "2024.pdf" {
				from = p
			}
		}
	}
	require.NotEmpty(t, from)
	s.nodes[fakeRoot+"/"+baseOf(from)] = s.nodes[from]
	delete(s.nodes, from)

	var repair NamePassProgress
	require.NoError(t, RunNamePass(nk, fakeRoot, s, &repair))
	require.Equal(t, 1, repair.Repaired)
	require.True(t, readBack(t, s, nk)["/2024.pdf"])
}

func TestRunNamePass_KeepsBothWhenTheNameIsTaken(t *testing.T) {
	s := newFakeServer(pattern(32, 1))
	nk := namesKey(t)
	enc, err := nk.EncryptName("rapor.txt", nk.RootID, nil)
	require.NoError(t, err)
	s.put(fakeRoot+"/"+enc.Stored, []byte("already there, encrypted name"))
	s.put(fakeRoot+"/rapor.txt", []byte("plaintext name"))
	var prog NamePassProgress
	require.NoError(t, RunNamePass(nk, fakeRoot, s, &prog))
	require.Equal(t, 1, prog.Renamed)
	got := readBack(t, s, nk)
	require.True(t, got["/rapor.txt"])
	require.True(t, got["/rapor (2).txt"])
}

func TestNumberedName(t *testing.T) {
	require.Equal(t, "rapor (2).txt", NumberedName("rapor.txt", 2))
	require.Equal(t, "Makefile (3)", NumberedName("Makefile", 3))
	require.Equal(t, ".env (2)", NumberedName(".env", 2))
}
