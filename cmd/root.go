package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	cc "github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"
	storage "github.com/storage-lock/go-storage"
	"github.com/storage-lock/storage-lock-cli/pkg/creater"
	"github.com/storage-lock/storage-lock-cli/pkg/manager"
)

var (
	storageName  string
	dsn          string
	timeout      time.Duration
	outputFormat string
	rootManager  *manager.StorageLockManager
	rootStorage  storage.Storage
)

var rootCmd = &cobra.Command{
	Use:   "storage-lock-cli",
	Short: "storage-lock command line tool",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if storageName == "" {
			return fmt.Errorf("--storage is required (supported: %s)", strings.Join(creater.ListSupportStorageName(), ", "))
		}
		created, err := creater.CreateStorage(strings.ToLower(storageName), dsn)
		if err != nil {
			return err
		}
		if err := created.Init(cmd.Context()); err != nil {
			_ = created.Close(context.Background())
			return fmt.Errorf("initialize storage: %w", err)
		}
		rootStorage = created
		rootManager = manager.NewStorageLockManager(created)
		return nil
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		closeRootStorage()
	},
	Run: func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
}

func closeRootStorage() {
	if rootStorage != nil {
		_ = rootStorage.Close(context.Background())
		rootStorage = nil
		rootManager = nil
	}
}

func commandContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return context.WithTimeout(cmd.Context(), timeout)
}

func printValue(value any) error {
	if outputFormat == "table" {
		return printTable(value)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func Execute() {
	defer closeRootStorage()
	cc.Init(&cc.Config{RootCmd: rootCmd, Headings: cc.HiCyan + cc.Bold + cc.Underline, Commands: cc.HiYellow + cc.Bold, Example: cc.Italic, ExecName: cc.Bold, Flags: cc.Bold})
	rootCmd.SetOut(color.Output)
	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&storageName, "storage", "", "storage implementation name")
	rootCmd.PersistentFlags().StringVar(&dsn, "dsn", "", "storage DSN or connection URI")
	rootCmd.PersistentFlags().DurationVar(&timeout, "timeout", 30*time.Second, "command timeout")
	rootCmd.PersistentFlags().StringVar(&outputFormat, "output", "json", "output format: json or table")
}

func printTable(value any) error {
	// 表格模式保持无额外依赖，适合人工排障；复杂值退回 JSON。
	if rows, ok := value.([]*storage.LockInformation); ok {
		fmt.Fprintln(os.Stdout, "LOCK_ID\tOWNER_ID\tVERSION\tCOUNT\tLEASE_EXPIRE")
		for _, row := range rows {
			fmt.Fprintf(os.Stdout, "%s\t%s\t%d\t%d\t%s\n", row.LockId, row.OwnerId, row.Version, row.LockCount, row.LeaseExpireTime.Format(time.RFC3339Nano))
		}
		return nil
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(data))
	return err
}
