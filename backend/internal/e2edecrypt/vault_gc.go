package e2edecrypt

import (
	"time"

	"github.com/brf-tech/filex/backend/internal/e2e"
)

// Garbage collection - docs/E2E-VAULT-FORMAT.md → "Garbage collection". Only
// the lock holder collects: the server cannot read the index, and a reader
// holds no lock. Two kinds of dead bytes build up:
//
//   - the graveyard: packs the latest generation no longer uses, which an
//     older generation that is still kept may use;
//   - orphans: packs no generation ever used (an interrupted write, a lost
//     lock, a pack sent again under a new id).
//
// Generation g is expired when g ≤ latest - 3 and generation g+1 was committed
// more than 15 minutes ago (an index file that is gone counts as long ago):
// the three newest generations, and any replaced less than 15 minutes ago,
// stay for readers still working on them.

// VaultRetention is how long a replaced generation is kept for its readers.
const VaultRetention = 15 * time.Minute

// VaultGCPassMax is the most a collection deletes in one pass (and one
// delete request).
const VaultGCPassMax = 1000

// VaultGCPlan is one collection pass.
type VaultGCPlan struct {
	// Indexes are the expired generations whose index files go.
	Indexes []uint64
	// Packs go: graveyard packs whose last user expired, and orphans.
	Packs [][16]byte
	// Forget are the graveyard packs this pass deletes or finds gone: the
	// next commit drops them from the graveyard (VaultWriter.ForgetGrave).
	Forget [][16]byte
}

// Empty reports whether the pass deletes nothing.
func (p VaultGCPlan) Empty() bool { return len(p.Indexes) == 0 && len(p.Packs) == 0 }

// PlanVaultGC works out one collection pass of at most limit deletions.
// latest is the latest generation, verified; indexes and packs are the
// listings; mine are the packs this writer stored since it took the lock
// (not orphans: its commit may still name them). Nothing is planned for a
// vault whose latest is damaged, carries extension bytes, or is missing.
func PlanVaultGC(latest *VaultIndex, indexes, packs []VaultObject, mine map[[16]byte]bool, now time.Time, limit int) VaultGCPlan {
	var plan VaultGCPlan
	if latest == nil || latest.Generation == 0 || latest.ReadOnly != "" || limit <= 0 {
		return plan
	}
	l := latest.Generation
	committed := make(map[uint64]time.Time, len(indexes))
	for _, o := range indexes {
		committed[o.Generation] = o.ModTime
	}
	expired := func(g uint64) bool {
		if g+e2e.VaultKeepGenerations > l {
			return false
		}
		t, listed := committed[g+1]
		if !listed {
			return true
		}
		return !t.IsZero() && now.Sub(t) > VaultRetention
	}
	room := func() bool { return len(plan.Indexes)+len(plan.Packs) < limit }

	for _, o := range indexes {
		if !room() {
			break
		}
		if o.Generation < l && expired(o.Generation) {
			plan.Indexes = append(plan.Indexes, o.Generation)
		}
	}

	listed := make(map[[16]byte]bool, len(packs))
	for _, o := range packs {
		listed[o.Pack] = true
	}
	inGrave := make(map[[16]byte]bool, len(latest.Grave))
	for _, g := range latest.Grave {
		inGrave[g.Pack] = true
		if g.Died < 2 || !expired(g.Died-1) {
			continue
		}
		if !listed[g.Pack] {
			plan.Forget = append(plan.Forget, g.Pack)
			continue
		}
		if room() {
			plan.Packs = append(plan.Packs, g.Pack)
			plan.Forget = append(plan.Forget, g.Pack)
		}
	}

	live := make(map[[16]byte]bool, len(latest.Packs))
	for _, p := range latest.Packs {
		live[p] = true
	}
	for _, o := range packs {
		if !room() {
			break
		}
		if !live[o.Pack] && !inGrave[o.Pack] && !mine[o.Pack] {
			plan.Packs = append(plan.Packs, o.Pack)
		}
	}
	return plan
}

// VaultLiveBytes is, for each pack of the latest table, the bytes the latest
// tree's extents use in it.
func VaultLiveBytes(latest *VaultIndex) map[[16]byte]int64 {
	live := make(map[[16]byte]int64, len(latest.Packs))
	for _, p := range latest.Packs {
		live[p] = 0
	}
	_ = latest.Tree.Walk(func(n *VaultNode, _ int) error {
		if n.Content != nil {
			for _, x := range n.Content.Extents {
				live[x.Pack] += x.Length
			}
		}
		return nil
	})
	return live
}

// PlanVaultRepack returns the packs to repack after a commit, or nil. With S
// the packs with fewer live bytes than half a data area, it repacks when S
// has at least 2 packs, their live bytes fit in fewer packs than S has, and
// more than half of all the data areas in the table is dead.
func PlanVaultRepack(latest *VaultIndex, packLog2 int) [][16]byte {
	if latest == nil || latest.ReadOnly != "" || len(latest.Packs) < 2 {
		return nil
	}
	area := VaultPackDataArea(packLog2)
	live := VaultLiveBytes(latest)
	var s [][16]byte
	var liveS, liveAll int64
	for _, p := range latest.Packs {
		liveAll += live[p]
		if live[p]*2 < area {
			s = append(s, p)
			liveS += live[p]
		}
	}
	total := area * int64(len(latest.Packs))
	if len(s) < 2 || (liveS+area-1)/area >= int64(len(s)) || (total-liveAll)*2 <= total {
		return nil
	}
	return s
}
