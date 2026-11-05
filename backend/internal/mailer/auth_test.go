package mailer

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeServer speaks the part of SMTP a login touches, over net.Pipe: no socket,
// so these tests also run on a machine whose firewall drops loopback.
type fakeServer struct {
	authLine string         // what follows "AUTH " in the EHLO answer; "" = no AUTH line
	refuse   map[string]int // mechanism -> the code its AUTH command gets
	prompts  []string       // the LOGIN prompts, "Username:" and "Password:" by default

	mu     sync.Mutex
	opened int
	lines  []string // every line the client sent
	logins []string // "<mechanism> <username> <password>", one per login that worked
}

func (f *fakeServer) open(cfg Config) (*smtp.Client, error) {
	f.mu.Lock()
	f.opened++
	f.mu.Unlock()
	client, server := net.Pipe()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	go f.serve(server)
	return smtp.NewClient(client, cfg.Host)
}

func (f *fakeServer) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lines...)
}

func (f *fakeServer) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	say := func(l string) { _, _ = fmt.Fprintf(conn, "%s\r\n", l) }
	read := func() (string, bool) {
		l, err := r.ReadString('\n')
		l = strings.TrimRight(l, "\r\n")
		f.mu.Lock()
		f.lines = append(f.lines, l)
		f.mu.Unlock()
		return l, err == nil
	}
	decoded := func(s string) string {
		b, _ := base64.StdEncoding.DecodeString(s)
		return string(b)
	}
	logged := func(mech, user, pass string) {
		f.mu.Lock()
		f.logins = append(f.logins, mech+" "+user+" "+pass)
		f.mu.Unlock()
		say("235 2.7.0 Authentication successful")
	}
	prompts := f.prompts
	if prompts == nil {
		prompts = []string{"Username:", "Password:"}
	}

	say("220 fake ESMTP")
	for {
		line, ok := read()
		if !ok {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO":
			if f.authLine == "" {
				say("250 fake")
				continue
			}
			say("250-fake")
			say("250 AUTH " + f.authLine)
		case "AUTH":
			mech, initial, _ := strings.Cut(arg, " ")
			if code := f.refuse[mech]; code != 0 {
				say(fmt.Sprintf("%d 5.7.4 refused", code))
				continue
			}
			if mech == mechPlain {
				parts := strings.Split(decoded(initial), "\x00")
				logged(mech, parts[1], parts[2])
				continue
			}
			answers := make([]string, 0, len(prompts))
			for _, p := range prompts {
				say("334 " + base64.StdEncoding.EncodeToString([]byte(p)))
				a, ok := read()
				if !ok {
					return
				}
				if a == "*" {
					break
				}
				answers = append(answers, decoded(a))
			}
			if len(answers) == len(prompts) {
				logged(mech, answers[0], answers[1])
				continue
			}
			say("501 5.7.0 aborted")
		case "NOOP":
			say("250 2.0.0 OK")
		case "QUIT":
			say("221 2.0.0 Bye")
			return
		default:
			say("501 5.5.2 Syntax error")
		}
	}
}

// localhost: the one name net/smtp's check lets log in without TLS, which is
// what a net.Pipe is.
var authConfig = Config{Host: "localhost", Port: "25", Username: "user", Password: "secret", From: "f@x.test"}

func TestOpenAuthed_PicksTheMechanismTheServerTakes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		authLine string
		refuse   map[string]int
		want     string
		opened   int
	}{
		{name: "LOGIN alone, the #132 server", authLine: "LOGIN", refuse: map[string]int{mechPlain: 504}, want: "LOGIN user secret", opened: 1},
		{name: "LOGIN beside mechanisms filex does not speak", authLine: "GSSAPI NTLM LOGIN", refuse: map[string]int{mechPlain: 504}, want: "LOGIN user secret", opened: 1},
		{name: "names are case-insensitive", authLine: "login", refuse: map[string]int{mechPlain: 504}, want: "LOGIN user secret", opened: 1},
		{name: "both offered stays PLAIN", authLine: "PLAIN LOGIN", want: "PLAIN user secret", opened: 1},
		{name: "PLAIN alone", authLine: "PLAIN", want: "PLAIN user secret", opened: 1},
		{name: "offers PLAIN but refuses it", authLine: "PLAIN LOGIN", refuse: map[string]int{mechPlain: 504}, want: "LOGIN user secret", opened: 2},
		{name: "no AUTH line, takes LOGIN", refuse: map[string]int{mechPlain: 504}, want: "LOGIN user secret", opened: 2},
		{name: "no AUTH line, takes PLAIN", want: "PLAIN user secret", opened: 1},
		{name: "offers LOGIN but refuses it", authLine: "LOGIN", refuse: map[string]int{mechLogin: 504}, want: "PLAIN user secret", opened: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeServer{authLine: tc.authLine, refuse: tc.refuse}
			c, err := openAuthed(authConfig, f.open)
			require.NoError(t, err)
			defer c.Close()

			require.NoError(t, c.Noop(), "the session must still be usable after the login")
			assert.Equal(t, []string{tc.want}, f.logins)
			assert.Equal(t, tc.opened, f.opened)
		})
	}
}

