package plugin

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Process supervises ONE launched plugin binary: start it, wait for the
// handshake line, hand the client to whoever asked, restart it with backoff
// when it dies, stop it on request. It knows nothing about storage — the
// manager layers registration on top through the callbacks.
type Process struct {
	Name    string
	Binary  string // absolute path
	Token   string
	SockDir string // private directory the plugin may create its socket in
	Log     *slog.Logger

	// OnUp is called (from the supervisor goroutine) each time the plugin has
	// printed its handshake and answered nothing yet — the manager describes
	// it here and decides whether it is acceptable. Returning an error stops
	// the plugin for good (a plugin that fails validation will fail it again;
	// restarting would only churn).
	OnUp func(ctx context.Context, c *Client) error
	// OnDown is called each time the process is gone (exit, kill, failed
	// handshake), before any restart.
	OnDown func(err error)

	mu       sync.Mutex
	cmd      *exec.Cmd
	client   *Client
	stopping bool
	stopped  chan struct{}
	restarts int
	lastErr  error
}

// Errors reported through OnDown / Err.
var (
	ErrHandshake = errors.New("plugin did not print its handshake line")
	ErrRefused   = errors.New("plugin refused by host")
	ErrStopped   = errors.New("plugin stopped")
)

// Client is the live client, or nil while the plugin is not up.
func (p *Process) Client() *Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.client
}

// Err is the last failure the supervisor saw (nil while healthy).
func (p *Process) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastErr
}

// Restarts counts crashes since Start.
func (p *Process) Restarts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.restarts
}

// Start runs the supervise loop until ctx ends or Stop is called. It returns
// immediately; failures surface through OnDown and Err.
func (p *Process) Start(ctx context.Context) {
	p.mu.Lock()
	p.stopping = false
	p.stopped = make(chan struct{})
	p.mu.Unlock()
	go p.loop(ctx)
}

// Stop terminates the plugin and waits (bounded) for the loop to end.
func (p *Process) Stop() {
	p.mu.Lock()
	p.stopping = true
	cmd := p.cmd
	stopped := p.stopped
	p.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		terminate(cmd)
	}
	if stopped != nil {
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
		}
	}
}

