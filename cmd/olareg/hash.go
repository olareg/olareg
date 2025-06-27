package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/olareg/olareg/config"
)

type hashOpts struct {
	root *rootOpts
	pass string
}

func newHashCmd(root *rootOpts) *cobra.Command {
	opts := hashOpts{
		root: root,
	}
	newCmd := &cobra.Command{
		Use:     "hash",
		Short:   "Hash a password",
		Long:    "Hash a password",
		Example: ``,
		RunE:    opts.run,
	}
	newCmd.Flags().StringVar(&opts.pass, "pass", "", "password to hash")
	return newCmd
}

func (opts *hashOpts) run(cmd *cobra.Command, args []string) error {
	hash, err := config.PassHash(opts.pass, config.PassAlgoDefault)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s\n", hash)
	return nil
}
