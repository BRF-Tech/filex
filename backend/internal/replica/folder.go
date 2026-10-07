package replica

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// EVERY STORAGE IN A FOLDER OF ITS OWN ON THE TARGET (#186, the maintainers'
// decision, 2026-10-06).
//
// Several storages may share a replication target, and their paths are
// relative to each storage: two storages each holding "rapor.docx" would
// overwrite each other on a target written at its root. So a storage writes
// inside its own folder there, `<folder>/rapor.docx`.
//
// The folder is chosen ONCE, when the storage is first linked to the target,
// from its name made safe for every backend (FolderName), and kept in
// replica_links (migration 00095). Renaming the storage does not move it -
// a backup that wandered off to a new folder at every rename would leave the
// old one behind, full - and a target switched off and on again keeps it.
// Changing it is its own operation (SetFolder: PUT
// /api/admin/replica/links/{storage_id}), which begins the storage's initial
// copy again into the new folder; the old folder is left as it is.

// ErrFolderTaken is SetFolder's answer for a folder another storage on the
// same target already writes into.
var ErrFolderTaken = errors.New("replica: another storage on this target already uses that folder")

// maxFolderRunes bounds a folder name; every backend takes this many.
const maxFolderRunes = 64

// reservedNames are names Windows (and so an SMB share on it) refuses as a
// folder, whatever the extension.
var reservedNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true, "com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true, "lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// FolderName makes a folder name every backend accepts out of a storage's
// name: letters (any script) and digits stay, a space, `-`, `_` and `.` stay,
// everything else - `/ \ : * ? " < > |`, control characters - becomes `-`;
// runs of `-` are one, the ends lose dots, spaces and dashes (a leading dot
// would hide it, a trailing one Windows drops), and it is at most 64
// characters. A name that ends up empty, or that Windows reserves (CON,
// LPT1...), becomes storage-<id>.
func FolderName(name string, storageID int64) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.TrimSpace(name) {
		ok := unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' || r == '_' || r == '.'
		if !ok || r == '-' {
			if !dash {
				b.WriteRune('-')
				dash = true
			}
			continue
		}
		b.WriteRune(r)
		dash = false
	}
	out := strings.Trim(b.String(), ". -")
	if rs := []rune(out); len(rs) > maxFolderRunes {
		out = strings.Trim(string(rs[:maxFolderRunes]), ". -")
	}
	fallback := "storage-" + strconv.FormatInt(storageID, 10)
	if out == "" {
		return fallback
	}
	// Windows reserves the stem whatever follows the first dot ("LPT1.txt" is
	// LPT1), so the id goes into the stem.
	cut := len(out)
	if i := strings.IndexByte(out, '.'); i >= 0 {
		cut = i
	}
	if reservedNames[strings.ToLower(out[:cut])] {
		return out[:cut] + "-" + strconv.FormatInt(storageID, 10) + out[cut:]
	}
	return out
}

// folderKey is how two folder names are compared: case does not tell them
// apart (an SMB share and S3 behind a case-insensitive gateway would merge
// them).
func folderKey(folder string) string { return strings.ToLower(folder) }

// EnsureFolder returns the storage's folder on targetID, choosing and keeping
// one the first time: its name made safe (FolderName), or - when another
// storage on the same target already writes into that name - the name with
// the storage's id appended. A link kept for another target is replaced (the
// storage was relinked).
func EnsureFolder(ctx context.Context, store db.Store, st *model.Storage, targetID int64) (string, error) {
	if st == nil || targetID == 0 {
		return "", errors.New("replica: no storage or target to choose a folder for")
	}
	cur, err := store.GetReplicaLink(ctx, st.ID)
	if err != nil {
		return "", err
	}
	if cur != nil && cur.TargetID == targetID && cur.Folder != "" {
		return cur.Folder, nil
	}
	taken, err := takenFolders(ctx, store, targetID, st.ID)
	if err != nil {
		return "", err
	}
	folder := FolderName(st.Name, st.ID)
	if taken[folderKey(folder)] {
		short := []rune(folder)
		short = short[:min(len(short), maxFolderRunes-12)]
		folder = strings.Trim(string(short), ". -") + "-" + strconv.FormatInt(st.ID, 10)
	}
	if taken[folderKey(folder)] {
		folder = "storage-" + strconv.FormatInt(st.ID, 10)
	}
	link := &model.ReplicaLink{
		StorageID:   st.ID,
		TargetID:    targetID,
		Folder:      folder,
		FolderKey:   folderKey(folder),
		CreatedUnix: time.Now().Unix(),
	}
	if err := store.PutReplicaLink(ctx, link); err != nil {
		// Another request chose it at the same moment: theirs stands.
		if again, gerr := store.GetReplicaLink(ctx, st.ID); gerr == nil && again != nil && again.TargetID == targetID && again.Folder != "" {
			return again.Folder, nil
		}
		return "", fmt.Errorf("replica: keep the folder of storage %d: %w", st.ID, err)
	}
	return folder, nil
}

