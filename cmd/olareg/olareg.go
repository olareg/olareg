package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/olareg/olareg/internal/slog"
)

func main() {
	err := newRootCmd().Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v", err)
	}
}

type rootOpts struct {
	log      slog.Logger
	levelStr string
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
	opts.log = slog.Null{}
	setupLogFlag(newCmd, &opts)
	newCmd.PersistentPreRunE = opts.preRun
	newCmd.AddCommand(
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
