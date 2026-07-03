package server

import (
	"fmt"
	"io"
	"strings"

	"github.com/fatih/color"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/version"
)

// PrintBanner writes the first-run banner to w (typically os.Stdout).
//
// fr is the FirstRun result — when non-empty it adds the "first run
// detected" credentials section.
func PrintBanner(w io.Writer, cfg config.Config, fr FirstRunCredentials, caps map[string]model.ExternalServiceState, storages []*model.Storage) {
	bold := color.New(color.Bold)
	cyan := color.New(color.FgCyan, color.Bold)
	yellow := color.New(color.FgYellow)
	green := color.New(color.FgGreen)
	red := color.New(color.FgRed)
	dim := color.New(color.Faint)

	bar := "═══════════════════════════════════════════════════════════════"

	fmt.Fprintln(w)
	cyan.Fprintln(w, bar)
	bold.Fprintf(w, "  filex %s · self-hosted file manager\n", version.Version)
	cyan.Fprintln(w, bar)

	fmt.Fprintf(w, "  %s    %s\n", bold.Sprint("Listening on:"), cfg.PublicURL)
	fmt.Fprintf(w, "  %s        %s/admin\n", bold.Sprint("Admin UI:"), cfg.PublicURL)
	fmt.Fprintf(w, "  %s        %s/embed.js\n", bold.Sprint("Embed JS:"), cfg.PublicURL)

	if fr.AdminEmail != "" {
		fmt.Fprintln(w)
		dim.Fprintln(w, "  ─── First run detected ─────────────────────────────────────")
		fmt.Fprintln(w, "  Admin user created:")
		fmt.Fprintf(w, "    Email:     %s\n", green.Sprint(fr.AdminEmail))
		fmt.Fprintf(w, "    Password:  %s\n", yellow.Sprint(fr.AdminPassword))
		fmt.Fprintf(w, "  Saved to:  %s (mode 0600, shown ONCE)\n", fr.WroteFile)
		fmt.Fprintln(w, "  Change at: /admin/profile")
	}

	fmt.Fprintln(w)
	dim.Fprintln(w, "  ─── Embed in your app ──────────────────────────────────────")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Vue 3:")
	fmt.Fprintln(w, "    pnpm add @brftech/filex-core")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "    <script setup>")
	fmt.Fprintln(w, "      import { FileExplorer } from '@brftech/filex-core';")
	fmt.Fprintln(w, "    </script>")
	fmt.Fprintln(w, "    <template>")
	fmt.Fprintln(w, "      <FileExplorer :config=\"{")
	fmt.Fprintf(w, "        apiBase: '%s',\n", cfg.PublicURL)
	fmt.Fprintln(w, "        auth:    { kind: 'bearer', token: yourToken }")
	fmt.Fprintln(w, "      }\"/>")
	fmt.Fprintln(w, "    </template>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  React:")
	fmt.Fprintln(w, "    pnpm add @brftech/filex-react")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "    import { FileManager } from '@brftech/filex-react';")
	fmt.Fprintf(w, "    <FileManager config={{ apiBase: '%s' }}/>\n", cfg.PublicURL)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Vanilla JS / any framework:")
	fmt.Fprintf(w, "    <script src=\"%s/embed.js\"></script>\n", cfg.PublicURL)
	fmt.Fprintf(w, "    <filex-explorer api-base=\"%s\"></filex-explorer>\n", cfg.PublicURL)

	fmt.Fprintln(w)
	dim.Fprintln(w, "  ─── Drivers loaded ─────────────────────────────────────────")
	fmt.Fprintf(w, "  Auth:    %s\n", strings.Join(auth.Names(), ", "))
	fmt.Fprintf(w, "  Storage: %s\n", strings.Join(storage.Names(), ", "))
	fmt.Fprintf(w, "  DB:      %s\n", strings.Join(db.Names(), ", "))

	if len(caps) > 0 {
		fmt.Fprintln(w, "  External:")
		for name, state := range caps {
			mark := green.Sprint("✓")
			detail := state.URL
			if state.State != "ok" {
				mark = red.Sprint("✗")
				detail = "(" + state.State + ")"
			}
			fmt.Fprintf(w, "            %s %s %s\n", mark, name, detail)
		}
	}

	if len(storages) == 0 {
		fmt.Fprintln(w)
		yellow.Fprintln(w, "  No storage configured. Add one at /admin/storages to start.")
	} else {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  %d storage(s) configured.\n", len(storages))
	}

	fmt.Fprintln(w)
	cyan.Fprintln(w, bar)
	fmt.Fprintln(w)
}
