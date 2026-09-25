// echo is the fixture plugin the wasmplugin tests run. It is built by
// scripts/build-wasm-fixture.sh (CI) into echo.wasm beside this file; the
// tests skip when the file is absent, except on CI where they fail.
//
// It exercises exactly what the runtime promises: describe echoes the
// manifest; `upper` is an action; `hello` is a view; `slow` never returns
// (timeout); `hungry` grows memory until the ceiling (OOM); `boom` sets an
// error; `crash` traps.
//
// ⚠ The `schedule` permission and the settings the tick reads are NOT in the
// manifest here. They are added by the scheduling tests, which install this
// same module with a widened manifest — describe only has to be a SUBSET of
// what was installed. It has to be this way round: a manifest that dropped a
// permission describe names is refused, so there would be no way left to
// install this module as an app that was never granted a wake-up, and
// "an app that did not ask is never woken" is exactly what has to be proven.
package main

import (
	"crypto"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// Manifest must equal manifest.json next to this file.
var manifest = wire.Manifest{
	ManifestVersion: 1,
	Name:            "echo",
	Version:         "0.0.1",
	// ⚠ "Echo Fixture", not "Echo": the label has to be visibly MORE than the
	// name capitalised, or a screen that fell back to the name `echo` would
	// still read "Echo …" wherever a sentence capitalises its first word, and
	// the tests that prove the label is the thing drawn (e2e 95, the lock
	// badge) would pass either way. Not "Echo app" either — the mail footer is
	// "Sent by the {label} app on filex", which would then stutter.
	Label:       wire.Text{"en": "Echo Fixture", "tr": "Yankı"},
	Languages:   []string{"en", "tr"},
	Permissions: []string{"files:read", "files:write", "files:lock", "state", "engines:ffmpeg", "users:lookup", "notify:send", "mail:send", "http:example.test", "public_pages", "sign"},
	Actions: []wire.Action{{
		ID: "upper", Label: wire.Text{"en": "Upper-case", "tr": "Büyük harf"},
		Applies: wire.Applies{Kind: "file", Ext: []string{"txt"}},
		Output:  wire.Output{Mode: "sibling", Name: "{stem}-upper{ext}"},
	}, {
		ID: "engine", Label: wire.Text{"en": "Engine probe", "tr": "Engine probe"},
		Applies: wire.Applies{Kind: "any"}, Output: wire.Output{Mode: "none"},
	}, {
		ID: "escape", Label: wire.Text{"en": "Engine escape probe", "tr": "Engine escape probe"},
		Applies: wire.Applies{Kind: "any"}, Output: wire.Output{Mode: "none"},
	}, {
		ID: "outbound", Label: wire.Text{"en": "Outbound probe", "tr": "Outbound probe"},
		Applies: wire.Applies{Kind: "any"}, Output: wire.Output{Mode: "none"},
	}, {
		ID: "invite", Label: wire.Text{"en": "Invite an outside signer", "tr": "Invite an outside signer"},
		Applies: wire.Applies{Kind: "file", Ext: []string{"txt"}}, Output: wire.Output{Mode: "none"},
	}, {
		ID: "sign", Label: wire.Text{"en": "Sign probe", "tr": "Sign probe"},
		Applies: wire.Applies{Kind: "file", Ext: []string{"txt"}}, Output: wire.Output{Mode: "none"},
	}, {
		ID: "deliver", Label: wire.Text{"en": "Deliver the signed file", "tr": "İmzalı dosyayı ilet"},
		Applies: wire.Applies{Kind: "file", Ext: []string{"txt"}},
		Output:  wire.Output{Mode: "sibling", Name: "{stem}-signed{ext}"},
	}, {
		ID: "owned", Label: wire.Text{"en": "Owner-only probe", "tr": "Yalnız sahip denemesi"},
		Applies: wire.Applies{Kind: "any"}, Output: wire.Output{Mode: "none"}, MinRole: "owner",
	}, {
		ID: "listing", Label: wire.Text{"en": "State listing probe", "tr": "Durum listesi denemesi"},
		Applies: wire.Applies{Kind: "any"}, Output: wire.Output{Mode: "none"},
	}, {
		// asset_fetch: download a pinned file once, then read it back.
		ID: "asset", Label: wire.Text{"en": "Asset probe", "tr": "Varlık denemesi"},
		Applies: wire.Applies{Kind: "any"}, Output: wire.Output{Mode: "none"},
	}, {
		// What a job is told about its file: whether the storage takes
		// writes (wire.FileRef.ReadOnly).
		ID: "facts", Label: wire.Text{"en": "File facts probe", "tr": "Dosya bilgisi denemesi"},
		Applies: wire.Applies{Kind: "any"}, Output: wire.Output{Mode: "none"},
	}, {
		ID: "lock", Label: wire.Text{"en": "Lock probe", "tr": "Lock probe"}, View: "wizard",
		Applies: wire.Applies{Kind: "any"}, Output: wire.Output{Mode: "none"},
	}, {
		ID: "unlock", Label: wire.Text{"en": "Unlock probe", "tr": "Unlock probe"},
		Applies: wire.Applies{Kind: "any"}, Output: wire.Output{Mode: "none"},
	}, {
		ID: "again", Label: wire.Text{"en": "Again (needs state)", "tr": "Again (needs state)"},
		Applies: wire.Applies{Kind: "file", Ext: []string{"txt"}, State: []string{"runs"}}, Output: wire.Output{Mode: "none"},
	}, {
		ID: "fresh", Label: wire.Text{"en": "Fresh (no state)", "tr": "Fresh (no state)"},
		Applies: wire.Applies{Kind: "file", Ext: []string{"txt"}, NoState: []string{"runs"}}, Output: wire.Output{Mode: "none"},
	}, {
		ID: "expire", Label: wire.Text{"en": "Expire (scheduled)", "tr": "Süresi doldu (zamanlanmış)"},
		Applies: wire.Applies{Kind: "any"}, Output: wire.Output{Mode: "none"}, Hidden: true,
	}, {
		ID: "applied", Label: wire.Text{"en": "Hidden apply", "tr": "Hidden apply"},
		Applies: wire.Applies{Kind: "any"}, Output: wire.Output{Mode: "none"}, Hidden: true,
	}, {
		// gather is a MODAL opened on a selection (`picks`) whose submit
		// queues the job on every file of it — the shape filex #64 broke:
		// the first screen saw the whole selection, every later event only
		// the first file.
		ID: "gather", Label: wire.Text{"en": "Gather", "tr": "Topla"}, View: "picks",
		Applies: wire.Applies{Kind: "file", Ext: []string{"txt"}, Multi: true},
		Output:  wire.Output{Mode: "sibling", Name: "{stem}-gathered{ext}"},
	}},
	PublicPages: []wire.PublicPage{{ID: "signer", Label: wire.Text{"en": "Sign the document", "tr": "Belgeyi imzala"}, PIN: "optional", DefaultTTLDays: 7, MaxTTLDays: 30}},
	Views: []wire.View{{ID: "hello", Placement: "modal", Label: wire.Text{"en": "Hello", "tr": "Hello"}}, {ID: "wizard", Placement: "page", Label: wire.Text{"en": "Wizard", "tr": "Wizard"}},
		{ID: "picks", Placement: "modal", Label: wire.Text{"en": "Picks", "tr": "Seçilenler"}}},
	// held: the lock reason `lock` names when asked (params.msg), which filex
	// says in each reader's language.
	Messages: map[string]wire.Text{"held": {"en": "held for {who}", "tr": "{who} için tutuluyor"}},
}

var sink [][]byte // hungry: keeps allocations alive

// ⚠ Registration happens in init(), not main(): a wasip1 module built with
// -buildmode=c-shared is a *reactor* — filex calls its exports directly and
// main() never runs. pluginkit.Run documents the same.
func main() {}

func init() {
	pluginkit.Run(&pluginkit.Plugin{
		Manifest: manifest,
		Actions: map[string]pluginkit.ActionFunc{
			"upper": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				pluginkit.Log("info", "upper: "+in.JobID)
				// No inputs (the runtime unit tests): answer without touching
				// the host. With inputs: read each, upper-case it, write a
				// sibling, remember a per-file counter, report progress.
				if len(in.Inputs) == 0 {
					return &wire.ActionRunOutput{OK: true, Message: wire.Text{"en": "ok " + strings.ToUpper(in.JobID)}}, nil
				}
				var outs []wire.OutputRef
				for i, f := range in.Inputs {
					data, err := pluginkit.ReadInput(f.Ref)
					if err != nil {
						return nil, err
					}
					if v, found, _ := pluginkit.StateGet(f.Ref, "runs"); found {
						_ = pluginkit.StateSet(f.Ref, "runs", v+"+1")
					} else {
						_ = pluginkit.StateSet(f.Ref, "runs", "1")
					}
					ref, err := pluginkit.WriteOutput(strings.TrimSuffix(f.Name, ".txt")+"-upper.txt", []byte(strings.ToUpper(string(data))))
					if err != nil {
						return nil, err
					}
					outs = append(outs, ref)
					pluginkit.Progress(int64(i+1), int64(len(in.Inputs)), "upper-cased "+f.Name)
				}
				if _, err := pluginkit.OpenInput("in:99"); err == nil || !strings.Contains(err.Error(), "not_found") {
					return nil, errors.New("expected not_found for a ref outside the scope")
				}
				return &wire.ActionRunOutput{OK: true, Outputs: outs, Message: wire.Text{"en": "done", "tr": "bitti"}}, nil
			},
			// gather writes one sibling per input carrying the note the
			// `picks` screen collected, so a test can count the files the
			// job really ran on and see that the form's answer arrived.
			"gather": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				note, _ := in.Params["note"].(string)
				var outs []wire.OutputRef
				for i, f := range in.Inputs {
					data, err := pluginkit.ReadInput(f.Ref)
					if err != nil {
						return nil, err
					}
					ref, err := pluginkit.WriteOutput(strings.TrimSuffix(f.Name, ".txt")+"-gathered.txt", []byte(note+":"+string(data)))
					if err != nil {
						return nil, err
					}
					outs = append(outs, ref)
					pluginkit.Progress(int64(i+1), int64(len(in.Inputs)), "gathered "+f.Name)
				}
				n := strconv.Itoa(len(outs))
				return &wire.ActionRunOutput{OK: true, Outputs: outs, Message: wire.Text{"en": "gathered " + n, "tr": n + " dosya toplandı"}}, nil
			},
			"engine": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				if !pluginkit.EngineAvailable("ffmpeg") {
					return &wire.ActionRunOutput{OK: false, Message: wire.Text{"en": "ffmpeg unavailable", "tr": "ffmpeg unavailable"}}, nil
				}
				res, err := pluginkit.EngineRun(pluginkit.EngineRequest{Engine: "ffmpeg", Args: []string{"-version"}})
				if err != nil {
					return nil, err
				}
				return &wire.ActionRunOutput{OK: res.Exit == 0, Message: wire.Text{"en": res.StdoutTail}}, nil
			},
			"outbound": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				var parts []string
				users, err := pluginkit.UsersLookup("a")
				if err != nil {
					parts = append(parts, "users:"+err.Error())
				} else {
					parts = append(parts, "users:"+strconv.Itoa(len(users)))
				}
				if id, err := pluginkit.NotifySend(pluginkit.Notice{Title: wire.Text{"en": "Hello from echo", "tr": "Yankı selam eder"}, Body: wire.Text{"en": "body", "tr": "body"}, Severity: "info", Meta: map[string]any{"k": "v"}}); err != nil {
					parts = append(parts, "notify:"+err.Error())
				} else {
					parts = append(parts, "notify:"+strconv.FormatInt(id, 10))
				}
				if err := pluginkit.MailSend("ada@example.test", "Subject", "Body"); err != nil {
					parts = append(parts, "mail:"+err.Error())
				} else {
					parts = append(parts, "mail:ok")
				}
				if resp, err := pluginkit.HTTPDo(pluginkit.HTTPRequest{URL: "https://example.test/hello", Headers: map[string]string{"X-Probe": "1"}}); err != nil {
					parts = append(parts, "http:"+err.Error())
				} else {
					parts = append(parts, "http:"+strconv.Itoa(resp.Status)+":"+string(resp.Body))
				}
				if _, err := pluginkit.HTTPDo(pluginkit.HTTPRequest{URL: "https://other.test/"}); err != nil {
					parts = append(parts, "other:"+err.Error())
				} else {
					parts = append(parts, "other:allowed")
				}
				return &wire.ActionRunOutput{OK: true, Message: wire.Text{"en": strings.Join(parts, "|")}}, nil
			},
			"sign": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				info, err := pluginkit.HostSignInfo()
				if err != nil {
					return nil, err
				}
				if !info.Available {
					return &wire.ActionRunOutput{OK: false, Message: wire.Text{"en": "unavailable: " + info.Reason}}, nil
				}
				data, err := pluginkit.ReadInput(in.Inputs[0].Ref)
				if err != nil {
					return nil, err
				}
				name := in.Actor.Name
				if name == "" {
					name = in.Actor.Email
				}
				if name == "" {
					name = "Signer"
				}
				issued, err := pluginkit.CertIssue(name, in.Actor.Email, 30)
				if err != nil {
					return nil, err
				}
				signer, err := pluginkit.NewHostSigner(issued)
				if err != nil {
					return nil, err
				}
				sum := sha256.Sum256(data)
				sig, err := signer.Sign(nil, sum[:], crypto.SHA256)
				if err != nil {
					return nil, err
				}
				if err := pluginkit.KeyDestroy(issued.KeyRef); err != nil {
					return nil, err
				}
				if _, err := pluginkit.HostSign(issued.KeyRef, sum[:]); err == nil {
					return nil, errors.New("a destroyed key still signs")
				}
				out, _ := json.Marshal(map[string]string{"cert": issued.CertPEM, "chain": issued.ChainPEM, "ca": info.CACertPEM, "sig": base64.StdEncoding.EncodeToString(sig)})
				return &wire.ActionRunOutput{OK: true, Message: wire.Text{"en": string(out)}}, nil
			},
			// owned exists for its MANIFEST line, not its body: `min_role:
			// owner` is the one floor an app author can put under an action,
			// and the host has to raise the ACL level it demands to match it
			// (handlers.pluginACLNeed). Nothing in the repo exercised that
			// floor until the public door started consulting it, so the
			// fixture grew the action rather than the assertion growing a
			// mock.
			"owned": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				return &wire.ActionRunOutput{OK: true, Message: wire.Text{"en": "owned " + in.JobID}}, nil
			},
			"invite": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				pin := "auto"
				if v, ok := in.Params["pin"].(string); ok {
					pin = v
					if v == "-" {
						pin = ""
					}
				}
				page, err := pluginkit.PublicPageCreate(pluginkit.PageCreate{
					PageID: "signer", Subject: "Please sign " + in.Inputs[0].Name, PIN: pin, TTLDays: 3,
					State: map[string]any{"step": "sent"}, Files: []wire.OutputRef{{Ref: in.Inputs[0].Ref, Name: in.Inputs[0].Name}},
				})
				if err != nil {
					return nil, err
				}
				_ = pluginkit.StateSet(in.Inputs[0].Ref, "envelope", page.Token)
				return &wire.ActionRunOutput{OK: true, Message: wire.Text{"en": page.URL + "|" + page.PIN}}, nil
			},
			// deliver is the signing app's last step in miniature: make a new
			// file out of the input, open ONE ordinary share of THAT file, and
			// report the link — all inside a single action_run, before the host
			// has committed anything. Everything about it is steered by params
			// so the host tests can drive each branch:
			//
			//   pin        "auto" (default) | "<digits>" | "-" for none
			//   ref        share this ref instead of naming the output in files
			//   page       render a manifest page behind the link
			//   ttl_days / max_visits
			//   fail       promise the link, then fail the job
			//   drop       promise the link, then keep no outputs
			"deliver": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				data, err := pluginkit.ReadInput(in.Inputs[0].Ref)
				if err != nil {
					return nil, err
				}
				name := strings.TrimSuffix(in.Inputs[0].Name, ".txt") + "-signed.txt"
				out, err := pluginkit.WriteOutput(name, []byte("SIGNED "+string(data)))
				if err != nil {
					return nil, err
				}
				pin := "auto"
				if v, ok := in.Params["pin"].(string); ok {
					pin = v
					if v == "-" {
						pin = ""
					}
				}
				// The signing app's own shape, exactly: ONE copy and no page.
				// A link without a page hands over no copies, so the host reads
				// that copy as the only thing it can mean — this is the
				// document — and the link is of the file being written.
				req := pluginkit.PageCreate{
					Subject: in.Inputs[0].Name, PIN: pin, TTLDays: 5,
					State: map[string]any{"kind": "delivery"},
					Files: []wire.OutputRef{{Ref: out.Ref, Name: name}},
				}
				if v, ok := in.Params["page"].(string); ok && v != "" {
					// With a page there IS a surface, so the copy stays a copy
					// and `ref` says what the link is about.
					req.PageID, req.Ref = v, out.Ref
				}
				if v, ok := in.Params["ref"].(string); ok && v != "" {
					req.Ref, req.Files = v, nil
				}
				if v, ok := in.Params["ttl_days"].(float64); ok {
					req.TTLDays = int(v)
				}
				if v, ok := in.Params["max_visits"].(float64); ok {
					req.MaxVisits = int(v)
				}
				share, err := pluginkit.ShareCreate(req)
				if err != nil {
					// The host's own words, handed straight to the job row: a
					// plugin that asks for an impossible link must be able to
					// say WHY, not "something went wrong".
					return &wire.ActionRunOutput{OK: false, Message: wire.Text{"en": "share_create: " + err.Error()}}, nil
				}
				if v, ok := in.Params["fail"].(bool); ok && v {
					return &wire.ActionRunOutput{OK: false, Message: wire.Text{"en": "signing failed after " + share.URL}}, nil
				}
				answer := &wire.ActionRunOutput{OK: true, Outputs: []wire.OutputRef{out},
					Message: wire.Text{"en": share.URL + "|" + share.PIN}}
				if v, ok := in.Params["drop"].(bool); ok && v {
					answer.Outputs = nil
				}
				return answer, nil
			},
			"listing": func(*wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				items, err := pluginkit.StateList("runs", 50)
				if err != nil {
					return nil, err
				}
				parts := make([]string, 0, len(items))
				for _, it := range items {
					parts = append(parts, it.Path+"="+it.Value)
				}
				return &wire.ActionRunOutput{OK: true, Message: wire.Text{"en": strings.Join(parts, ","), "tr": strings.Join(parts, ",")}}, nil
			},
			"asset": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				str := func(k string) string { v, _ := in.Params[k].(string); return v }
				max, _ := in.Params["max_bytes"].(float64)
				a, err := pluginkit.AssetFetch(str("url"), str("sha256"), int64(max))
				if err != nil {
					code := "error"
					if he, ok := err.(*pluginkit.HostError); ok {
						code = he.Code
					}
					return &wire.ActionRunOutput{OK: true, Message: wire.Text{"en": "refused=" + code, "tr": "refused=" + code}}, nil
				}
				b, err := pluginkit.ReadInput(a.Ref)
				if err != nil {
					return nil, err
				}
				msg := fmt.Sprintf("read=%d size=%d cached=%v body=%s", len(b), a.Size, a.Cached, string(b))
				return &wire.ActionRunOutput{OK: true, Message: wire.Text{"en": msg, "tr": msg}}, nil
			},
			"facts": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				ro := "none"
				if len(in.Inputs) > 0 {
					ro = "false"
					if in.Inputs[0].ReadOnly {
						ro = "true"
					}
				}
				return &wire.ActionRunOutput{OK: true, Message: wire.Text{"en": "ro=" + ro, "tr": "ro=" + ro}}, nil
			},
			"lock": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				// Lock the first input for a day and tell one person, with a
				// deep link to the sign action on that file.
				var until time.Time
				var err error
				if who, _ := in.Params["msg"].(string); who != "" {
					until, err = pluginkit.FileLockMessage(in.Inputs[0].Ref, 1, "held", map[string]string{"who": who})
				} else {
					until, err = pluginkit.FileLock(in.Inputs[0].Ref, 1, "under signature")
				}
				if err != nil {
					return nil, err
				}
				parts := []string{"locked:" + until.UTC().Format("2006-01-02")}
				n := pluginkit.Notice{Title: wire.Text{"en": "Please sign", "tr": "Please sign"}, Body: wire.Text{"en": in.Inputs[0].Name},
					Target: &pluginkit.NoticeTarget{Ref: in.Inputs[0].Ref, Action: "sign"}}
				if v, ok := in.Params["to"].(float64); ok {
					n.ToUserID = int64(v)
				}
				if _, err := pluginkit.NotifySend(n); err != nil {
					parts = append(parts, "notify:"+err.Error())
				} else {
					parts = append(parts, "notify:ok")
				}
				return &wire.ActionRunOutput{OK: true, Message: wire.Text{"en": strings.Join(parts, "|")}}, nil
			},
			"unlock": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				if err := pluginkit.FileUnlock(in.Inputs[0].Ref); err != nil {
					return nil, err
				}
				return &wire.ActionRunOutput{OK: true, Message: wire.Text{"en": "unlocked", "tr": "unlocked"}}, nil
			},
			"again": func(*wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				return &wire.ActionRunOutput{OK: true, Message: wire.Text{"en": "again", "tr": "again"}}, nil
			},
			"fresh": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				// params.mine: leave a PERSONAL key for the person running it
				// (wire.PersonalState) — what a listing shows them as todo@me.
				if mine, _ := in.Params["mine"].(bool); mine && len(in.Inputs) > 0 {
					if err := pluginkit.StateSet(in.Inputs[0].Ref, wire.PersonalState("todo", in.Actor.ID), "1"); err != nil {
						return nil, err
					}
				}
				return &wire.ActionRunOutput{OK: true, Message: wire.Text{"en": "fresh", "tr": "fresh"}}, nil
			},
			// expire is what a wake-up schedules: a hidden action nobody
			// picks from a menu, run by the host at a minute the app chose.
			// It writes state to prove a scheduled job gets a WRITABLE
			// scope, unlike the tick that asked for it.
			"expire": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				name := "nothing"
				if len(in.Inputs) > 0 {
					name = in.Inputs[0].Name
					_ = pluginkit.StateSet(in.Inputs[0].Ref, "expired", "1")
				}
				who, _ := in.Params["envelope"].(string)
				return &wire.ActionRunOutput{OK: true, Message: wire.Text{
					"en": "expired " + name + "|" + who + "|actor=" + strconv.FormatInt(in.Actor.ID, 10),
					"tr": "expired " + name + "|" + who + "|actor=" + strconv.FormatInt(in.Actor.ID, 10)}}, nil
			},
			"applied": func(*wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				return &wire.ActionRunOutput{OK: true, Message: wire.Text{"en": "applied", "tr": "applied"}}, nil
			},
			"escape": func(*wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				_, err := pluginkit.EngineRun(pluginkit.EngineRequest{Engine: "ffmpeg", Args: []string{"-i", "../../etc/passwd"}})
				if err == nil {
					return nil, errors.New("expected the host to refuse a path argument")
				}
				return &wire.ActionRunOutput{OK: true, Message: wire.Text{"en": err.Error()}}, nil
			},
			"slow": func(*wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				for {
					time.Sleep(50 * time.Millisecond)
				}
			},
			"hungry": func(*wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				for {
					sink = append(sink, make([]byte, 4<<20))
				}
			},
			"boom": func(*wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				return nil, errors.New("boom: the plugin said no")
			},
			"crash": func(*wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				var p *wire.Surface
				_ = p.Nodes[0] // nil dereference → trap
				return nil, nil
			},
		},
		// Tick is the hourly wake-up. It is registered whether or not the
		// manifest asks for `schedule`; the HOST decides whether to call it,
		// and an app that was not granted the permission never is.
		//
		// What it schedules is driven by settings so one fixture can play
		// every case the host has to survive: nothing to do, work due inside
		// the window, work due beyond it, more items than the cap, a bad
		// key, a trap and a hang.
		Tick: func(in *wire.TickInput) (*wire.TickOutput, error) {
			mode := in.Settings["tick_mode"]
			switch mode {
			case "", "quiet":
				return &wire.TickOutput{Note: wire.Text{"en": "nothing to do", "tr": "yapacak iş yok"}}, nil
			case "crash":
				var s *wire.Surface
				_ = s.Nodes[0] // nil dereference → trap
				return nil, nil
			case "slow":
				for {
					time.Sleep(50 * time.Millisecond)
				}
			case "boom":
				return nil, errors.New("tick: the plugin said no")
			}
			offset := 0
			if v, err := strconv.Atoi(in.Settings["tick_due_in_s"]); err == nil {
				offset = v
			}
			due := in.Now.Add(time.Duration(offset) * time.Second)
			item := func(key, path string) wire.ScheduleItem {
				return wire.ScheduleItem{Key: key, DueAt: due, ActionID: "expire",
					Paths: []string{path}, Params: map[string]any{"envelope": key}}
			}
			switch mode {
			case "far":
				// Beyond the window on purpose: the host must not schedule
				// it, must not complain, and must ask again next hour.
				it := item("far", in.Settings["tick_path"])
				it.DueAt = in.WindowEnd.Add(time.Hour)
				return &wire.TickOutput{Items: []wire.ScheduleItem{it}}, nil
			case "badkey":
				it := item("no spaces allowed", in.Settings["tick_path"])
				return &wire.TickOutput{Items: []wire.ScheduleItem{it}}, nil
			case "badaction":
				it := item("ghost", in.Settings["tick_path"])
				it.ActionID = "no-such-action"
				return &wire.TickOutput{Items: []wire.ScheduleItem{it}}, nil
			case "nopaths":
				it := item("empty", "")
				it.Paths = nil
				return &wire.TickOutput{Items: []wire.ScheduleItem{it}}, nil
			case "flood":
				items := make([]wire.ScheduleItem, 0, in.MaxItems+5)
				for i := 0; i < in.MaxItems+5; i++ {
					items = append(items, item("flood-"+strconv.Itoa(i), in.Settings["tick_path"]))
				}
				return &wire.TickOutput{Items: items}, nil
			case "state":
				// The realistic shape: find the files this app is waiting
				// on, and schedule the closure of each.
				found, err := pluginkit.StateList("runs", 50)
				if err != nil {
					return nil, err
				}
				items := make([]wire.ScheduleItem, 0, len(found))
				for i, f := range found {
					items = append(items, item("state-"+strconv.Itoa(i), f.Path))
				}
				return &wire.TickOutput{Items: items, Note: wire.Text{"en": "from state", "tr": "durumdan"}}, nil
			case "write":
				// A tick may NOT write state: the host has to refuse it.
				err := pluginkit.StateSet("in:0", "ticked", "1")
				if err == nil {
					return nil, errors.New("expected the host to refuse a state write from a tick")
				}
				return &wire.TickOutput{Note: wire.Text{"en": "refused: " + err.Error(), "tr": "refused"}}, nil
			}
			return &wire.TickOutput{Items: []wire.ScheduleItem{item("due", in.Settings["tick_path"])}}, nil
		},
		Pages: map[string]pluginkit.PageFunc{
			"signer": func(in *wire.ViewEventInput) (*wire.Surface, error) {
				var st struct {
					Step string `json:"step"`
				}
				_ = pluginkit.PublicPageState("", &st)
				if in.Event == "submit" {
					_ = pluginkit.PublicPageStateSet("", map[string]any{"step": "signed"})
					_ = pluginkit.StateSet("in:0", "signed_by", "visitor")
					// ⭐ Two knobs the visitor's own `data` turns, and they
					// exist for one reason: the host's submit-time gate on THIS
					// door has to be measurable. A page is the only thing that
					// can ask for a job as the link's creator, so a test cannot
					// aim a job at a disabled / admin-only / missing action, or
					// at oversized parameters, unless the fixture page lets it
					// (public_page_job_guard_test.go).
					//
					// ⚠ Absent, the page behaves exactly as it always has --
					// a signer's submit that queues `upper` with one flag -- so
					// every older test reads the same answers as before.
					action := "upper"
					if v, _ := in.Data["run"].(string); v != "" {
						action = v
					}
					params := map[string]any{"from_page": true}
					if n, ok := in.Data["bulk"].(float64); ok && n > 0 {
						params["bulk"] = strings.Repeat("x", int(n))
					}
					return &wire.Surface{Job: &wire.JobRequest{ActionID: action, Params: params}}, nil
				}
				body, _ := pluginkit.ReadInput("pub:0")
				// The event is echoed because a visitor-facing surface has to
				// be re-askable WITHOUT starting over: switching the language
				// asks again with `change`, and a test can only tell the two
				// apart if the answer says which one it was.
				note, _ := in.Data["values"].(map[string]any)
				noteVal, _ := note["note"].(string)
				return &wire.Surface{
					Title: wire.Text{"en": "Sign", "tr": "Sign"},
					Nodes: []wire.Node{
						{Type: "text", Props: map[string]any{"text": map[string]string{
							"en": "step=" + st.Step + " doc=" + string(body) + " inputs=" + strconv.Itoa(len(in.Context.Inputs)),
							"tr": "adim=" + st.Step + " belge=" + string(body) + " girdi=" + strconv.Itoa(len(in.Context.Inputs))}}},
						{Type: "text", Props: map[string]any{"text": map[string]string{
							"en": "event=" + in.Event + " note=" + noteVal,
							"tr": "olay=" + in.Event + " not=" + noteVal}}},
						{Type: "form", Props: map[string]any{"fields": []map[string]any{{
							"key": "note", "type": "string",
							"label": map[string]string{"en": "Note", "tr": "Not"}}}}},
					},
					Actions: []wire.SurfaceAction{{ID: "submit", Label: wire.Text{"en": "Sign", "tr": "Sign"}, Primary: true}},
				}, nil
			},
		},
		Views: map[string]pluginkit.ViewFunc{
			"wizard": func(in *wire.ViewEventInput) (*wire.Surface, error) {
				// Echoes what a home page's frame and a signed-in person's
				// request hand a view (v3.1): the section asked for and the
				// actor's address.
				section, _ := in.Data["section"].(string)
				ip := ""
				if in.Context.Actor != nil {
					ip = in.Context.Actor.IP
				}
				// ...and whether the file's storage takes writes.
				ro := "none"
				if len(in.Context.Inputs) > 0 {
					ro = "false"
					if in.Context.Inputs[0].ReadOnly {
						ro = "true"
					}
				}
				return &wire.Surface{Title: wire.Text{"en": "Wizard", "tr": "Wizard"}, Section: section,
					Sections: []wire.Section{{ID: "a", Label: wire.Text{"en": "A", "tr": "A"}}, {ID: "b", Label: wire.Text{"en": "B", "tr": "B"}}},
					Nodes:    []wire.Node{{Type: "text", Props: map[string]any{"text": map[string]string{"en": "page " + in.Event + " section=" + section + " ip=" + ip + " ro=" + ro + " locale=" + in.Context.Locale}}}}}, nil
			},
			"picks": func(in *wire.ViewEventInput) (*wire.Surface, error) {
				// Every answer says how many files the host handed this event
				// and which: a client that falls back to one file after the
				// first event (filex #64) reads "n=1" on its second screen.
				paths := make([]string, 0, len(in.Context.Inputs))
				for _, f := range in.Context.Inputs {
					paths = append(paths, f.Path)
				}
				values, _ := in.Data["values"].(map[string]any)
				note, _ := values["note"].(string)
				if in.Event == "submit" {
					return &wire.Surface{Job: &wire.JobRequest{ActionID: "gather", Params: map[string]any{"note": note}}}, nil
				}
				summary := "picks " + in.Event + " n=" + strconv.Itoa(len(paths)) + ": " + strings.Join(paths, ", ")
				return &wire.Surface{
					Title: wire.Text{"en": "Picks", "tr": "Seçilenler"},
					Nodes: []wire.Node{
						{Type: "text", Props: map[string]any{"text": map[string]string{"en": summary, "tr": summary}}},
						{Type: "form", Props: map[string]any{"fields": []map[string]any{{
							"key": "note", "type": "string",
							"label": map[string]string{"en": "Note", "tr": "Not"}}}}},
					},
					Actions: []wire.SurfaceAction{{ID: "gather", Label: wire.Text{"en": "Gather", "tr": "Topla"}, Primary: true}},
				}, nil
			},
			"hello": func(in *wire.ViewEventInput) (*wire.Surface, error) {
				if in.Event == "submit" {
					// A submit naming an output mode queues `upper` with that
					// mode overriding the manifest (the wizard's "same file as
					// a new version" choice); a submit naming a hidden action
					// queues it (the second half of a flow).
					if mode, _ := in.Data["output_mode"].(string); mode != "" {
						// output_dir: the folder the person chose (mode folder).
						dir, _ := in.Data["output_dir"].(string)
						return &wire.Surface{Job: &wire.JobRequest{ActionID: "upper", Output: &wire.Output{Mode: mode, Name: "custom-{stem}{ext}", Dir: dir}}}, nil
					}
					if act, _ := in.Data["run"].(string); act != "" {
						return &wire.Surface{Job: &wire.JobRequest{ActionID: act}}, nil
					}
					return &wire.Surface{Done: true, Toast: wire.Text{"en": "submitted", "tr": "submitted"}}, nil
				}
				first := ""
				if len(in.Context.Inputs) > 0 {
					first = " " + in.Context.Inputs[0].Path
				}
				if in.Context.Home != "" {
					first += " home=" + in.Context.Home
				}
				return &wire.Surface{
					Title:   wire.Text{"en": "Hello", "tr": "Hello"},
					Nodes:   []wire.Node{{Type: "text", Props: map[string]any{"text": map[string]string{"en": "hi " + in.Event + first}}}},
					Actions: []wire.SurfaceAction{{ID: "submit", Label: wire.Text{"en": "Submit", "tr": "Submit"}, Primary: true}},
				}, nil
			},
		},
	})
}
