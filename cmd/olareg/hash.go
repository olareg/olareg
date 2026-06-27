// Copyright the olareg contributors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
