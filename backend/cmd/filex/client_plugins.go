// client_plugins.go — `filex client plugins request|requests`: leave a
// plugin install request and follow it (docs/APP-PLUGINS.md → Install
// requests). An API key cannot install a plugin; an administrator approves
// or rejects the request in the admin panel, and there is deliberately no
// command for that.
package main

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/brf-tech/filex/backend/internal/cliclient"
)

func clientPluginsCmd(opts *clientOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "plugins",
		Short: "Ask for a plugin to be installed, and follow the request",
		Long: "An API key cannot install, upgrade or remove a plugin: it leaves a request, and an administrator\n" +
			"signed in to the admin panel approves or rejects it (Plugins → Install requests).",
	}
	c.AddCommand(clientPluginRequestCmd(opts), clientPluginRequestsCmd(opts))
	return c
}

func clientPluginRequestCmd(opts *clientOpts) *cobra.Command {
	var in cliclient.PluginRequestInput
	var upgrade bool
	c := &cobra.Command{
		Use:   "request",
		Short: "Leave a request to install (or --upgrade) a plugin",
		Example: "  filex client plugins request --kind app --github-repo BRF-Tech/filex-sign --ref v0.1.1 --reason \"contracts\"\n" +
			"  filex client plugins request --kind app --manifest-url https://example.com/filex-app.json --reason \"…\"\n" +
			"  filex client plugins request --kind storage --name myfs --source owner/myfs --reason \"…\"\n" +
			"  filex client plugins request --kind app --upgrade --name sign --reason \"security fix\"",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(in.Reason) == "" {
				return errors.New("--reason is required: the administrator who approves it reads it")
			}
			if upgrade {
				in.Op = "upgrade"
			}
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			res, err := api.RequestPlugin(cmd.Context(), in)
			if err != nil {
				return authHint(err)
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), string(res.Raw))
				return nil
			}
			r := res.Request
			verb := "Request"
			if !res.Created {
				verb = "Already requested"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s #%d: %s %s %s %s - %s\n", verb, r.ID, r.Op, r.Kind, r.Name, r.Version, r.Status)
			if len(r.Permissions) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Permissions: %s\n", strings.Join(r.Permissions, ", "))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "sha256:      %s\n", r.SHA256)
			fmt.Fprintln(cmd.OutOrStdout(), res.Message)
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&in.Kind, "kind", "", "app | storage (required)")
	f.BoolVar(&upgrade, "upgrade", false, "upgrade an installed plugin (--name or --plugin-id) instead of installing one")
	f.StringVar(&in.Name, "name", "", "storage: the name to install it under; upgrade: the installed plugin's name")
	f.Int64Var(&in.PluginID, "plugin-id", 0, "upgrade: the installed plugin's id")
	f.StringVar(&in.GitHubRepo, "github-repo", "", "app: owner/name of a GitHub repository whose root holds filex-app.json")
	f.StringVar(&in.Ref, "ref", "", "app: git tag or branch (default main, then master)")
	f.StringVar(&in.ManifestURL, "manifest-url", "", "app: https address of filex-app.json")
	f.StringVar(&in.URL, "url", "", "app: the module's address; storage: the binary's https address")
	f.StringVar(&in.SHA256, "sha256", "", "the sha256 the bytes at --url must have (optional)")
	f.StringVar(&in.Source, "source", "", "storage: owner/name or the https address of a filex-storage.json")
	f.StringVar(&in.Reason, "reason", "", "why the plugin is needed (required)")
	_ = c.MarkFlagRequired("kind")
	return quiet(c)
}

func clientPluginRequestsCmd(opts *clientOpts) *cobra.Command {
	var status string
	c := &cobra.Command{
		Use:   "requests",
		Short: "List plugin requests (pending by default)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			list, raw, err := api.PluginRequests(cmd.Context(), status)
			if err != nil {
				return authHint(err)
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return nil
			}
			if len(list) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No plugin requests.")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSTATUS\tOP\tKIND\tNAME\tVERSION\tREQUESTER\tNOTE")
			for _, r := range list {
				note := r.DecisionNote
				if note == "" {
					note = r.Reason
				}
				fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Status, r.Op, r.Kind, r.Name, r.Version,
					r.Requester, truncateLine(note, 60))
			}
			return tw.Flush()
		},
	}
	c.Flags().StringVar(&status, "status", "", "pending (default) | approved | rejected | expired | superseded | all")
	return quiet(c)
}
