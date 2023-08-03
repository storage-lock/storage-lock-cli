package cmd

import (
	"context"
	"github.com/fatih/color"
	message_channel "github.com/golang-infrastructure/go-message-channel"
	"github.com/spf13/cobra"
	"github.com/storage-lock/storage-lock-cli/pkg/manager"
)

func init() {
	rootCmd.AddCommand(clearCmd)
}

var clearCmd = &cobra.Command{
	Use:   "clear",
	Short: "clear all locks",
	Long:  ``,
	RunE: func(cmd *cobra.Command, args []string) error {
		return Clear(cmd.Context())
	},
}

func Clear(ctx context.Context) error {
	color.HiGreen("version %s", Version)
}

func EnsureClear(ctx context.Context) bool {
	lockManager := manager.StorageLockManager{}
	options := message_channel.NewChannelOptions()
	channel := message_channel.NewChannel(options)
	lockManager.Clear(ctx, channel)
}

func DoClear(ctx context.Context) error {

}
