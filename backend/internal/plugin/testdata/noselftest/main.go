// Command noselftest is a storage plugin that offers no /v1/selftest: it
// works, and proves nothing - what check_test.go holds CheckBinary to answer
// as CheckNoSelfTest.
package main

import (
	"context"
	"fmt"
	"io"

	"github.com/brf-tech/filex/backend/pkg/pluginsdk"
)

type empty struct{}

func (empty) List(context.Context, string) ([]pluginsdk.Object, error) { return nil, nil }

func (empty) Stat(_ context.Context, p string) (pluginsdk.Object, error) {
	if p == "" || p == "/" {
		return pluginsdk.Object{Path: "", Name: "/", Kind: pluginsdk.KindDir}, nil
	}
	return pluginsdk.Object{}, fmt.Errorf("%q: %w", p, pluginsdk.ErrNotFound)
}

func (empty) Read(_ context.Context, p string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("%q: %w", p, pluginsdk.ErrNotFound)
}

func main() {
	pluginsdk.Serve(pluginsdk.Plugin[empty]{
		Name:    "noselftest",
		Version: "0.1.0",
		Label:   "No selftest",
		Fields:  []pluginsdk.Field{{Key: "root", Type: pluginsdk.FieldString, Label: "Root", Required: true, Root: true}},
		Open:    func(context.Context, map[string]any) (empty, error) { return empty{}, nil },
	})
}
