package cmd

import (
	"time"

	"github.com/spf13/cobra"
	storage "github.com/storage-lock/go-storage"
)

var (
	listActiveOnly bool
	listTombstones bool
)

func init() {
	listCmd.Flags().BoolVar(&listActiveOnly, "active-only", false, "show only currently held locks")
	listCmd.Flags().BoolVar(&listTombstones, "tombstones", true, "include released tombstone records")
	rootCmd.AddCommand(listCmd)
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "list lock records",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := commandContext(cmd)
		defer cancel()
		rows, err := rootManager.List(ctx)
		if err != nil {
			return err
		}
		filtered := make([]*storage.LockInformation, 0, len(rows))
		now := timeNow()
		for _, row := range rows {
			held := row.LockCount > 0 && now.Before(row.LeaseExpireTime)
			if listActiveOnly && !held {
				continue
			}
			if !listTombstones && !held {
				continue
			}
			filtered = append(filtered, row)
		}
		return printValue(filtered)
	},
}

func timeNow() time.Time { return time.Now() }
