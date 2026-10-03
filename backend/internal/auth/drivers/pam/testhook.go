package pam

import "context"

// defaultRunner runs the commands of every driver built without a runner of
// its own. It is execRunner; SwapForTest replaces it.
var defaultRunner runner = execRunner{}

type funcRunner func(argv []string, stdin string) (int, string, string, error)

func (f funcRunner) Run(ctx context.Context, argv []string, stdin string) (result, error) {
	type answer struct {
		code     int
		out, err string
		e        error
	}
	ch := make(chan answer, 1)
	go func() {
		code, out, errOut, err := f(argv, stdin)
		ch <- answer{code, out, errOut, err}
	}()
	select {
	case a := <-ch:
		return result{ExitCode: a.code, Stdout: a.out, Stderr: a.err}, a.e
	case <-ctx.Done():
		return result{}, ctx.Err()
	}
}

// SwapForTest replaces the process runner (every command the provider would
// run) and the directory PAM service files are looked up in, and returns the
// function that puts both back. It exists so tests of the layers above — the
// identity-providers handler, the provider set — can drive this provider
// without a real PAM, sudo or pamtester. Never call it outside a test.
func SwapForTest(run func(argv []string, stdin string) (exit int, stdout, stderr string, err error), dir string) (restore func()) {
	prevRun, prevDir, prevOK := defaultRunner, pamDir, platformOK
	defaultRunner, pamDir, platformOK = funcRunner(run), dir, true
	return func() { defaultRunner, pamDir, platformOK = prevRun, prevDir, prevOK }
}
