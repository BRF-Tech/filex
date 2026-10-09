//go:build !linux

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func pvTestStartLingerer(string) error { return errors.New("the lingering helper is a Linux test") }

// Outside Linux there is no other user to start the plugin under test as, so
// the validator starts only when told it is a development run.
func TestPluginValidator_OutsideLinuxStartsOnlyWithInsecureDev(t *testing.T) {
	o := validatorOptions(t, t.TempDir())
	o.insecureDev = false
	err := runPluginValidator(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "--insecure-dev") {
		t.Fatalf("want a refusal naming --insecure-dev, got %v", err)
	}
	o.insecureDev = true
	if err := runPluginValidator(context.Background(), o); err != nil {
		t.Fatalf("with --insecure-dev: %v", err)
	}
}
