package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/olareg/olareg"
	"github.com/olareg/olareg/internal/cobradoc"
	"github.com/olareg/olareg/internal/sloghandle"
	"github.com/olareg/olareg/internal/template"
	"github.com/olareg/olareg/internal/version"
)

func main() {
	err := newRootCmd().Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v", err)
	}
}

type rootOpts struct {
	format   string // for Go template formatting of various commands
	log      *slog.Logger
	levelStr string
	name     string // name of the command, extracted from cobra
}

func newRootCmd() *cobra.Command {
	opts := rootOpts{}
	newCmd := &cobra.Command{
		Use:           "olareg <cmd>",
		Short:         "Registry server based on the OCI Layout",
		Long:          "Registry server based on the OCI Layout",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	opts.name = newCmd.Name()
	opts.log = slog.New(sloghandle.Discard)
	newCmd.PersistentFlags().StringVarP(&opts.levelStr, "verbosity", "v", "warn", "Log level (trace, debug, info, warn, error)")
	_ = newCmd.RegisterFlagCompletionFunc("verbosity", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"debug", "info", "warn", "error"}, cobra.ShellCompDirectiveNoFileComp
	})

	var versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Show the version",
		Long:  fmt.Sprintf(`Show the version of %s`, opts.name),
		Example: `
# display full version details
olareg version

# retrieve the version number
olareg version --format '{{.VCSTag}}'`,
		Args: cobra.ExactArgs(0),
		RunE: opts.runVersion,
	}
	versionCmd.Flags().StringVarP(&opts.format, "format", "", "{{printPretty .}}", "Format output with go template syntax")
	_ = versionCmd.RegisterFlagCompletionFunc("format", completeArgNone)

	newCmd.PersistentPreRunE = opts.preRun
	newCmd.AddCommand(
		versionCmd,
		newServeCmd(&opts),
		cobradoc.NewCmd(opts.name, "cli-doc"),
	)
	return newCmd
}

func (opts *rootOpts) preRun(cmd *cobra.Command, args []string) error {
	var lvl slog.Level
	err := lvl.UnmarshalText([]byte(opts.levelStr))
	if err != nil {
		// handle custom levels
		if opts.levelStr == strings.ToLower("trace") {
			lvl = olareg.LogTrace
		} else {
			return fmt.Errorf("unable to parse verbosity %s: %v", opts.levelStr, err)
		}
	}
	opts.log = slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: lvl}))
	return nil
}

func (opts *rootOpts) runVersion(cmd *cobra.Command, args []string) error {
	info := version.GetInfo()
	return template.Writer(cmd.OutOrStdout(), opts.format, info)
}
