package ldap

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/go-ldap/ldap/v3"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/group"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Directory sync, groups (docs/LDAP.md → Directory sync): every group the
// directory lists becomes a filex group linked to it, so its members fill
// it. A group keeps its directory group's PERMANENT id (entryUUID, or
// objectGUID on Active Directory): renamed there, it is renamed here with
// its folders, role and members; gone from there, it stays here flagged
// "removed" — its LDAP members leave — until an administrator deletes it or
// keeps it as a filex group. The directory decides which groups exist: one
// deleted here while the directory has it is made again at the next sync —
// leave a group out on the directory, or with sync_group_filter.

// defaultSyncGroupFilter lists every group of the usual kinds: OpenLDAP's
// and lldap's groupOfNames / groupOfUniqueNames, posixGroup, and Active
// Directory's group.
const defaultSyncGroupFilter = "(|(objectClass=groupOfNames)(objectClass=groupOfUniqueNames)(objectClass=group)(objectClass=posixGroup))"

// idPrefix marks the ids this directory gives the groups it brings in: its
// provider name — "ldap:…", "ldap-partner:…" — so one directory's sync never
// flags another's groups as removed.
func (d *Driver) idPrefix() string { return d.Directory() + ":" }

func (d *Driver) syncGroupFilter() string {
	if d.syncGroupFilterRaw != "" {
		return d.syncGroupFilterRaw
	}
	return defaultSyncGroupFilter
}

// directoryGroup is one group the directory listed.
type directoryGroup struct {
	id, dn, name string
}

// groupID is a directory group's permanent id: entryUUID (OpenLDAP, lldap,
// 389-ds), else objectGUID (Active Directory, binary), else its DN — which
// changes when it is renamed, so a rename there is then a new group here.
func groupID(prefix string, e *ldap.Entry) string {
	if v := strings.TrimSpace(e.GetAttributeValue("entryUUID")); v != "" {
		return prefix + strings.ToLower(v)
	}
	if b := e.GetRawAttributeValue("objectGUID"); len(b) == 16 {
		return prefix + hex.EncodeToString(b)
	}
	return prefix + "dn:" + group.LDAPValue(e.DN)
}

// groupName is the name a directory group is shown by: its cn, else the
// value of its DN's first part, else the DN.
func groupName(e *ldap.Entry) string {
	if v := strings.TrimSpace(e.GetAttributeValue("cn")); v != "" {
		return v
	}
	if parsed, err := ldap.ParseDN(e.DN); err == nil && len(parsed.RDNs) > 0 && len(parsed.RDNs[0].Attributes) > 0 {
		return parsed.RDNs[0].Attributes[0].Value
	}
	return e.DN
}

// listGroups reads every group sync_group_filter finds under the group base.
func (d *Driver) listGroups(c conn) ([]directoryGroup, error) {
	base := d.groupBaseDN
	if base == "" {
		base = d.baseDN
	}
	res, err := c.SearchWithPaging(ldap.NewSearchRequest(
		base, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		d.syncGroupFilter(), []string{"cn", "entryUUID", "objectGUID"}, nil,
	), syncPageSize)
	if err != nil {
		return nil, err
	}
	out := make([]directoryGroup, 0, len(res.Entries))
	for _, e := range res.Entries {
		out = append(out, directoryGroup{id: groupID(d.idPrefix(), e), dn: e.DN, name: groupName(e)})
	}
	return out, nil
}

