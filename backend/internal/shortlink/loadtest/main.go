// Command loadtest floods an app's public route - the route door
// (internal/api/handlers/route_door.go) in front of the link shortener
// example (backend/examples/app-shortlinks) - and says what came back and how
// fast. README.md beside it defines the scenarios (H3, H4), how a run is set
// up and what was measured; run.sh does a whole run on a Linux host.
//
//	loadtest setup -url http://127.0.0.1:18080 -wasm shortlinks.wasm -manifest filex-app.json
//	loadtest run   -url http://127.0.0.1:18080 -scenario H3 -pid <filex's pid>
//	loadtest run   -url http://127.0.0.1:18080 -scenario H4 -pid <filex's pid>
//
// Visitors are told apart by X-Real-IP, which filex believes from a loopback
// peer only (internal/clientip): run it on the machine filex runs on, against
// 127.0.0.1. Each scenario uses addresses of its own in 198.18.0.0/15 (the
// benchmarking range, RFC 2544), so one run's lockouts do not touch the next.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "setup":
		err = setup(os.Args[2:])
	case "edit":
		err = edit(os.Args[2:])
	case "run":
		err = run(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: loadtest setup|edit|run [flags] (see README.md)")
	os.Exit(2)
}

// signIn is the administrator's API client.
func signIn(base, email string) (*api, error) {
	password := os.Getenv("FILEX_ADMIN_PASSWORD")
	if password == "" {
		return nil, errors.New("FILEX_ADMIN_PASSWORD is not set")
	}
	c := &api{base: strings.TrimRight(base, "/")}
	var login struct {
		Token string `json:"token"`
	}
	if err := c.json(http.MethodPost, "/api/auth/login", map[string]any{"email": email, "password": password}, &login); err != nil {
		return nil, fmt.Errorf("sign in: %w", err)
	}
	c.token = login.Token
	return c, nil
}

// ── edit: the door's pages replaced by the administrator's templates ────────

// edit saves an install-wide page.error and page.route (docs/TEMPLATES.md):
// from then on a build that draws the door's pages from templates draws them
// in the wasm sandbox, the dearest render it has - what H4 then measures.
func edit(args []string) error {
	fs := flag.NewFlagSet("edit", flag.ExitOnError)
	base := fs.String("url", "http://127.0.0.1:18080", "filex's address")
	email := fs.String("email", envOr("FILEX_ADMIN_EMAIL", "loadtest@example.com"), "the administrator's e-mail")
	_ = fs.Parse(args)
	c, err := signIn(*base, *email)
	if err != nil {
		return err
	}
	for key, body := range map[string]string{
		"page.error": `<div class="card"><div class="code">{{ error.code }}</div><h1>{{ error.title }}</h1>` +
			`<p class="sub">{{ error.body }}</p><p class="sub" style="margin-bottom:0">Edited for the load test.</p></div>`,
		"page.route": `<div class="card"><h1>{{ route.title }}</h1><p class="sub">{{ route.text }}</p>` +
			`{% if route.link != blank %}<a class="btn" href="{{ route.link }}">{{ route.link_label }}</a>{% endif %}</div>`,
	} {
		err := c.json(http.MethodPut, "/api/admin/templates/"+key, map[string]any{
			"subject": "", "body": body, "text": "", "revision": 0, "lang": "en",
		}, nil)
		if err != nil {
			// A build before #324 has no page.route: the door draws no
			// template there, and H4 measures it as it is.
			fmt.Printf("edit: %s not saved (%v)\n", key, err)
			continue
		}
		fmt.Printf("edit: %s saved\n", key)
	}
	return nil
}

// ── setup: the shortener installed and answering under /<prefix>/ ───────────

