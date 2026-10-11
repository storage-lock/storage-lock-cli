package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var unlockOwner string

func init() {
	unlockCmd.Flags().StringVar(&unlockOwner, "owner", "", "owner/session id that acquired the lock")
	rootCmd.AddCommand(unlockCmd)
}

var unlockCmd = &cobra.Command{
	Use:   "unlock <lock-id>",
	Short: "release a lock owned by the specified owner",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if unlockOwner == "" {
			return fmt.Errorf("--owner is required; force unlock is intentionally unsupported")
		}
		ctx, cancel := commandContext(cmd)
		defer cancel()
		if err := rootManager.Unlock(ctx, args[0], unlockOwner); err != nil {
			return err
		}
		return printValue(map[string]any{"lock_id": args[0], "owner_id": unlockOwner, "status": "released"})
	},
}
