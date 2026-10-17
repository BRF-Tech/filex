package officeengine

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The tests run engine processes without WebAssembly: this test binary is
// the engine process too. Started with fakeEnv set, it speaks the engine's
// protocol (Hello, then one Request and one Answer per line) and does what
// each job's input file says (fakeJob); started with childEnv set, it is the
// real engine (Main) - the integration test's way to run x2t and the layout
// (real_test.go).
const (
	fakeEnv  = "FILEX_FAKE_OFFICE_ENGINE"
	childEnv = "FILEX_OFFICE_ENGINE_CHILD"
)

func TestMain(m *testing.M) {
	switch {
	case os.Getenv(childEnv) == "1":
		os.Exit(Main(os.Stdin, os.Stdout))
	case os.Getenv(fakeEnv) != "":
		os.Exit(fakeEngine(os.Getenv(fakeEnv)))
	}
	os.Exit(m.Run())
}

// fakeEngine is an engine process. mode:
//
//	ok            prepares at once, then runs jobs
//	prepare-wait  a prepare run waits for <cache>/gate to exist
//	prepare-fail  a prepare run says its module does not compile
//	no-hello      a job process never says it is ready
func fakeEngine(mode string) int {
	var cfg ChildConfig
	if err := json.Unmarshal([]byte(os.Getenv(ConfigEnv)), &cfg); err != nil {
		return 2
	}
	out := os.Stdout
	if cfg.Prepare {
		switch mode {
		case "prepare-fail":
			writeLine(out, Hello{Error: "office engine: x2t does not compile: a fake failure"})
			return 1
		case "prepare-wait":
			gate := filepath.Join(cfg.Cache, "gate")
			for {
				if _, err := os.Stat(gate); err == nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
		writeLine(out, Hello{Prepared: true})
		return 0
	}
	if mode == "no-hello" {
		time.Sleep(time.Hour)
	}
	writeLine(out, Hello{Ready: true})
	r := bufio.NewReader(os.Stdin)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return 0
		}
		var req Request
		if json.Unmarshal(line, &req) != nil {
			continue
		}
		a := fakeJob(req, cfg)
		a.ID = req.ID
		writeLine(out, a)
	}
}

// keep holds what "grow" allocated, so the memory stays in use.
var keep [][]byte

// fakeJob does what the input file's first word says:
//
//	pid           write this process's id
//	sleep <d>     sleep d (a module that does not end), then write the id
//	crash         end the process mid-job
//	x2t <code>    fail as x2t does with that return code
//	grow <MiB>    take that much memory and hold it, then wait
//	big <bytes>   make a result of that size
//	env           write this process's environment
//	request       write the request and the configuration as JSON
func fakeJob(req Request, cfg ChildConfig) Answer {
	b, err := os.ReadFile(req.In)
	if err != nil {
		return Answer{Code: CodeUnknown, What: "input", Error: err.Error()}
	}
	cmd, arg, _ := strings.Cut(strings.TrimSpace(string(b)), " ")
	write := func(data []byte) Answer {
		if req.MaxBytes > 0 && int64(len(data)) > req.MaxBytes {
			return Answer{Code: CodeTooLarge, What: "result", Over: true}
		}
		if err := os.WriteFile(req.Out, data, 0o600); err != nil {
			return Answer{Code: CodeUnknown, What: "io", Error: err.Error()}
		}
		return Answer{OK: true, Bytes: int64(len(data))}
	}
	pid := []byte(strconv.Itoa(os.Getpid()))
	switch cmd {
	case "pid":
		return write(pid)
	case "sleep":
		d, _ := time.ParseDuration(arg)
		time.Sleep(d)
		return write(pid)
	case "crash":
		os.Exit(3)
	case "x2t":
		n, _ := strconv.Atoi(arg)
		return Answer{Code: codeOfExit(uint32(n)), What: "x2t-" + arg, Error: "fake x2t"}
	case "grow":
		mb, _ := strconv.Atoi(arg)
		buf := make([]byte, mb<<20)
		for i := 0; i < len(buf); i += 4096 {
			buf[i] = 1
		}
		keep = append(keep, buf)
		runtime.KeepAlive(buf)
		time.Sleep(time.Minute)
		return write(pid)
	case "big":
		n, _ := strconv.Atoi(arg)
		return write(make([]byte, n))
	case "env":
		return write([]byte(strings.Join(os.Environ(), "\n")))
	case "request":
		j, _ := json.Marshal(map[string]any{"request": req, "config": cfg})
		return write(j)
	}
	return Answer{Code: CodeParams, What: "fake", Error: "unknown fake job " + cmd}
}
