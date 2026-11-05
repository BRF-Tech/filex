package mailer

import (
	"errors"
	"net/smtp"
	"net/textproto"
	"slices"
	"strings"
)

// The SASL mechanisms filex logs in with.
const (
	mechPlain = "PLAIN"
	mechLogin = "LOGIN"
)

// openAuthed opens a client with open (smtpClient outside the tests) and, when
// the config has a username, logs in.
//
// Not every server takes AUTH PLAIN: some take LOGIN alone and answer PLAIN
// with 504 "Unrecognized authentication type" (GitHub #132). The server's AUTH
// line picks the mechanism, but that line is a hint rather than a promise, so
// a 504 moves on to the other one.
//
// ⚠ Only a 504 does. Every other refusal, 535 above all, is an answer about
// the credentials: asking again would double the failed logins that the
// 5-minute verification spends, and a provider counts those towards a lockout.
func openAuthed(cfg Config, open func(Config) (*smtp.Client, error)) (*smtp.Client, error) {
	c, err := open(cfg)
	if err != nil || cfg.Username == "" {
		return c, err
	}
	_, offered := c.Extension("AUTH")
	for i, mech := range mechanismOrder(offered) {
		if i > 0 {
			// A refused Client.Auth has already sent QUIT: the next try needs
			// a session of its own.
			if c, err = open(cfg); err != nil {
				return nil, err
			}
		}
		if err = c.Auth(newAuth(mech, cfg)); err == nil {
			return c, nil
		}
		_ = c.Close()
		if !isUnknownMechanism(err) {
			break
		}
	}
	return nil, err
}

// mechanismOrder is the order to try the mechanisms in, given the parameters
// of the server's AUTH line. PLAIN goes first unless the line names LOGIN and
// leaves PLAIN out. Mechanism names are case-insensitive (RFC 4954).
func mechanismOrder(offered string) []string {
	named := strings.Fields(strings.ToUpper(offered))
	if slices.Contains(named, mechLogin) && !slices.Contains(named, mechPlain) {
		return []string{mechLogin, mechPlain}
	}
	return []string{mechPlain, mechLogin}
}

func isUnknownMechanism(err error) bool {
	var tp *textproto.Error
	return errors.As(err, &tp) && tp.Code == 504
}

func newAuth(mech string, cfg Config) smtp.Auth {
	plain := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	if mech == mechLogin {
		return &loginAuth{guard: plain, username: cfg.Username, password: cfg.Password}
	}
	return plain
}

// loginAuth is the LOGIN mechanism, which net/smtp does not ship.
//
// ⚠⚠ LOGIN carries the password as readably as PLAIN does, so it needs the
// check PLAIN has: no credentials on an unencrypted connection unless the
// server is this machine. An empty smtp.tls makes STARTTLS opportunistic, so
// without the check whoever strips STARTTLS from the EHLO answer reads the
// password. guard is a PlainAuth, and its Start IS that check.
type loginAuth struct {
	guard              smtp.Auth
	username, password string
	step               int
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if _, _, err := a.guard.Start(server); err != nil {
		return "", nil, err
	}
	return mechLogin, nil, nil
}

// Next answers by position, not by reading the prompt: the mechanism never
// fixed its wording ("Username:", "User Name", "username:" are all in use).
func (a *loginAuth) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	a.step++
	switch a.step {
	case 1:
		return []byte(a.username), nil
	case 2:
		return []byte(a.password), nil
	}
	return nil, errors.New("smtp auth: LOGIN asked for more than a username and a password")
}
