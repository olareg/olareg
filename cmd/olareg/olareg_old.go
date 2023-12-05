//go:build !go1.21

package main

import (
	"github.com/spf13/cobra"
)

// Empty functions since log/slog is not provided in older versions of go.

func setupLogFlag(newCmd *cobra.Command, opts *rootOpts) {}

func setupLogger(cmd *cobra.Command, opts *rootOpts) error {
	return nil
}
