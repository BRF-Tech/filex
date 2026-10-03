package notify_test

// A tenant's own domain stopped working (docs/TENANT-ADMIN.md): the notice
// says WHY in each reader's language, from the check's code and names, and
// keeps the check's English sentence only for a code the catalogue does not
// know.

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

func TestTenantDomainSuspended_SaysWhyInEachLanguage(t *testing.T) {
	d := &model.ProviderDomain{
		Domain:          "files.acme.example",
		LastError:       "points at other.example, not acme.tenants.files.example",
		LastErrorCode:   model.DomainWhyPointsElsewhere,
		LastErrorParams: map[string]string{"found": "other.example", "target": "acme.tenants.files.example"},
	}
	ev := notify.TenantDomainChanged(7, d, true)
	assert.Equal(t, notify.EventTenantDomainSuspended, ev.Event)
	assert.Contains(t, ev.Meta["body_en"], "its CNAME points at other.example, not acme.tenants.files.example")
	assert.Contains(t, ev.Meta["body_tr"], "CNAME kaydı acme.tenants.files.example yerine other.example adresini gösteriyor")
	assert.NotContains(t, ev.Meta["body_tr"], "points at", "a Turkish reader is not told in English")
	assert.Equal(t, model.DomainWhyPointsElsewhere, ev.Meta["reason_code"])

	d.LastErrorCode, d.LastError = "something_newer", "a newer check's sentence"
	assert.Equal(t, "a newer check's sentence", notify.DomainReason("tr", d), "an unknown code keeps the sentence")
	assert.Equal(t, "", notify.DomainReason("tr", nil))
}
