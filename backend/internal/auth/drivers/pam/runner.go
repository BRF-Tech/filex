package pam

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// result is what one external command left behind.
type result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// runner runs ONE command: argv[0] with the rest as separate arguments — no
// shell, ever — and stdin as its standard input. A non-nil error means the
// command could not be run to completion (not found, killed by the timeout);
// a command that ran and failed is a result with a non-zero ExitCode.
type runner interface {
	Run(ctx context.Context, argv []string, stdin string) (result, error)
}

// maxCapture bounds what is kept of a command's output: a tool that floods its
// stderr must not grow filex's memory.
const maxCapture = 8 << 10

// safeEnv is the whole environment a command gets. LC_ALL=C pins the
// messages classify reads to English; nothing of filex's own environment
// (FILEX_SECRET_KEY, database URLs) reaches a process that runs as root.
var safeEnv = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "LANG=C"}

type execRunner struct{}

type capBuffer struct {
	bytes.Buffer
}

func (b *capBuffer) Write(p []byte) (int, error) {
	if room := maxCapture - b.Len(); room > 0 {
		if len(p) > room {
			b.Buffer.Write(p[:room])
		} else {
			b.Buffer.Write(p)
		}
	}
	return len(p), nil
}

func (execRunner) Run(ctx context.Context, argv []string, stdin string) (result, error) {
	if len(argv) == 0 {
		return result{}, errors.New("pam: empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = safeEnv
	cmd.Stdin = strings.NewReader(stdin)
	var so, se capBuffer
	cmd.Stdout, cmd.Stderr = &so, &se
	cmd.WaitDelay = 2 * time.Second
	detach(cmd)
	err := cmd.Run()
	res := result{Stdout: so.String(), Stderr: se.String()}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		res.ExitCode = ee.ExitCode()
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if res.ExitCode < 0 {
			// Killed by a signal.
			return res, errors.New("pam: the command was killed")
		}
	default:
		return res, err
	}
	return res, nil
}
