package cmd

import (
	"context"
	"encoding/json"
	"errors"
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
		// 纯信息型命令不该要求存储后端：`version` 要在"什么都还没配"时可用，
		// 否则新机器上第一步就跑不起来，排障时也拿不到版本号。
		if noStorageNeeded(cmd) {
			return nil
		}
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

// noStorageNeeded 判断该命令是否与存储无关。
//
// 用命令名而不是 flag：把这些命令列在这里是显式的，
// 新增信息型命令时忘了登记会立刻暴露成"要求 --storage"，不会被静默放过。
func noStorageNeeded(cmd *cobra.Command) bool {
	switch cmd.Name() {
	case "version", "help", "completion":
		return true
	}
	return false
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
		var code exitCodeError
		if errors.As(err, &code) {
			// with-lock 之类需要把子进程退出码透传给调用方的场景。
			// 结果 JSON 已经输出，这里不再重复打印 "exit status N"。
			closeRootStorage()
			os.Exit(code.code)
		}
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	// 出错时只打印一行错误，不打印整段 Usage：skills 命令的输出要能被 Agent 直接解析，
	// 而人工排障时 Usage 也只是噪音（--help 依旧完整可用）。
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true

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
