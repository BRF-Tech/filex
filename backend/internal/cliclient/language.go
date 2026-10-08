package cliclient

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// AccountLanguage is the interface language the signed-in account chose
// (GET /api/auth/me → user.locale), "" when it chose none. The sync engine's
// event stream (`filex sync run --json`) speaks it when nobody named a
// language, the way the server says a notification in the account's language
// when the request names none.
func (c *Client) AccountLanguage(ctx context.Context) (string, error) {
	var me struct {
		User *struct {
			Locale string `json:"locale"`
		} `json:"user"`
	}
	if _, err := c.getJSONInto(ctx, "/api/auth/me", nil, "account", &me); err != nil {
		return "", err
	}
	if me.User == nil {
		return "", nil
	}
	return strings.TrimSpace(me.User.Locale), nil
}

// UILocale is the strings one language pack adds for code (GET
// /api/public/ui-locales/{code}): the interface's keys and the server's own
// `server.*` sentences, merged over every running pack. nil, nil when no
// running pack carries the language (404).
func (c *Client) UILocale(ctx context.Context, code string) (map[string]string, error) {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return nil, nil
	}
	var res struct {
		Strings map[string]string `json:"strings"`
	}
	_, err := c.getJSONInto(ctx, "/api/public/ui-locales/"+url.PathEscape(code), nil, "language pack", &res)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return res.Strings, nil
}
