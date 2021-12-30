package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(reloadCmd)
}

var reloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "reload msgservice",
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}
