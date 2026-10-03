package wasmplugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The sentences an administrator approves at install say what the code does.
//
//   - files:lock is not "for a limited time": file_lock takes up to 365 days,
//     and ttl_days -1 holds until the app or an administrator lifts it.
//   - files:read and files:write reach beyond "the file you pick": an app
//     that keeps records on a file reads and writes it again later (when it
//     wakes up on its own, for a public page's visitor), and an output may
//     land in a folder the person chooses.
func TestPermissionLabels_SayWhatTheCodeDoes(t *testing.T) {
	lock := PermFilesLock.Label("en")
	assert.NotContains(t, lock, "limited time")
	assert.Contains(t, lock, "up to a year")
	assert.Contains(t, lock, "until the app or an administrator lifts it")
	assert.Contains(t, lock, "administrators included")

	assert.Contains(t, PermFilesRead.Label("en"), "keeps records about")
	assert.Contains(t, PermFilesWrite.Label("en"), "a folder you choose")
	assert.Contains(t, PermFilesWrite.Label("en"), "new version")

	trLock := PermFilesLock.Label("tr")
	assert.NotContains(t, trLock, "süreli")
	assert.Contains(t, trLock, "en çok bir yıl")
	assert.Contains(t, trLock, "kaldırana kadar")
	assert.Contains(t, trLock, "yöneticiler dâhil")
	assert.Contains(t, PermFilesRead.Label("tr"), "kayıt tuttuğu")
	assert.Contains(t, PermFilesWrite.Label("tr"), "seçtiğiniz bir klasöre")
}
