# Load test of an app's public route (H3, H4)

What a flood does to the **route door** - the part of filex that answers an
app's public addresses (`internal/api/handlers/route_door.go`, docs:
[APP-PLUGINS.md → Short links](../../../../docs/APP-PLUGINS.md#short-links)) -
measured against the link shortener example
([`backend/examples/app-shortlinks`](../../../examples/app-shortlinks/main.go)).

The first flood measurement (0.60, task #313) ran from a directory on the
build host that was deleted afterwards; its scenarios were known only by
their results. This directory is their home now: the runner (`main.go`), the
script that does a whole run (`run.sh`), and below what each scenario is, how
to run it and what it measured.

## The scenarios

Visitors are told apart by `X-Real-IP`, which filex believes from a loopback
peer only, so the runner runs on the machine filex runs on. Each scenario has
addresses of its own in 198.18.0.0/15 (the benchmarking range). Every visit
is a GET of `/l/<8 random letters and digits>` - a guess - with a browser's
headers (Firefox's user agent, `Accept-Language: en`,
`Accept-Encoding: gzip, deflate, br, zstd`).

| | What it holds | How |
|---|---|---|
| **H3** | The lockout holds under load: every guesser gets exactly 20 guesses judged by the app (404), then "too many tries" (429), however busy the server is. | 1,000 visitors, 64 at a time, each making 30 guesses one after the other. A guess answered 503 (every warm instance busy) was not judged: the visitor sends it again after 20 ms, as a guesser would, and the 503 is counted apart. Red when any visitor's answers are not 20 × 404 then 10 × 429. |
| **H4** | A flood of guesses: what the door answers, how fast, and how much it sheds. | 10,000 visits a second for 30 seconds on a fixed schedule (a late answer does not slow the sender; latency is counted from when the visit was due, so a queue shows), each a random guess from one of 500 visitors. Each visitor's first 20 guesses reach the app (404), the rest are answered 429 without it: about 10,000 404s, then 429s - the answers a flood is made of. A 503 is a visit shed (the door's 256 in flight, or every warm instance busy). |
| **H4-edited** | H4 with the door's pages replaced by an administrator's templates (`page.error`, `page.route`), which filex renders in the wasm sandbox - its dearest render. | `loadtest edit` saves both install-wide, then H4 again from 500 visitors of its own. A build before #324 does not draw the door's pages from templates: there H4-edited is H4 again. |

Pool: `FILEX_APP_ROUTE_POOL=4` (the shortener on four warm instances).

## Running it

`run.sh` boots a filex binary on a fresh data directory (removed afterwards),
installs and sets up the shortener under `/l`, runs H3, H4 and H4-edited, and
writes `run.txt` and one JSON file per scenario to the output directory:

```sh
# from backend/, for the machine that runs it (here linux/amd64)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o filex ./cmd/filex
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o loadtest ./internal/shortlink/loadtest
GOOS=wasip1 GOARCH=wasm go build -ldflags="-s -w" -buildmode=c-shared -o shortlinks.wasm ./examples/app-shortlinks

bash internal/shortlink/loadtest/run.sh ./filex ./shortlinks.wasm \
  examples/app-shortlinks/filex-app.json ./loadtest /tmp/route-load/this-build
```

`cmd/filex` embeds the web build: on a checkout without one, create
`embed/admin/.keep` and `embed/web/.keep` first (the door serves none of it).

Compare two builds **on the same machine, one after the other, with the
machine otherwise idle** - the build you change and the main it starts from -
and run each pair twice: H4 is sensitive to the CPU the warm instances get
(a 503 does not count as a guess, so a visitor whose guesses are shed keeps
guessing at the app). Numbers from a busy workstation are not comparable.
`LOADTEST_H3_ARGS` and `LOADTEST_H4_ARGS` add flags to a scenario (a shorter
`-duration` while trying the script out); `loadtest run -h` lists them.

## Results

Latencies in milliseconds, from when the visit was due. H3 is the share of
visitors that got exactly 20 × 404 then 429s, and its p99; H4 and H4-edited:
the share shed (503), p50 / p99 / max, and filex's CPU in cores. Every H4 run
sent 9,987-9,992 visits a second; about 10,000 were 404s, the rest 429s
and the shed.

**2026-11-12, task #324** (the door's pages as templates), on the build host
(20 CPUs, idle before the run), `FILEX_APP_ROUTE_POOL=4`, main (53e088135)
and the branch one after the other, three rounds:

| Round | Build | H3 | H4 | H4-edited |
|---|---|---|---|---|
| 1 | main | 1000/1000, p99 3.3 | 3.35 % · 0.32 / 3.70 / 12.6 · 2.15 | 4.62 % · 0.31 / 4.13 / 28.1 · 2.10 |
| 1 | branch | 1000/1000, p99 5.6 | 2.18 % · 0.32 / 3.02 / 8.5 · 2.04 | 1.59 % · 0.32 / 2.88 / 37.2 · 2.00 |
| 2 | main | 1000/1000, p99 5.9 | 2.88 % · 0.32 / 3.26 / 12.0 · 2.09 | 3.48 % · 0.31 / 3.71 / 28.0 · 2.03 |
| 2 | branch | 1000/1000, p99 5.0 | 3.49 % · 0.32 / 315 / 588 · 2.11 | 1.01 % · 0.32 / 541 / 856 · 1.95 |
| 3 | branch | 1000/1000, p99 5.1 | 1.57 % · 0.32 / 2.72 / 26.1 · 1.94 | 3.73 % · 0.31 / 3.79 / 75.3 · 2.04 |
| 3 | main | 1000/1000, p99 5.3 | 3.12 % · 0.32 / 3.60 / 11.6 · 2.07 | 3.72 % · 0.30 / 3.96 / 37.7 · 1.94 |

On main, H4-edited is H4 again (main draws the door's pages without
templates; its `page.route` does not exist). Round 2's branch tail is one
pause of 0.5-0.6 s about 29 s into each of its two H4 runs, in which filex
logged no visit and had none in flight (no visit took over 50 ms in the
handler); it did not come back in round 3, and the other runs of either
build had no pause over 13 ms. Before the pages were compressed, a CPU
profile of the branch under H4 on a busy workstation had found writing the
5.6 KB page 15 % of filex's CPU samples (main's 1.2 KB page: under 1 %);
compressed to 2.1 KB, 2 %.

**2026-11-11, task #313** (the 0.60 short-link branch), on the build host,
idle, with the lost runner: H3 every client exactly 20 × 404; H4 10,000/s,
p99 1.26 ms, 0.35 % shed. Its H4's mix of visitors is not known, so these
numbers are not comparable with the ones above.
