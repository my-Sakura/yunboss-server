package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "get msgservice status",

	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}
