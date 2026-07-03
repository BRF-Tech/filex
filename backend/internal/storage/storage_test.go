package storage

import (
	"context"
	"io"
	"testing"
)

func TestComputeCapabilities(t *testing.T) {
	d := &noOpDriver{caps: Capabilities{Read: true}}
	got := ComputeCapabilities(d)
	if !got.Read {
		t.Fatalf("expected Read true, got %+v", got)
	}
	if got.Write {
		t.Fatalf("noOpDriver shouldn't advertise Write")
	}
}

type noOpDriver struct{ caps Capabilities }

func (n *noOpDriver) Init(_ context.Context, _ map[string]any) error     { return nil }
func (n *noOpDriver) Name() string                                       { return "noop" }
func (n *noOpDriver) List(_ context.Context, _ string) ([]Object, error) { return nil, nil }
func (n *noOpDriver) Stat(_ context.Context, _ string) (Object, error)   { return Object{}, nil }
func (n *noOpDriver) Read(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}
func (n *noOpDriver) Capabilities() Capabilities { return n.caps }
