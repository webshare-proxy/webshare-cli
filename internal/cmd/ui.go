package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/webshare-proxy/webshare-cli/internal/output"
	"github.com/webshare-proxy/webshare-cli/internal/tui"
)

func newUICmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Browse your account in an interactive terminal UI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Never draw into a pipe or CI log: the UI needs a real terminal
			// on both ends.
			if !output.IsTerminal(os.Stdin) || !output.IsTerminal(os.Stdout) {
				return usagef("webshare ui needs an interactive terminal; use the other commands in scripts")
			}
			client, err := newClient(cmd.Context(), flags)
			if err != nil {
				return err
			}
			return tui.Run(cmd.Context(), client)
		},
	}
}
