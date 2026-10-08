package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #210 (A10): the download command is the server's. These are the rules the
// share dialog used to know by itself (packages/core lib/shareCli.ts, now
// gone): a folder's archive is behind ?zip=wait, the PIN rides as ?pin=, -L
// follows an S3 redirect, and a name is quoted for the shell it is pasted in.
func TestDownloadCommandFor(t *testing.T) {
	t.Run("a file with a PIN", func(t *testing.T) {
		c := downloadCommandFor("https://files.example.com/s/abc", "12345678", "q3.pdf", false)
		require.NotNil(t, c)
		assert.Equal(t, "curl -fSL -o 'q3.pdf' 'https://files.example.com/s/abc?pin=12345678'", c.Curl)
		assert.Equal(t, "Invoke-WebRequest -UseBasicParsing -Uri 'https://files.example.com/s/abc?pin=12345678' -OutFile 'q3.pdf'", c.PowerShell)
	})
	t.Run("a folder downloads its ZIP, waiting for it", func(t *testing.T) {
		c := downloadCommandFor("https://files.example.com/s/abc", "", "reports", true)
		assert.Equal(t, "curl -fSL -o 'reports.zip' 'https://files.example.com/s/abc?zip=wait'", c.Curl)
		assert.Contains(t, c.PowerShell, "-OutFile 'reports.zip'")
	})
	t.Run("a folder with a PIN carries both", func(t *testing.T) {
		c := downloadCommandFor("https://files.example.com/s/abc", "4321", "reports", true)
		assert.Contains(t, c.Curl, "?pin=4321&zip=wait")
	})
	t.Run("an address that already has a query", func(t *testing.T) {
		c := downloadCommandFor("https://files.example.com/s/abc?x=1", "99887766", "", false)
		assert.Equal(t, "curl -fSL -OJ 'https://files.example.com/s/abc?x=1&pin=99887766'", c.Curl)
	})
	t.Run("a PIN is query-escaped", func(t *testing.T) {
		c := downloadCommandFor("https://h/s/abc", "a b&c", "f.txt", false)
		assert.Contains(t, c.Curl, "pin=a+b%26c")
	})
	t.Run("a quote in the name is quoted for each shell", func(t *testing.T) {
		c := downloadCommandFor("https://h/s/abc", "", "ada's file.pdf", false)
		assert.Contains(t, c.Curl, `-o 'ada'\''s file.pdf'`)
		assert.Contains(t, c.PowerShell, `-OutFile 'ada''s file.pdf'`)
	})
	t.Run("-L is always there: an S3 install redirects", func(t *testing.T) {
		assert.Regexp(t, `^curl -fSL `, downloadCommandFor("https://h/s/abc", "", "f", false).Curl)
	})
	t.Run("no address, no command", func(t *testing.T) {
		assert.Nil(t, downloadCommandFor("", "", "f", false))
	})
}
