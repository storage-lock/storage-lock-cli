package cmd

import (
	"errors"

	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(deleteCmd) }

var deleteCmd = &cobra.Command{
	Use:   "delete <lock-id>",
	Short: "disabled: physical deletion would destroy fencing history",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return errors.New("physical lock deletion is unsupported; release the owner lease and retain the tombstone")
	},
}
