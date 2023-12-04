package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	err := newRootCmd().Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v", err)
	}
}

type rootOpts struct {
	log      *slog.Logger
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
	// TODO: temporary log value, is this needed before the prerun executes?
	opts.log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	newCmd.PersistentFlags().StringVarP(&opts.levelStr, "verbosity", "v", "warn", "Log level (debug, info, warn, error)")
	_ = newCmd.RegisterFlagCompletionFunc("verbosity", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"debug", "info", "warn", "error"}, cobra.ShellCompDirectiveNoFileComp
	})
	newCmd.PersistentPreRunE = opts.preRun
	newCmd.AddCommand(
		newServeCmd(&opts),
	)
	return newCmd
}

func (opts *rootOpts) preRun(cmd *cobra.Command, args []string) error {
	var lvl slog.Level
	err := lvl.UnmarshalText([]byte(opts.levelStr))
	if err != nil {
		return fmt.Errorf("unable to parse verbosity %s: %v", opts.levelStr, err)
	}
	opts.log = slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: lvl}))
	return nil
}