// ⚠ A 535 is about the credentials. Trying the other mechanism would spend a
// second failed login on every verification, towards a lockout.
func TestOpenAuthed_AWrongPasswordIsNotAskedTwice(t *testing.T) {
	f := &fakeServer{authLine: "PLAIN LOGIN", refuse: map[string]int{mechPlain: 535, mechLogin: 535}}
	c, err := openAuthed(authConfig, f.open)
	require.Error(t, err)
	assert.Nil(t, c)
	assert.Equal(t, "auth", Reason(err))
	assert.Equal(t, 1, f.opened)
}

// A server that takes neither: the answer is the server's own words, and the
// screen says the login failed, not that the message was refused.
func TestOpenAuthed_NeitherMechanismIsAnAuthFailure(t *testing.T) {
	f := &fakeServer{authLine: "NTLM", refuse: map[string]int{mechPlain: 504, mechLogin: 504}}
	c, err := openAuthed(authConfig, f.open)
	require.Error(t, err)
	assert.Nil(t, c)
	assert.Contains(t, err.Error(), "5.7.4")
	assert.Equal(t, "auth", Reason(err))
	assert.Equal(t, 2, f.opened)
}

// ⚠⚠ LOGIN must not be the way around PLAIN's check: on an unencrypted
// connection to another machine nothing of the login may leave, whatever the
// server offers. An attacker who strips STARTTLS writes the AUTH line too.
func TestOpenAuthed_NoCredentialsOverAnUnencryptedConnection(t *testing.T) {
	for _, authLine := range []string{"LOGIN", "PLAIN", "PLAIN LOGIN", ""} {
		t.Run("offers "+authLine, func(t *testing.T) {
			cfg := authConfig
			cfg.Host = "mail.example.test"
			f := &fakeServer{authLine: authLine}
			c, err := openAuthed(cfg, f.open)
			require.Error(t, err)
			assert.Nil(t, c)
			assert.Contains(t, err.Error(), "unencrypted connection")
			assert.Equal(t, 1, f.opened)
			for _, l := range f.sent() {
				assert.NotContains(t, strings.ToUpper(l), "AUTH")
			}
			assert.Empty(t, f.logins)
		})
	}
}

func TestOpenAuthed_NoUsernameNoLogin(t *testing.T) {
	cfg := authConfig
	cfg.Username = ""
	f := &fakeServer{authLine: "PLAIN LOGIN"}
	c, err := openAuthed(cfg, f.open)
	require.NoError(t, err)
	defer c.Close()

	require.NoError(t, c.Noop())
	for _, l := range f.sent() {
		assert.NotContains(t, strings.ToUpper(l), "AUTH")
	}
}

func TestLoginAuth_AnswersByPositionNotByThePromptsWording(t *testing.T) {
	f := &fakeServer{authLine: "LOGIN", prompts: []string{"User Name\x00", "Pass Word\x00"}}
	c, err := openAuthed(authConfig, f.open)
	require.NoError(t, err)
	defer c.Close()

	assert.Equal(t, []string{"LOGIN user secret"}, f.logins)
}

// A third prompt is not the LOGIN exchange: the client stops instead of
// sending the password again to whatever asked.
func TestLoginAuth_StopsAtAThirdPrompt(t *testing.T) {
	f := &fakeServer{authLine: "LOGIN", prompts: []string{"Username:", "Password:", "Password again:"}}
	c, err := openAuthed(authConfig, f.open)
	require.Error(t, err)
	assert.Nil(t, c)
	assert.Contains(t, err.Error(), "more than a username and a password")
	assert.Empty(t, f.logins)
}
