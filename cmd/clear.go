package cmd

import (
	"context"
	"errors"

	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(clearCmd) }

var clearCmd = &cobra.Command{
	Use:   "clear",
	Short: "disabled: physical deletion would destroy fencing history",
	RunE: func(cmd *cobra.Command, args []string) error {
		return errors.New("clearing lock records is unsupported; release leases and retain tombstones")
	},
}

// 保留旧导出名，但不再执行危险的物理清理。
func Clear(context.Context) error       { return errors.New("clearing lock records is unsupported") }
func EnsureClear(context.Context) bool  { return false }
func DoClear(ctx context.Context) error { return Clear(ctx) }
