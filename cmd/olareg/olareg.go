package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/olareg/olareg/internal/slog"
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
	log      slog.Logger
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
	opts.log = slog.Null{}
	setupLogFlag(newCmd, &opts)

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
	)
	return newCmd
}

func (opts *rootOpts) preRun(cmd *cobra.Command, args []string) error {
	err := setupLogger(cmd, opts)
	if err != nil {
		return err
	}
	return nil
}

func (opts *rootOpts) runVersion(cmd *cobra.Command, args []string) error {
	info := version.GetInfo()
	return template.Writer(cmd.OutOrStdout(), opts.format, info)
}
