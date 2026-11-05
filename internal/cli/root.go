// Package cli defines the command-line entry point.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewRootCommand builds an independent command tree for each invocation.
func NewRootCommand(version string) *cobra.Command {
	root := &cobra.Command{
		Use:           "gitea-cli",
		Short:         "CLI for self-hosted Gitea",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "gitea-cli %s\n", version)
			return err
		},
	})

	return root
}
