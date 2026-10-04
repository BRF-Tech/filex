package e2epolicy_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Every branch of the rule, in the order the design asks the layers: the
// tenant's ceiling, the policy, administrators, the permission, the approval.
func TestDecide(t *testing.T) {
	const (
		off       = model.E2EPolicyOff
		admins    = model.E2EPolicyAdmins
		permitted = model.E2EPolicyPermitted
		approval  = model.E2EPolicyApproval
	)
	cases := []struct {
		name string
		in   e2epolicy.Input
		ans  e2epolicy.Answer
		why  e2epolicy.Reason
	}{
		{"the ceiling binds an administrator",
			e2epolicy.Input{TenantAllowed: false, Policy: permitted, IsAdmin: true, HasPermission: true},
			e2epolicy.AnswerDenied, e2epolicy.ReasonTenantDisabled},
		{"the ceiling comes before the policy",
			e2epolicy.Input{TenantAllowed: false, Policy: off, HasPermission: true},
			e2epolicy.AnswerDenied, e2epolicy.ReasonTenantDisabled},
		{"off binds an administrator",
			e2epolicy.Input{TenantAllowed: true, Policy: off, IsAdmin: true, HasPermission: true},
			e2epolicy.AnswerDenied, e2epolicy.ReasonPolicyOff},
		{"off binds a person who holds the permission",
			e2epolicy.Input{TenantAllowed: true, Policy: off, HasPermission: true, HasApproval: true},
			e2epolicy.AnswerDenied, e2epolicy.ReasonPolicyOff},
		{"admins: a person is refused even with the permission",
			e2epolicy.Input{TenantAllowed: true, Policy: admins, HasPermission: true},
			e2epolicy.AnswerDenied, e2epolicy.ReasonAdminsOnly},
		{"admins: an administrator may",
			e2epolicy.Input{TenantAllowed: true, Policy: admins, IsAdmin: true},
			e2epolicy.AnswerAllowed, ""},
		{"permitted: roles cannot narrow an administrator",
			e2epolicy.Input{TenantAllowed: true, Policy: permitted, IsAdmin: true, HasPermission: false},
			e2epolicy.AnswerAllowed, ""},
		{"permitted: a person with the permission may",
			e2epolicy.Input{TenantAllowed: true, Policy: permitted, HasPermission: true},
			e2epolicy.AnswerAllowed, ""},
		{"permitted: a person without it may not",
			e2epolicy.Input{TenantAllowed: true, Policy: permitted},
			e2epolicy.AnswerDenied, e2epolicy.ReasonPermission},
		{"approval: an administrator needs no approval",
			e2epolicy.Input{TenantAllowed: true, Policy: approval, IsAdmin: true},
			e2epolicy.AnswerAllowed, ""},
		{"approval: the permission is asked before the approval",
			e2epolicy.Input{TenantAllowed: true, Policy: approval, HasApproval: true},
			e2epolicy.AnswerDenied, e2epolicy.ReasonPermission},
		{"approval: without one, the person may ask",
			e2epolicy.Input{TenantAllowed: true, Policy: approval, HasPermission: true},
			e2epolicy.AnswerRequest, e2epolicy.ReasonApprovalRequired},
		{"approval: with one, the person may",
			e2epolicy.Input{TenantAllowed: true, Policy: approval, HasPermission: true, HasApproval: true},
			e2epolicy.AnswerAllowed, ""},
		{"an unknown policy is permitted",
			e2epolicy.Input{TenantAllowed: true, Policy: "everyone", HasPermission: true},
			e2epolicy.AnswerAllowed, ""},
		{"an unknown policy still asks for the permission",
			e2epolicy.Input{TenantAllowed: true, Policy: "everyone"},
			e2epolicy.AnswerDenied, e2epolicy.ReasonPermission},
		{"no policy at all is permitted",
			e2epolicy.Input{TenantAllowed: true, HasPermission: true},
			e2epolicy.AnswerAllowed, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ans, why := e2epolicy.Decide(c.in)
			assert.Equal(t, c.ans, ans)
			assert.Equal(t, c.why, why)
		})
	}
}

// Only two names encrypt: the marker, and a .fxe in any case. Paths are
// storage-relative in any spelling a door has them.
func TestIsEncryptionNameAndTargetOf(t *testing.T) {
	cases := []struct {
		rel  string
		dir  string
		kind string
	}{
		{".filex-e2e.json", "", model.E2ERequestFolder},
		{"/.filex-e2e.json", "", model.E2ERequestFolder},
		{"Muhasebe/Bordrolar/.filex-e2e.json", "Muhasebe/Bordrolar", model.E2ERequestFolder},
		{"/Muhasebe/.filex-e2e.json", "Muhasebe", model.E2ERequestFolder},
		{".FILEX-E2E.JSON", "", model.E2ERequestFolder},
		{"Muhasebe/.Filex-E2E.json", "Muhasebe", model.E2ERequestFolder},
		{"rapor.pdf.fxe", "", model.E2ERequestFile},
		{"Muhasebe/Bordrolar/Ocak.PDF.FXE", "Muhasebe/Bordrolar", model.E2ERequestFile},
		{"a/./b/../c/Zm9vYmFyYmF6cXV4.Fxe", "a/c", model.E2ERequestFile},
		{"Muhasebe/rapor.pdf", "", ""},
		{"Muhasebe/.filex-e2e.json.bak", "", ""},
		{"Muhasebe/filex-e2e.json", "", ""},
		{"Muhasebe/.filex-e2e.json/ek.txt", "", ""},
		{"Muhasebe/kasa.fxe/ek.txt", "", ""},
		{".fxe", "", ""},
		{"", "", ""},
		{"/", "", ""},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%q", c.rel), func(t *testing.T) {
			dir, kind := e2epolicy.TargetOf(c.rel)
			assert.Equal(t, c.dir, dir)
			assert.Equal(t, c.kind, kind)
			assert.Equal(t, c.kind != "", e2epolicy.IsEncryptionName(c.rel))
		})
	}
}

// An approval is kept under a folder: the folder's own for a folder, the
// file's folder for a file (whose .fxe name nobody knows yet).
func TestApprovalPath(t *testing.T) {
	assert.Equal(t, "Muhasebe/Bordrolar", e2epolicy.ApprovalPath("/Muhasebe/Bordrolar/", model.E2ERequestFolder))
	assert.Equal(t, "", e2epolicy.ApprovalPath("", model.E2ERequestFolder))
	assert.Equal(t, "Muhasebe", e2epolicy.ApprovalPath("Muhasebe/rapor.pdf", model.E2ERequestFile))
	assert.Equal(t, "", e2epolicy.ApprovalPath("/rapor.pdf", model.E2ERequestFile))
}

func TestRefusedError(t *testing.T) {
	var err error = fmt.Errorf("upload: %w", &e2epolicy.RefusedError{Reason: e2epolicy.ReasonAdminsOnly})
	require.ErrorIs(t, err, e2epolicy.ErrRefused)
	var refused *e2epolicy.RefusedError
	require.True(t, errors.As(err, &refused))
	assert.Equal(t, e2epolicy.ReasonAdminsOnly, refused.Reason)
	assert.Contains(t, err.Error(), "admins_only")
}
