package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"image"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

func jar(t *testing.T, names ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.Create(n)
		require.NoError(t, err)
		_, _ = w.Write([]byte("x"))
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestDraw_ListsThePackage(t *testing.T) {
	out, err := draw("JAR", bytes.NewReader(jar(t, "META-INF/MANIFEST.MF", "com/example/App.class", "com/example/Util.class", "plugin.yml")))
	require.NoError(t, err)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out.Image))
	require.NoError(t, err)
	assert.Equal(t, "png", format)
	assert.LessOrEqual(t, cfg.Width, wire.ThumbnailMaxPixels)

	lines, files := listing(mustZip(t, jar(t, "META-INF/MANIFEST.MF", "com/example/App.class", "com/example/Util.class", "plugin.yml", "dir/")).File)
	assert.Equal(t, 4, files)
	assert.Equal(t, []string{"META-INF/  (1)", "com/  (2)", "plugin.yml"}, lines)
}

func TestDraw_RefusesWhatItCannotRead(t *testing.T) {
	_, err := draw("jar", bytes.NewReader([]byte("not a zip")))
	assert.Error(t, err, "filex then asks the next handler")
	_, err = draw("rar", bytes.NewReader(jar(t, "a")))
	assert.Error(t, err, "a kind it does not draw")
}

// The manifest the module describes is the file an administrator installs.
func TestManifest_IsFilexAppJSON(t *testing.T) {
	raw, err := os.ReadFile("filex-app.json")
	require.NoError(t, err)
	var onDisk wire.Manifest
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	a, _ := json.Marshal(onDisk)
	b, _ := json.Marshal(manifest)
	assert.JSONEq(t, string(a), string(b))
}

func mustZip(t *testing.T, b []byte) *zip.Reader {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	require.NoError(t, err)
	return zr
}
