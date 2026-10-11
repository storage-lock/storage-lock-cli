package cmd

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var lockOwner string

func init() {
	lockCmd.Flags().StringVar(&lockOwner, "owner", "", "owner/session id (generated when omitted)")
	rootCmd.AddCommand(lockCmd)
}

var lockCmd = &cobra.Command{
	Use:   "lock <lock-id>",
	Short: "acquire a lock and hold it until interrupted",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		owner := lockOwner
		if owner == "" {
			owner = "cli/" + uuid.NewString()
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		lock, err := storageLockFor(args[0])
		if err != nil {
			return err
		}
		if err := lock.Lock(ctx, owner); err != nil {
			return err
		}
		if err := printValue(map[string]any{"lock_id": args[0], "owner_id": owner, "status": "held"}); err != nil {
			return err
		}
		<-ctx.Done()
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), lockCleanupTimeout())
		defer cancel()
		if err := lock.UnLock(cleanupCtx, owner); err != nil {
			return fmt.Errorf("lock acquired but cleanup failed: %w", err)
		}
		return nil
	},
}
