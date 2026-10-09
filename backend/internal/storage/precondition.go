package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// A change is checked before it asks for its storage's row gate
// (internal/rowgate): the destination is free (WebDAV's Overwrite: F, a
// rename's NameTaken, a move's MoveDest), the source is the item the person
// asked about. The gate can make it wait - a judgement of the storage holds it
// - and what was checked may not hold any more once it gets the gate: a file
// landed on the free name, the source was replaced or went. So a surface asks
// again once it holds the gate, with the two questions below, and refuses
// rather than replace a file nobody asked it to replace or move one nobody
// asked it to move (sec055).

// ErrTakenMeanwhile is a destination that was free when the change was
// checked and holds an item by the time the change held its storage's row
// gate. It is an os.ErrExist, so every surface answers it as the conflict it
// already answers for a taken name (409, WebDAV's refusal, SSH_FX_FAILURE).
var ErrTakenMeanwhile = fmt.Errorf("%w: the destination was taken while the change waited for its turn", os.ErrExist)

// ErrChangedMeanwhile is a source that is not the item the change was checked
// against any more by the time the change held its storage's row gate: gone,
// replaced, or rewritten. A request answers it 412 Precondition Failed.
var ErrChangedMeanwhile = errors.New("storage: the item changed while the change waited for its turn")

// StillFree asks whether dst is still free on drv. src is the change's own
// source: on a storage that folds case, a change of case alone finds the
// source itself at dst, which does not make dst taken. Answers nil,
// ErrTakenMeanwhile, or the Stat's own error.
func StillFree(ctx context.Context, drv Driver, dst, src string) error {
	obj, err := drv.Stat(ctx, dst)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil
	case err != nil:
		return err
	}
	if src != "" && strings.EqualFold(strings.Trim(dst, "/"), strings.Trim(src, "/")) &&
		strings.EqualFold(strings.Trim(obj.Path, "/"), strings.Trim(src, "/")) {
		return nil
	}
	return ErrTakenMeanwhile
}

// StillAsSeen asks whether rel is still the object seen (a Stat taken when the
// change was checked): there, of the same kind, and - for a file - of the same
// size, ETag and modification time wherever both answers carry one. A
// folder's own time changes with its contents, so a folder is compared by
// kind alone. Answers nil, ErrChangedMeanwhile, or the Stat's own error.
func StillAsSeen(ctx context.Context, drv Driver, rel string, seen Object) error {
	obj, err := drv.Stat(ctx, rel)
	switch {
	case errors.Is(err, ErrNotFound):
		return ErrChangedMeanwhile
	case err != nil:
		return err
	}
	if obj.Kind != seen.Kind {
		return ErrChangedMeanwhile
	}
	if obj.Kind == KindDirectory {
		return nil
	}
	if obj.Size != seen.Size {
		return ErrChangedMeanwhile
	}
	if obj.Etag != "" && seen.Etag != "" && obj.Etag != seen.Etag {
		return ErrChangedMeanwhile
	}
	if !obj.Mtime.IsZero() && !seen.Mtime.IsZero() && !obj.Mtime.Equal(seen.Mtime) {
		return ErrChangedMeanwhile
	}
	return nil
}
