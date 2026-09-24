package mailer

import (
	"context"
	"mime"
	"strings"
	"testing"

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