func setup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	base := fs.String("url", "http://127.0.0.1:18080", "filex's address")
	email := fs.String("email", envOr("FILEX_ADMIN_EMAIL", "loadtest@example.com"), "the administrator's e-mail")
	wasmPath := fs.String("wasm", "", "the shortener's module (built from backend/examples/app-shortlinks)")
	manifestPath := fs.String("manifest", "", "its filex-app.json")
	prefix := fs.String("prefix", "l", "the path the addresses are under")
	_ = fs.Parse(args)
	if *wasmPath == "" || *manifestPath == "" {
		return errors.New("setup needs -wasm and -manifest")
	}
	c, err := signIn(*base, *email)
	if err != nil {
		return err
	}

	wasm, err := os.ReadFile(*wasmPath)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		return err
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	// The build under test is not a release: its version says nothing about
	// the app's range, which the store and the install check elsewhere.
	delete(manifest, "filex")
	delete(manifest, "min_filex")
	mbytes, _ := json.Marshal(manifest)
	grant, _ := json.Marshal(map[string]any{"permissions": manifest["permissions"]})
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	_ = mw.WriteField("grant", string(grant))
	part, _ := mw.CreateFormFile("wasm", "plugin.wasm")
	_, _ = part.Write(wasm)
	part, _ = mw.CreateFormFile("manifest", "filex-app.json")
	_, _ = part.Write(mbytes)
	_ = mw.Close()
	var installed struct {
		ID     int64 `json:"id"`
		Plugin struct {
			ID int64 `json:"id"`
		} `json:"plugin"`
	}
	if err := c.do(http.MethodPost, "/api/admin/app-plugins", mw.FormDataContentType(), form.Bytes(), &installed); err != nil {
		return fmt.Errorf("install: %w", err)
	}
	id := installed.ID
	if id == 0 {
		id = installed.Plugin.ID
	}
	app := fmt.Sprintf("/api/admin/app-plugins/%d", id)
	if err := c.json(http.MethodPost, app+"/setup/view", map[string]any{
		"event": "submit", "action_id": "save", "data": map[string]any{"values": map[string]any{"alphabet": "alnum"}},
	}, nil); err != nil {
		return fmt.Errorf("the app's setup step: %w", err)
	}
	if err := c.json(http.MethodPost, app+"/setup/routes/go/bases", map[string]any{"prefix": *prefix, "default": true}, nil); err != nil {
		return fmt.Errorf("the address: %w", err)
	}
	if err := c.json(http.MethodPost, app+"/setup/complete", nil, nil); err != nil {
		return fmt.Errorf("complete: %w", err)
	}
	// The router takes the address up on its own goroutine: wait for a miss.
	probe := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < 100; i++ {
		req, _ := http.NewRequest(http.MethodGet, c.base+"/"+*prefix+"/probe0000", nil)
		req.Header.Set("X-Real-IP", "198.19.255.254")
		if resp, err := probe.Do(req); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound && strings.HasPrefix(resp.Header.Get("Content-Security-Policy"), "default-src 'none'") {
				fmt.Printf("set up: app %d answers %s/%s/<code>\n", id, c.base, *prefix)
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("the address does not answer as the route door")
}

type api struct {
	base, token string
	hc          http.Client
}

func (c *api) json(method, path string, body, out any) error {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	return c.do(method, path, "application/json", b, out)
}

func (c *api) do(method, path, ctype string, body []byte, out any) error {
	req, err := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", ctype)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, firstBytes(raw, 300))
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// ── run: one scenario ────────────────────────────────────────────────────────

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	base := fs.String("url", "http://127.0.0.1:18080", "filex's address")
	prefix := fs.String("prefix", "l", "the path the addresses are under")
	scenario := fs.String("scenario", "", "H3 or H4")
	pid := fs.Int("pid", 0, "filex's process id: its CPU time is read from /proc (Linux)")
	// H3
	clients := fs.Int("clients", 1000, "H3: visitors")
	tries := fs.Int("tries", 30, "H3: guesses each visitor makes, one after the other")
	parallel := fs.Int("parallel", 64, "H3: visitors guessing at once")
	// H4
	rate := fs.Int("rate", 10000, "H4: visits a second, sent on a fixed schedule")
	duration := fs.Duration("duration", 30*time.Second, "H4: how long")
	pool := fs.Int("pool", 500, "H4: visitors the visits are drawn from")
	offset := fs.Int("offset", 0, "H4: the first visitor's number (a second H4 on the same server uses visitors of its own)")
	workers := fs.Int("workers", 512, "H4: connections")
	jsonOut := fs.String("json", "", "also write the result here")
	label := fs.String("label", "", "the name the result is printed under (default: the scenario)")
	_ = fs.Parse(args)

	target := strings.TrimRight(*base, "/") + "/" + strings.Trim(*prefix, "/") + "/"
	var res *result
	cpu0 := cpuSeconds(*pid)
	start := time.Now()
	switch strings.ToUpper(*scenario) {
	case "H3":
		res = runH3(target, *clients, *tries, *parallel)
	case "H4":
		res = runH4(target, *rate, *duration, *pool, *offset, *workers)
	default:
		return errors.New("-scenario H3 or H4")
	}
	res.Scenario = strings.ToUpper(*scenario)
	if *label != "" {
		res.Scenario = *label
	}
	res.Wall = time.Since(start).Seconds()
	res.Rate = float64(res.Sent) / res.Wall
	if cpu0 >= 0 {
		if cpu1 := cpuSeconds(*pid); cpu1 >= 0 {
			res.ServerCPUSeconds = cpu1 - cpu0
			res.ServerCores = res.ServerCPUSeconds / res.Wall
		}
	}
	res.print(os.Stdout)
	if *jsonOut != "" {
		b, _ := json.MarshalIndent(res, "", "  ")
		if err := os.WriteFile(*jsonOut, append(b, '\n'), 0o644); err != nil {
			return err
		}
	}
	if len(res.Problems) > 0 {
		return fmt.Errorf("%s: %d problem(s)", res.Scenario, len(res.Problems))
	}
	return nil
}

// result is what a scenario saw.
type result struct {
	Scenario         string             `json:"scenario"`
	Sent             int                `json:"sent"`
	Status           map[string]int     `json:"status"` // "404", "429", "503", "error"
	Wall             float64            `json:"wall_seconds"`
	Rate             float64            `json:"achieved_per_second"`
	P50, P90, P99    float64            `json:"-"`
	P999, Max        float64            `json:"-"`
	Latency          map[string]float64 `json:"latency_ms"`
	Shed             float64            `json:"shed_percent"` // 503s of all answers
	ServerCPUSeconds float64            `json:"server_cpu_seconds,omitempty"`
	ServerCores      float64            `json:"server_cores,omitempty"`
	// H3: visitors whose guesses were exactly MissBurst 404s, then 429s.
	Exact    int      `json:"exact_lockouts,omitempty"`
	Visitors int      `json:"visitors,omitempty"`
	Problems []string `json:"problems,omitempty"`
}

func (r *result) print(w io.Writer) {
	keys := make([]string, 0, len(r.Status))
	for k := range r.Status {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var st []string
	for _, k := range keys {
		st = append(st, fmt.Sprintf("%s=%d", k, r.Status[k]))
	}
	fmt.Fprintf(w, "%s: %d visits in %.1f s (%.0f/s) - %s - shed %.2f%%\n", r.Scenario, r.Sent, r.Wall, r.Rate, strings.Join(st, " "), r.Shed)
	fmt.Fprintf(w, "  latency ms: p50 %.3f  p90 %.3f  p99 %.3f  p99.9 %.3f  max %.3f\n", r.P50, r.P90, r.P99, r.P999, r.Max)
	if r.ServerCPUSeconds > 0 {
		fmt.Fprintf(w, "  filex CPU: %.1f s, %.2f cores\n", r.ServerCPUSeconds, r.ServerCores)
	}
	if r.Visitors > 0 {
		fmt.Fprintf(w, "  lockout: %d of %d visitors got exactly %d misses, then too many tries\n", r.Exact, r.Visitors, missBurst)
	}
	for _, p := range r.Problems {
		fmt.Fprintln(w, "  PROBLEM:", p)
	}
}

// missBurst is shortlink.MissBurst: the misses a visitor gets before the
// lockout. Written out so that the runner builds without filex's packages.
const missBurst = 20

// newClient is a keep-alive client with conns connections to one host.
func newClient(conns int) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:        conns,
			MaxIdleConnsPerHost: conns,
			MaxConnsPerHost:     conns,
			IdleConnTimeout:     time.Minute,
			DisableCompression:  true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// visit sends one GET as visitor ip and answers the status ("error" when
// none came back).
func visit(c *http.Client, url, ip string) string {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return "error"
	}
	req.Header.Set("X-Real-IP", ip)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Firefox/140.0")
	req.Header.Set("Accept-Language", "en")
	// What a browser says (Firefox's); the body is read and dropped as it is.
	req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	resp, err := c.Do(req)
	if err != nil {
		return "error"
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return strconv.Itoa(resp.StatusCode)
}

// code is a random guess in the shortener's default alphabet.
func code(rng *rand.Rand) string {
	const symbols = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = symbols[rng.IntN(len(symbols))]
	}
	return string(b)
}

// ip is visitor n of a scenario's own /16 of 198.18.0.0/15.
func ip(scenario byte, n int) string {
	return fmt.Sprintf("198.%d.%d.%d", 18+int(scenario), (n>>8)&0xff, n&0xff)
}

// H3: the lockout holds under load. clients visitors, parallel at a time,
// each guessing tries random codes one after the other: each must get
// exactly missBurst 404s, then 429s - not one guess more reaches the app,
// none fewer. A guess answered 503 (the app's instances all busy, or the
// door shedding) is not a guess the app judged: the visitor sends it again
// after shedPause, as a guesser would, and the 503 is counted apart.
func runH3(target string, clients, tries, parallel int) *result {
	c := newClient(parallel)
	type answer struct {
		status string
		ms     float64
	}
	per := make([][]answer, clients)
	var shed, errs atomic.Int64
	var mu sync.Mutex
	var lat []float64
	next := atomic.Int64{}
	var wg sync.WaitGroup
	for w := 0; w < parallel; w++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(seed, 3))
			var mine []float64
			defer func() {
				mu.Lock()
				lat = append(lat, mine...)
				mu.Unlock()
			}()
			for {
				n := int(next.Add(1) - 1)
				if n >= clients {
					return
				}
				addr := ip(0, n)
				for i := 0; i < tries; i++ {
					for attempt := 0; ; attempt++ {
						t0 := time.Now()
						s := visit(c, target+code(rng), addr)
						ms := float64(time.Since(t0).Microseconds()) / 1000
						mine = append(mine, ms)
						if (s == "503" || s == "error") && attempt < shedRetries {
							if s == "503" {
								shed.Add(1)
							} else {
								errs.Add(1)
							}
							time.Sleep(shedPause)
							continue
						}
						per[n] = append(per[n], answer{s, ms})
						break
					}
				}
			}
		}(uint64(w) + 1)
	}
	wg.Wait()
	res := &result{Status: map[string]int{}, Visitors: clients}
	res.Status["503"] = int(shed.Load())
	res.Status["error"] = int(errs.Load())
	res.Sent = int(shed.Load() + errs.Load())
	for n, as := range per {
		exact := len(as) == tries
		for i, a := range as {
			res.Sent++
			res.Status[a.status]++
			want := "404"
			if i >= missBurst {
				want = "429"
			}
			if a.status != want {
				exact = false
			}
		}
		if exact {
			res.Exact++
		} else if len(res.Problems) < 10 {
			var got []string
			for _, a := range as {
				got = append(got, a.status)
			}
			res.Problems = append(res.Problems, fmt.Sprintf("visitor %s: %s", ip(0, n), strings.Join(got, " ")))
		}
	}
	for k, v := range res.Status {
		if v == 0 {
			delete(res.Status, k)
		}
	}
	res.finish(lat)
	return res
}

