//go:build go1.21

package main

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
)

func setupLogFlag(newCmd *cobra.Command, opts *rootOpts) {
	newCmd.PersistentFlags().StringVarP(&opts.levelStr, "verbosity", "v", "warn", "Log level (debug, info, warn, error)")
	_ = newCmd.RegisterFlagCompletionFunc("verbosity", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"debug", "info", "warn", "error"}, cobra.ShellCompDirectiveNoFileComp
	})
}

func setupLogger(cmd *cobra.Command, opts *rootOpts) error {
	var lvl slog.Level
	err := lvl.UnmarshalText([]byte(opts.levelStr))
	if err != nil {
		return fmt.Errorf("unable to parse verbosity %s: %v", opts.levelStr, err)
	}
	opts.log = slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: lvl}))
	return nil
}
