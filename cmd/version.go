package cmd

// 版本信息。默认值用于 `go build` / `go install` 的场景；
// 正式发布时由 goreleaser 通过 -ldflags 覆盖（见 .goreleaser.yaml）。

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
)

// 以下三个变量由 goreleaser 注入，不要在业务代码里读它们——
// 用 VersionString() 以便将来换实现（例如读 build info）。
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func init() {
	rootCmd.AddCommand(versionCmd)
	// 让 `--version` 与 `version` 子命令行为一致：Agent 和脚本两处都会用到。
	rootCmd.Version = VersionString()
	rootCmd.SetVersionTemplate("{{.Version}}\n")
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "print version, commit and build date",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if outputFormat == "table" || !jsonPassthrough() {
			fmt.Fprintf(os.Stdout, "storage-lock-cli %s (commit %s, built %s, %s/%s, go %s)\n",
				version, commit, buildDate, runtime.GOOS, runtime.GOARCH, runtime.Version())
			return
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
			"name":       "storage-lock-cli",
			"version":    version,
			"commit":     commit,
			"build_date": buildDate,
			"os":         runtime.GOOS,
			"arch":       runtime.GOARCH,
			"go_version": runtime.Version(),
		})
	},
}

// VersionString 返回适合 `--version` 的单行输出。
func VersionString() string { return version }

// jsonPassthrough 判断当前输出是否应为 JSON。
//
// `version` 是给人看还是给 Agent 看，取决于全局 --output；
// 默认（json）时输出结构化结果，便于脚本解析。
func jsonPassthrough() bool { return outputFormat == "json" }
