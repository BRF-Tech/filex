package filesync

import "fmt"

// ── the hold: a first run that would re-upload a stale mirror asks first ──
//
// A client whose baseline had never been written — a reinstalled app, a lost
// state folder — re-uploaded 9,665 files the server had cleaned up: with no
// history, "new here" and "deleted there" look the same, and only the person
// can tell them apart. So a FIRST run that would upload more than
// MassUploadThreshold local-only files into a server folder that already has
// files holds them instead, and the pair keeps holding until the person
// decides: `filex sync confirm` sends them, `filex sync discard` moves them
// into the local sync trash. Downloads and everything the baseline knows go on
// as usual; only this machine's unknown items wait.
//
// ⚠⚠ The hold is written BEFORE the pass carries out a single action, and a
// pass that cannot write it does nothing. The ledger checkpoints while a pass
// runs, and a checkpoint turns the next run into a non-first run — so a first
// run that was cut short (Ctrl-C, a closed laptop, a desktop quit) after its
// first checkpoint but before recording the hold used to leave the pair with
// a baseline and no hold, and the next run uploaded the whole stale mirror.

// MassUploadThreshold is how many local-only files a FIRST run may upload into
// a server folder that already has content before it stops and asks.
const MassUploadThreshold = 100

// shouldHold decides whether this pass holds its unknown local items: yes while
// the pair is already holding, and on a first run that would upload more than
// MassUploadThreshold files into a server folder with at least one file in it
// (triggered). An empty server folder is the obvious "upload my folder".
func (e *Engine) shouldHold(actions []Action, remote Snapshot, firstRun, holding bool) (hold, triggered bool) {
	if holding {
		return true, false
	}
	if !firstRun || e.Pair.File {
		return false, false
	}
	uploads := 0
	for _, a := range actions {
		if a.Kind == ActionUpload {
			uploads++
		}
	}
	if uploads <= MassUploadThreshold {
		return false, false
	}
	for _, n := range remote {
		if !n.IsDir {
			return true, true
		}
	}
	return false, false
}

// holdLocalOnly takes out every upload and remote mkdir for a path the
// baseline does not know, and marks conflicts on such paths to settle only if
// both sides hold the same bytes. It returns what is left to do and what was
// held (rel → local signature).
func holdLocalOnly(actions []Action, base Baseline, local Snapshot) ([]Action, map[string]string) {
	kept := make([]Action, 0, len(actions))
	held := map[string]string{}
	for _, a := range actions {
		switch a.Kind {
		case ActionUpload, ActionMkdirRemote:
			if _, known := base[a.Rel]; !known {
				held[a.Rel] = local[a.Rel].Signature()
				continue
			}
		case ActionConflict:
			if _, known := base[a.Rel]; !known {
				a.Hold = true
			}
		}
		kept = append(kept, a)
	}
	return kept, held
}

// holdPass applies the hold to one pass's plan: it reads whether the pair is
// holding, decides whether this pass starts a hold, filters the plan and — for
// a hold this pass starts, or a full pass's fresh list — writes the hold down
// BEFORE anything is carried out. full says the snapshots cover the whole pair
// (the list is replaced) rather than one folder (the list is added to).
func (e *Engine) holdPass(actions []Action, base Baseline, local, remote Snapshot, firstRun, full bool, res *Result) ([]Action, bool, error) {
	holding, herr := e.Store.Holding(e.Pair.ID)
	if herr != nil {
		// Unreadable: Holding already answered "holding" — fail closed, and say
		// why on the pass.
		res.Errors = append(res.Errors, "read the hold: "+herr.Error())
	}
	hold, triggered := e.shouldHold(actions, remote, firstRun, holding)
	if !hold {
		return actions, false, nil
	}
	kept, held := holdLocalOnly(actions, base, local)
	res.Held += len(held)
	if triggered || full || len(held) > 0 {
		if err := e.Store.recordHold(e.Pair.ID, triggered, full, held); err != nil {
			if triggered {
				// ⚠ Nothing has been done yet, and nothing will be: a hold that
				// is not on disk is a hold the next run does not know about.
				return nil, true, fmt.Errorf("could not record the items held for a decision, so nothing was transferred: %w", err)
			}
			res.Errors = append(res.Errors, "record held items: "+err.Error())
		}
	}
	if triggered {
		e.progressf("hold: %d item(s) here are not on the server — waiting for a decision "+
			"(`filex sync confirm %s` sends them, `filex sync discard %s` moves them to the local sync trash)",
			len(held), e.Pair.ID, e.Pair.ID)
	}
	return kept, true, nil
}

// recordHeldConflicts adds the conflicts a holding pass left alone (their two
// sides differ) to the hold's list — only while the hold is still on.
func (e *Engine) recordHeldConflicts(held map[string]string, res *Result) {
	if len(held) == 0 {
		return
	}
	if err := e.Store.recordHold(e.Pair.ID, false, false, held); err != nil {
		res.Errors = append(res.Errors, "record held items: "+err.Error())
	}
}