func (p *Process) loop(ctx context.Context) {
	defer func() {
		p.mu.Lock()
		if p.stopped != nil {
			close(p.stopped)
		}
		p.mu.Unlock()
	}()
	backoff := time.Second
	for {
		p.mu.Lock()
		stopping := p.stopping
		p.mu.Unlock()
		if stopping || ctx.Err() != nil {
			return
		}
		err := p.runOnce(ctx)
		p.mu.Lock()
		p.client = nil
		p.cmd = nil
		p.lastErr = err
		stopping = p.stopping
		p.mu.Unlock()
		if p.OnDown != nil {
			p.OnDown(err)
		}
		if stopping || ctx.Err() != nil || errors.Is(err, ErrRefused) {
			return
		}
		p.mu.Lock()
		p.restarts++
		p.mu.Unlock()
		p.Log.Warn("plugin exited; restarting", slog.String("plugin", p.Name), slog.Any("err", err), slog.Duration("in", backoff))
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

// runOnce spawns the binary, waits for the handshake, reports up, then
// waits for exit. It returns the reason the process is no longer usable.
func (p *Process) runOnce(ctx context.Context) error {
	if err := os.MkdirAll(p.SockDir, 0o700); err != nil {
		return fmt.Errorf("plugin socket dir: %w", err)
	}
	cmd := exec.Command(p.Binary)
	cmd.Dir = filepath.Dir(p.Binary)
	cmd.Env = append(os.Environ(),
		"FILEX_PLUGIN_TOKEN="+p.Token,
		"FILEX_PLUGIN_SOCKET_DIR="+p.SockDir,
		"FILEX_PLUGIN_NAME="+p.Name,
		"FILEX_PLUGIN_PROTOCOL=1",
	)
	// Its own process group and resource ceilings — see limits_unix.go for
	// what these are and, more importantly, what they are not.
	applyLimits(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", p.Binary, err)
	}
	p.mu.Lock()
	p.cmd = cmd
	stopping := p.stopping
	p.mu.Unlock()
	if stopping {
		terminate(cmd)
		_ = cmd.Wait()
		return ErrStopped
	}
	go p.relay(stderr, "stderr")

	// The handshake is the FIRST line matching the prefix; anything else the
	// plugin prints before it is logged, not parsed.
	addrCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		found := false
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if !found && strings.HasPrefix(line, HandshakePrefix+" ") {
				found = true
				addrCh <- strings.TrimSpace(strings.TrimPrefix(line, HandshakePrefix))
				continue
			}
			if line != "" {
				p.Log.Info("plugin", slog.String("plugin", p.Name), slog.String("stdout", line))
			}
		}
		if !found {
			close(addrCh)
		}
	}()

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	var addrLine string
	select {
	case a, ok := <-addrCh:
		if !ok {
			// stdout closed before a handshake — the process died or never
			// spoke the protocol.
			err := <-waitCh
			return fmt.Errorf("%w (exit: %v)", ErrHandshake, err)
		}
		addrLine = a
	case err := <-waitCh:
		return fmt.Errorf("%w (exited before handshake: %v)", ErrHandshake, err)
	case <-time.After(20 * time.Second):
		terminate(cmd)
		<-waitCh
		return fmt.Errorf("%w within 20s", ErrHandshake)
	case <-ctx.Done():
		terminate(cmd)
		<-waitCh
		return ErrStopped
	}

	addr, err := ParseAddress(addrLine)
	if err != nil {
		terminate(cmd)
		<-waitCh
		return fmt.Errorf("%w: %v", ErrRefused, err)
	}
	client := NewClient(addr, p.Token)
	if p.OnUp != nil {
		upCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := p.OnUp(upCtx, client)
		cancel()
		if err != nil {
			client.Close()
			terminate(cmd)
			<-waitCh
			return fmt.Errorf("%w: %v", ErrRefused, err)
		}
	}
	p.mu.Lock()
	p.client = client
	p.lastErr = nil
	p.mu.Unlock()

	select {
	case err := <-waitCh:
		client.Close()
		if err == nil {
			err = errors.New("exited 0")
		}
		p.mu.Lock()
		stopping := p.stopping
		p.mu.Unlock()
		if stopping {
			return ErrStopped
		}
		return err
	case <-ctx.Done():
		terminate(cmd)
		<-waitCh
		client.Close()
		return ErrStopped
	}
}

// relay copies a plugin's output into filex's log, RATE LIMITED.
//
// ⚠ A plugin's stdout and stderr are somebody else's code writing into
// filex's log file. A debug build left chatty, or a tight retry loop printing
// an error per attempt, becomes filex filling the disk — and the first
// symptom is not "the plugin is noisy", it is "the server ran out of space".
// So the relay carries a per-second budget and says, once, when it starts
// dropping lines. The budget is generous enough that ordinary logging is
// untouched.
const (
	logLinesPerSecond = 50
	logBurst          = 200
)

func (p *Process) relay(r io.Reader, stream string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 16<<10), 256<<10)

	budget := logBurst
	window := time.Now()
	dropped := 0

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if elapsed := time.Since(window); elapsed >= time.Second {
			// Refill, and report what was lost in the window that just ended
			// — a silent drop would leave somebody debugging from a log that
			// lies by omission.
			if dropped > 0 {
				p.Log.Warn("plugin output rate-limited",
					slog.String("plugin", p.Name), slog.String("stream", stream),
					slog.Int("dropped_lines", dropped))
				dropped = 0
			}
			budget = logBurst
			window = time.Now()
		}
		if budget <= 0 {
			dropped++
			continue
		}
		budget--
		p.Log.Info("plugin", slog.String("plugin", p.Name), slog.String(stream, line))
	}
	if dropped > 0 {
		p.Log.Warn("plugin output rate-limited",
			slog.String("plugin", p.Name), slog.String("stream", stream),
			slog.Int("dropped_lines", dropped))
	}
}

// terminate asks politely on unix (SIGTERM, then SIGKILL after a grace) and
// kills outright on Windows, which has no equivalent of a catchable signal.
func terminate(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		_ = cmd.Process.Kill()
		return
	}
	_ = cmd.Process.Signal(os.Interrupt)
	go func(pid int) {
		time.Sleep(5 * time.Second)
		// ⚠ The GROUP, not just the process: a plugin that spawned a helper
		// otherwise leaves it holding the socket, and the next start fails
		// with an address already in use that nothing on screen explains.
		killGroup(pid)
	}(cmd.Process.Pid)
}