// importGroups brings the directory's groups in as filex groups (see the
// top of this file). ⚠ An empty list changes nothing: that is far likelier a
// wrong group base or filter than a directory with no groups.
func (d *Driver) importGroups(ctx context.Context, listed []directoryGroup, rep *auth.DirectorySyncReport) error {
	rep.GroupsFound = len(listed)
	if len(listed) == 0 {
		rep.Problem("groups", fmt.Errorf("the group search found no groups; no group was changed - check group_base_dn and sync_group_filter"))
		return nil
	}
	providerID, err := d.groupTenant(ctx)
	if err != nil {
		return err
	}
	existing, err := d.store.ListGroups(ctx)
	if err != nil {
		return err
	}
	synced := map[string]*model.Group{}
	handLinked := map[string]bool{} // LDAP groups an administrator linked by hand
	names := map[string]bool{}      // the tenant's taken names
	for _, g := range existing {
		if sameTenant(g.ProviderID, providerID) {
			names[g.Name] = true
		}
		if strings.HasPrefix(g.DirectoryID, d.idPrefix()) {
			synced[g.DirectoryID] = g
			continue
		}
		if g.DirectoryID != "" {
			continue // another directory's
		}
		for _, l := range g.Links {
			if l.Kind == model.GroupLinkLDAP {
				handLinked[group.LDAPValue(l.Value)] = true
			}
		}
	}

	seen := map[string]bool{}
	for _, dg := range listed {
		if err := ctx.Err(); err != nil {
			return err
		}
		seen[dg.id] = true
		link := model.GroupLink{Kind: model.GroupLinkLDAP, Value: group.LDAPValue(dg.dn)}
		if g := synced[dg.id]; g != nil {
			if err := d.refreshGroup(ctx, g, dg, link, names, rep); err != nil {
				rep.Problem(dg.name, err)
			}
			continue
		}
		if handLinked[link.Value] || handLinked[group.LDAPValue(dg.name)] {
			rep.GroupsLinkedByHand++
			continue
		}
		name := freeName(dg.name, names)
		if _, err := d.store.CreateGroup(ctx, &model.Group{
			Name:          name,
			Description:   "From the directory: " + dg.dn,
			ProviderID:    providerID,
			Links:         []model.GroupLink{link},
			DirectoryID:   dg.id,
			DirectoryName: dg.name,
		}); err != nil {
			rep.Problem(dg.name, err)
			continue
		}
		names[name] = true
		rep.GroupsCreated++
	}

	for id, g := range synced {
		if seen[id] || g.DirectoryState == model.GroupDirectoryRemoved {
			continue
		}
		if err := d.store.SetGroupDirectory(ctx, g.ID, g.DirectoryID, g.DirectoryName, model.GroupDirectoryRemoved); err != nil {
			rep.Problem(g.Name, err)
			continue
		}
		rep.GroupsRemoved++
	}
	return nil
}

// refreshGroup brings a group sync made in step with its directory group:
// back from "removed", its LDAP link the group's current DN (a synced
// group's LDAP link is the directory's), and its name the new one when the
// directory renamed it — unless an administrator renamed it here.
func (d *Driver) refreshGroup(ctx context.Context, g *model.Group, dg directoryGroup, link model.GroupLink, names map[string]bool, rep *auth.DirectorySyncReport) error {
	if g.DirectoryState == model.GroupDirectoryRemoved {
		rep.GroupsRestored++
	}
	links := []model.GroupLink{link}
	for _, l := range g.Links {
		if l.Kind != model.GroupLinkLDAP {
			links = append(links, l)
		}
	}
	name := g.Name
	if dg.name != g.DirectoryName && g.Name == g.DirectoryName {
		delete(names, g.Name)
		name = freeName(dg.name, names)
		names[name] = true
		rep.GroupsRenamed++
	}
	if name != g.Name || !sameLinks(links, g.Links) {
		g.Name, g.Links = name, links
		if err := d.store.UpdateGroup(ctx, g); err != nil {
			return err
		}
	}
	if g.DirectoryName != dg.name || g.DirectoryState != "" {
		return d.store.SetGroupDirectory(ctx, g.ID, g.DirectoryID, dg.name, "")
	}
	return nil
}

// groupTenant is the tenant synced groups belong to: install-wide on a
// single-tenant install; on a multi-tenant one the tenant new directory
// accounts are homed in (`provider`), else install-wide — the supertenant's.
func (d *Driver) groupTenant(ctx context.Context) (*int64, error) {
	if !d.homing.MultiTenant || d.homing.Pin == "" {
		return nil, nil
	}
	p, err := d.store.GetProviderBySlug(ctx, d.homing.Pin)
	if err != nil || p == nil {
		return nil, fmt.Errorf("ldap: the tenant %q for synced groups: %v", d.homing.Pin, err)
	}
	return &p.ID, nil
}

func sameTenant(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// freeName is name, or name (LDAP), (LDAP 2)… when the tenant has it.
func freeName(name string, taken map[string]bool) string {
	if len([]rune(name)) > group.MaxNameLen-10 {
		name = string([]rune(name)[:group.MaxNameLen-10])
	}
	if !taken[name] {
		return name
	}
	for i := 1; ; i++ {
		n := name + " (LDAP)"
		if i > 1 {
			n = fmt.Sprintf("%s (LDAP %d)", name, i)
		}
		if !taken[n] {
			return n
		}
	}
}

func sameLinks(a, b []model.GroupLink) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
