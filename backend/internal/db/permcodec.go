package db

import (
	"encoding/json"
	"fmt"

	"github.com/brf-tech/filex/backend/internal/model"
)

// Permission rows (migration 00069) keep their structured parts as JSON
// text. Both hand-written drivers encode and decode through these so the
// three engines store byte-identical documents.

// EncodePermOverrides renders an overrides / effects map. nil encodes as {}.
func EncodePermOverrides(m map[string]string) (string, error) {
	if m == nil {
		m = map[string]string{}
	}
	b, err := json.Marshal(m)
	return string(b), err
}

// DecodePermOverrides parses an overrides / effects column. Empty text is an
// empty map (a MySQL JSON column never is, but a hand-edited row might be).
func DecodePermOverrides(s string) (map[string]string, error) {
	m := map[string]string{}
	if s == "" {
		return m, nil
	}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("permission overrides: %w", err)
	}
	return m, nil
}

// EncodePermConditions renders a rule's conditions column.
func EncodePermConditions(c model.PermRuleConditions) (string, error) {
	b, err := json.Marshal(c)
	return string(b), err
}

// DecodePermConditions parses a conditions column ("" and "{}" are none).
func DecodePermConditions(s string) (model.PermRuleConditions, error) {
	var c model.PermRuleConditions
	if s == "" {
		return c, nil
	}
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return c, fmt.Errorf("permission rule conditions: %w", err)
	}
	return c, nil
}

// EncodePermRule renders a rule's three JSON columns.
func EncodePermRule(r *model.PermissionRule) (targets, effects, settings string, err error) {
	ts := r.Targets
	if ts == nil {
		ts = []model.PermRuleTarget{}
	}
	tb, err := json.Marshal(ts)
	if err != nil {
		return "", "", "", err
	}
	effects, err = EncodePermOverrides(r.Effects)
	if err != nil {
		return "", "", "", err
	}
	sb, err := json.Marshal(r.Settings)
	if err != nil {
		return "", "", "", err
	}
	return string(tb), effects, string(sb), nil
}

// DecodePermRule fills a rule's structured fields from its three columns.
func DecodePermRule(r *model.PermissionRule, targets, effects, settings string) error {
	r.Targets = []model.PermRuleTarget{}
	if targets != "" {
		if err := json.Unmarshal([]byte(targets), &r.Targets); err != nil {
			return fmt.Errorf("permission rule %d targets: %w", r.ID, err)
		}
	}
	var err error
	if r.Effects, err = DecodePermOverrides(effects); err != nil {
		return fmt.Errorf("permission rule %d effects: %w", r.ID, err)
	}
	r.Settings = model.PermRuleSettings{}
	if settings != "" {
		if err := json.Unmarshal([]byte(settings), &r.Settings); err != nil {
			return fmt.Errorf("permission rule %d settings: %w", r.ID, err)
		}
	}
	return nil
}

// EncodePermRuleAll renders all four of a rule's JSON columns.
func EncodePermRuleAll(r *model.PermissionRule) (targets, effects, settings, conditions string, err error) {
	if targets, effects, settings, err = EncodePermRule(r); err != nil {
		return
	}
	conditions, err = EncodePermConditions(r.Conditions)
	return
}