// takenFolders are the folder keys of the OTHER storages on targetID.
func takenFolders(ctx context.Context, store db.Store, targetID, except int64) (map[string]bool, error) {
	links, err := store.ListReplicaLinks(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, l := range links {
		if l.TargetID == targetID && l.StorageID != except {
			out[l.FolderKey] = true
		}
	}
	return out, nil
}

// SetFolder changes the storage's folder on its target to folder (made safe
// the way FolderName makes a name safe) and returns the one kept. Another
// storage on the same target writing into it is ErrFolderTaken. The caller
// rebuilds the storage's driver and begins its initial copy again; nothing is
// moved on the target.
func (s *Service) SetFolder(ctx context.Context, storageID int64, folder string) (*model.ReplicaLink, error) {
	st, err := s.store.GetStorage(ctx, storageID)
	if err != nil || st == nil {
		return nil, ErrNotLinked
	}
	if st.ReplicaTargetID == nil {
		return nil, ErrNotLinked
	}
	targetID := *st.ReplicaTargetID
	clean := FolderName(folder, storageID)
	taken, err := takenFolders(ctx, s.store, targetID, storageID)
	if err != nil {
		return nil, err
	}
	if taken[folderKey(clean)] {
		return nil, ErrFolderTaken
	}
	link := &model.ReplicaLink{
		StorageID:   storageID,
		TargetID:    targetID,
		Folder:      clean,
		FolderKey:   folderKey(clean),
		CreatedUnix: s.now().Unix(),
	}
	if err := s.store.PutReplicaLink(ctx, link); err != nil {
		if again, _ := takenFolders(ctx, s.store, targetID, storageID); again[folderKey(clean)] {
			return nil, ErrFolderTaken
		}
		return nil, err
	}
	return link, nil
}

// LinkStatus is a storage's link as the Replication page shows it.
type LinkStatus struct {
	*model.ReplicaLink
	StorageName string `json:"storage_name"`
	TargetName  string `json:"target_name"`
}

// Links lists every storage's folder on its target.
func (s *Service) Links(ctx context.Context) ([]LinkStatus, error) {
	links, err := s.store.ListReplicaLinks(ctx)
	if err != nil {
		return nil, err
	}
	names, targets := s.pageNames(ctx)
	out := make([]LinkStatus, 0, len(links))
	for _, l := range links {
		out = append(out, LinkStatus{
			ReplicaLink: l,
			StorageName: storageLabel(l.StorageID, names[l.StorageID]),
			TargetName:  targets[l.TargetID],
		})
	}
	return out, nil
}

// pageNames are the names the Replication page shows beside a row: every
// storage's and every target's, by id. A list that cannot be read leaves its
// names out and the row is still shown (a storage by its id, storageLabel).
// Links and InitialCopies both ask this, so the two tables name a storage the
// same way.
func (s *Service) pageNames(ctx context.Context) (storages, targets map[int64]string) {
	storages, targets = map[int64]string{}, map[int64]string{}
	if list, err := s.store.ListStorages(ctx); err == nil {
		for _, st := range list {
			storages[st.ID] = st.Name
		}
	}
	if list, err := s.store.ListReplicationTargets(ctx); err == nil {
		for _, t := range list {
			targets[t.ID] = t.Name
		}
	}
	return storages, targets
}
