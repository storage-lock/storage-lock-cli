package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	storage_lock "github.com/storage-lock/go-storage-lock"
)

func init() { rootCmd.AddCommand(showCmd) }

var showCmd = &cobra.Command{
	Use:   "show <lock-id>",
	Short: "show one lock record",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := commandContext(cmd)
		defer cancel()
		lock, err := storage_lock.NewStorageLock(rootStorage, args[0])
		if err != nil {
			return err
		}
		status, err := lock.Inspect(ctx)
		if err != nil {
			if errors.Is(err, storage_lock.ErrLockNotFound) {
				return fmt.Errorf("lock %q not found", args[0])
			}
			return err
		}
		return printValue(status)
	},
}
