package db

import (
	"reflect"
	"testing"
)

// The Audit page filters by RESOURCE: "user." (every user action) or several
// spellings of one resource at once ("app_plugin.,app-plugins."). An exact
// action, or anything that is not a list of prefixes, stays an exact match.
func TestAuditActionPrefixes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
		ok   bool
	}{
		{"user.", []string{"user.%"}, true},
		{"auth_provider.", []string{`auth\_provider.%`}, true},
		{"app_plugin.,app-plugins.", []string{`app\_plugin.%`, "app-plugins.%"}, true},
		{"user.create", nil, false},
		{"", nil, false},
		{".", nil, false},
		{"user.,bad", nil, false},
	} {
		got, ok := AuditActionPrefixes(tc.in)
		if ok != tc.ok || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q → %v %v, want %v %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