// shedRetries bounds how often an H3 visitor sends a guess answered 503
// again, and shedPause is the pause before it does.
const (
	shedRetries = 200
	shedPause   = 20 * time.Millisecond
)

// H4: a flood of guesses. rate visits a second for duration, on a fixed
// schedule (a late answer does not slow the sender: latency is measured from
// when the visit was due, so a queue shows), each a random code from one of
// pool visitors: every visitor's first missBurst guesses are 404s (the app is
// asked), the rest 429s (it is not). A 503 is a visit shed at the door.
func runH4(target string, rate int, duration time.Duration, pool, offset, workers int) *result {
	c := newClient(workers)
	total := int(float64(rate) * duration.Seconds())
	due := make(chan time.Time, workers*4)
	type tally struct {
		status map[string]int
		lat    []float64
	}
	tallies := make([]tally, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(w)+1, 4))
			t := tally{status: map[string]int{}, lat: make([]float64, 0, total/workers+16)}
			for at := range due {
				s := visit(c, target+code(rng), ip(1, offset+rng.IntN(pool)))
				t.status[s]++
				t.lat = append(t.lat, float64(time.Since(at).Microseconds())/1000)
			}
			tallies[w] = t
		}(w)
	}
	interval := time.Second / time.Duration(rate)
	start := time.Now()
	for i := 0; i < total; i++ {
		at := start.Add(time.Duration(i) * interval)
		if d := time.Until(at); d > 0 {
			time.Sleep(d)
		}
		due <- at
	}
	close(due)
	wg.Wait()
	res := &result{Status: map[string]int{}}
	var lat []float64
	for _, t := range tallies {
		for k, v := range t.status {
			res.Status[k] += v
			res.Sent += v
		}
		lat = append(lat, t.lat...)
	}
	res.finish(lat)
	if n := res.Status["error"]; n > 0 {
		res.Problems = append(res.Problems, fmt.Sprintf("%d visits got no answer", n))
	}
	for k, v := range res.Status {
		if k != "404" && k != "429" && k != "503" && k != "error" {
			res.Problems = append(res.Problems, fmt.Sprintf("%d answers %s", v, k))
		}
	}
	return res
}

