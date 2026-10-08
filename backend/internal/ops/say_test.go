package ops

// A trash job's row says where it stands, in the reader's language, and never
// sends a person to the server log (0.54, findings A4 and A15).
//
// RED PROOF: the rows had no `summary`; the explorer and the admin page each
// built their own sentence for "empty the trash" ("Trash emptied, but 3 items
// could not be purged - see the server log.", or the raw "504"), and the end
// of a restore or a permanent delete was worded in the browser from counts.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/srvtext"
)

func TestSayTrashEmpty_EveryEndIsSaidAndNoneSendsAPersonToTheLog(t *testing.T) {
	cases := []struct {
		name string
		op   Op
		en   string
		tr   string
	}{
		{"queued", Op{Kind: OpTrashEmpty, Status: StatusPending},
			"Emptying the trash… waiting for another purge to finish",
			"Çöp kutusu boşaltılıyor… başka bir temizliğin bitmesi bekleniyor"},
		{"running", Op{Kind: OpTrashEmpty, Status: StatusRunning, Done: 1200, Total: 61844},
			"Emptying the trash… 1,200 of 61,844",
			"Çöp kutusu boşaltılıyor… 1.200 / 61.844"},
		{"done", Op{Kind: OpTrashEmpty, Status: StatusOK, Done: 61844, Total: 61844, BytesDone: 2_500_000_000},
			"Trash emptied: 61,844 items deleted for good, 2.5 GB freed.",
			"Çöp kutusu boşaltıldı: 61.844 öğe kalıcı olarak silindi, 2,5 GB yer açıldı."},
		{"done, no bytes known", Op{Kind: OpTrashEmpty, Status: StatusOK, Done: 1, Total: 1},
			"Trash emptied: 1 item deleted for good.",
			"Çöp kutusu boşaltıldı: 1 öğe kalıcı olarak silindi."},
		{"nothing", Op{Kind: OpTrashEmpty, Status: StatusOK},
			"Trash emptied: there was nothing to delete.",
			"Çöp kutusu boşaltıldı: silinecek bir şey yoktu."},
		{"partly", Op{Kind: OpTrashEmpty, Status: StatusPartial, Done: 10, Failed: 3, Error: "3 of 10 items could not be purged"},
			"Trash emptied, but 3 items could not be deleted and are still in the trash.",
			"Çöp kutusu boşaltıldı, ancak 3 öğe silinemedi ve hâlâ çöp kutusunda."},
		{"none", Op{Kind: OpTrashEmpty, Status: StatusFailed, Done: 2, Failed: 2, Error: "2 items could not be purged"},
			"None of the 2 items could be deleted; they are still in the trash.",
			"2 öğenin hiçbiri silinemedi; hepsi hâlâ çöp kutusunda."},
		{"stopped", Op{Kind: OpTrashEmpty, Status: StatusFailed, Done: 5, Error: "trash: list: database is locked"},
			"Emptying the trash stopped before it finished, after 5 items. What is left is still in the trash.",
			"Çöp kutusunu boşaltma 5 öğeden sonra yarıda kaldı. Kalanlar hâlâ çöp kutusunda."},
		{"cancelled", Op{Kind: OpTrashEmpty, Status: StatusCancelled, Done: 7},
			"Emptying the trash was stopped after 7 items; the rest is still in the trash.",
			"Çöp kutusunu boşaltma 7 öğeden sonra durduruldu; kalanlar hâlâ çöp kutusunda."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			en := SayTrashEmpty("en", &c.op)
			assert.Equal(t, c.en, en)
			assert.Equal(t, c.tr, SayTrashEmpty("tr", &c.op))
			assert.NotContains(t, strings.ToLower(en), "log", "a person is never sent to the server log")
			assert.NotContains(t, en, "database", "the run's own error is not the sentence")
		})
	}
}

func TestSayTrashEmptyPreview_NamesTheCountAndTheSize(t *testing.T) {
	assert.Equal(t, "This permanently deletes 61,844 items (12.3 GB). It cannot be undone.",
		SayTrashEmptyPreview("en", 61844, 12_300_000_000))
	assert.Equal(t, "This permanently deletes 1 item. It cannot be undone.", SayTrashEmptyPreview("en", 1, 0))
	assert.Equal(t, "Bu işlem 61.844 öğeyi (12,3 GB) kalıcı olarak siler. Geri alınamaz.",
		SayTrashEmptyPreview("tr", 61844, 12_300_000_000))
	assert.Equal(t, "There is nothing to delete: the trash is empty.", SayTrashEmptyPreview("en", 0, 0))
}

func TestSayTrashBatch_WhatWentAndWhyTheRestDidNot(t *testing.T) {
	assert.Equal(t, "3 items restored", SayTrashBatch("en", OpRestore, false, 3, 0, "", ""))
	assert.Equal(t, "Restoring 1 item…", SayTrashBatch("en", OpRestore, true, 1, 0, "", ""))
	assert.Equal(t, "2 items restored - 1 item was not restored: something already has the name “Rapor.docx”",
		SayTrashBatch("en", OpRestore, false, 2, 1, ReasonExists, "Rapor.docx"))
	assert.Equal(t, "2 öğe geri getirildi - 1 öğe geri getirilmedi: aynı adla bir şey zaten var (“Rapor.docx”)",
		SayTrashBatch("tr", OpRestore, false, 2, 1, ReasonExists, "Rapor.docx"))
	assert.Equal(t, "2 items could not be restored", SayTrashBatch("en", OpRestore, false, 0, 2, ReasonFailed, ""))
	assert.Equal(t, "Deleting 4 items permanently…", SayTrashBatch("en", OpPurge, true, 4, 0, "", ""))
	assert.Equal(t, "4 items deleted permanently - 1 item was not deleted: you may not delete it for good",
		SayTrashBatch("en", OpPurge, false, 4, 1, ReasonForbidden, ""))
	// An unknown reason is said as the plain failure, never as a raw key.
	assert.Equal(t, "1 item could not be deleted permanently", SayTrashBatch("en", OpPurge, false, 0, 1, "weird", ""))
}

// The rows of the queue carry the sentence: a restore job whose entry found
// its place taken says so with the name, in the reader's language.
func TestSayRows_ARestoreJobSaysWhyAnEntryDidNotComeBack(t *testing.T) {
	ctx := srvtext.WithReader(context.Background(), "en")
	rows := []*Op{
		{Kind: OpRestore, Status: StatusPartial, Total: 3, Done: 2, Failed: 1, Error: TakenPrefix + "a.txt"},
		{Kind: OpPurge, Status: StatusFailed, Total: 1, Failed: 1, Error: "trash: the item is not in the trash"},
		{Kind: OpPurge, Status: StatusRunning, Total: 5},
		{Kind: OpCopy, Status: StatusOK, Total: 1, Done: 1},
	}
	sayRows(ctx, rows)
	assert.Equal(t, "2 items restored - 1 item was not restored: something already has the name “a.txt”", rows[0].Summary)
	assert.Equal(t, "1 item was not deleted: it is no longer in the trash", rows[1].Summary)
	assert.Equal(t, "Deleting 5 items permanently…", rows[2].Summary)
	assert.Empty(t, rows[3].Summary, "only trash jobs are said here")
}
