package mailer

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func header(msg, name string) string {
	head, _, _ := strings.Cut(msg, "\r\n\r\n")
	for _, l := range strings.Split(head, "\r\n") {
		if k, v, ok := strings.Cut(l, ": "); ok && k == name {
			return v
		}
	}
	return ""
}

// ⚠⚠ A subject is written by a language pack now, and a header ends at a line
// break: a translation carrying CR LF would add headers of its own to every
// mail of that kind. Flattened to one line, it stays the subject.
func TestBuildMessage_ASubjectCannotAddHeaders(t *testing.T) {
	msg := string(buildMessage("f@x.test", "t@x.test", "Hola\r\nBcc: spy@evil.test\nX: y", "body", ""))
	head, _, _ := strings.Cut(msg, "\r\n\r\n")
	assert.NotContains(t, head, "\r\nBcc:")
	assert.NotContains(t, head, "\nX:")
	assert.Equal(t, "Hola Bcc: spy@evil.test X: y", header(msg, "Subject"))
}

// A header is ASCII: a Turkish, Spanish or Arabic subject goes as RFC 2047
// encoded-words that decode back to exactly the text.
func TestBuildMessage_ANonASCIISubjectIsEncoded(t *testing.T) {
	for _, subj := range []string{"informe.txt se ha compartido contigo — sí", "x dosyası sizinle paylaşıldı", "تمت مشاركة ملف معك"} {
		msg := string(buildMessage("f@x.test", "t@x.test", subj, "body", "es"))
		raw := header(msg, "Subject")
		for i := 0; i < len(raw); i++ {
			require.Less(t, raw[i], byte(0x80), "the header line is ASCII: %q", raw)
		}
		got, err := new(mime.WordDecoder).DecodeHeader(raw)
		require.NoError(t, err)
		assert.Equal(t, subj, got)
	}
	assert.Equal(t, "plain ascii", header(string(buildMessage("f", "t", "plain ascii", "b", "")), "Subject"), "ASCII stays readable")
}

func TestBuildMessage_ContentLanguage(t *testing.T) {
	assert.Equal(t, "es", header(string(buildMessage("f", "t", "s", "b", "es")), "Content-Language"))
	assert.Equal(t, "pt-br", header(string(buildMessage("f", "t", "s", "b", "pt-br")), "Content-Language"))
	assert.Empty(t, header(string(buildMessage("f", "t", "s", "b", "")), "Content-Language"))
	assert.Empty(t, header(string(buildMessage("f", "t", "s", "b", "es\r\nBcc: x")), "Content-Language"), "only a plain tag reaches a header")
	assert.Equal(t, "8bit", header(string(buildMessage("f", "t", "s", "b", "")), "Content-Transfer-Encoding"))
	assert.Equal(t, "ar", languageFrom(WithLanguage(context.Background(), "ar")))
	assert.Empty(t, languageFrom(context.Background()))
}

// A mail with HTML goes as multipart/alternative: the plain text first, the
// HTML last (a program shows the last part it can), both quoted-printable so
// no line is over the 998 bytes SMTP allows.
func TestComposeMessage_HTMLAndText(t *testing.T) {
	long := strings.Repeat("<td style=\"padding:0\">ş</td>", 200)
	msg := string(composeMessage("filex <noreply@files.example.com>", "a@example.com",
		Message{Subject: "Rapor paylaşıldı", Text: "Merhaba,\nhttps://files.example.com/s/a", HTML: "<html><body>" + long + "</body></html>"},
		"tr", time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)))
	ct := header(msg, "Content-Type")
	require.True(t, strings.HasPrefix(ct, "multipart/alternative; boundary="), ct)
	assert.Equal(t, "Wed, 11 Nov 2026 10:00:00 +0000", header(msg, "Date"))
	assert.Regexp(t, `^<[0-9a-f]{32}@files\.example\.com>$`, header(msg, "Message-ID"))
	for _, l := range strings.Split(msg, "\r\n") {
		require.LessOrEqual(t, len(l), 998, "a line over SMTP's limit")
	}
	_, params, err := mime.ParseMediaType(ct)
	require.NoError(t, err)
	_, body, _ := strings.Cut(msg, "\r\n\r\n")
	mr := multipart.NewReader(strings.NewReader(body), params["boundary"])
	var kinds, texts []string
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		// multipart.Part decodes quoted-printable itself.
		b, _ := io.ReadAll(p)
		kinds = append(kinds, p.Header.Get("Content-Type"))
		texts = append(texts, string(b))
	}
	assert.Equal(t, []string{"text/plain; charset=UTF-8", "text/html; charset=UTF-8"}, kinds)
	assert.Equal(t, "Merhaba,\r\nhttps://files.example.com/s/a", texts[0])
	assert.Contains(t, texts[1], long)
}

// A mail without HTML is the plain text alone, as before.
func TestComposeMessage_TextOnly(t *testing.T) {
	msg := string(composeMessage("f@x.test", "t@x.test", Message{Subject: "s", Text: "a\nb"}, "", time.Now()))
	assert.Equal(t, "text/plain; charset=UTF-8", header(msg, "Content-Type"))
	assert.Equal(t, "quoted-printable", header(msg, "Content-Transfer-Encoding"))
}