func (r *result) finish(lat []float64) {
	sort.Float64s(lat)
	pick := func(q float64) float64 {
		if len(lat) == 0 {
			return 0
		}
		i := int(q * float64(len(lat)-1))
		return lat[i]
	}
	r.P50, r.P90, r.P99, r.P999 = pick(0.50), pick(0.90), pick(0.99), pick(0.999)
	if len(lat) > 0 {
		r.Max = lat[len(lat)-1]
	}
	r.Latency = map[string]float64{"p50": r.P50, "p90": r.P90, "p99": r.P99, "p99.9": r.P999, "max": r.Max}
	if r.Sent > 0 {
		r.Shed = 100 * float64(r.Status["503"]) / float64(r.Sent)
	}
}

// cpuSeconds is the user and system time of process pid, from
// /proc/<pid>/stat; -1 when it cannot be read (no pid, not Linux).
func cpuSeconds(pid int) float64 {
	if pid <= 0 || runtime.GOOS != "linux" {
		return -1
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return -1
	}
	// The fields after the command's closing parenthesis; utime and stime
	// are the 14th and 15th of the line (11th and 12th after it).
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return -1
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 13 {
		return -1
	}
	ut, _ := strconv.ParseFloat(f[11], 64)
	st, _ := strconv.ParseFloat(f[12], 64)
	return (ut + st) / 100 // USER_HZ, 100 on every Linux filex runs on
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func firstBytes(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	return string(b)
}
