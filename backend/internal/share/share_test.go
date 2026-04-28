package share

import (
	"testing"
	"time"

	"gitlab.com/brftech/filemanager/backend/internal/model"
)

func TestIsExpired(t *testing.T) {
	now := time.Now()
	s := &model.Share{}
	if s.IsExpired(now) {
		t.Fatal("share with no expiry should not be expired")
	}
	past := now.Add(-time.Minute)
	s.ExpiresAt = &past
	if !s.IsExpired(now) {
		t.Fatal("expired share not detected")
	}
}
