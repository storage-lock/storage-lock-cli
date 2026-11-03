package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	storage "github.com/storage-lock/go-storage"
	storage_lock "github.com/storage-lock/go-storage-lock"
)

// skillsCmd 是给 Agent 用的锁协调命令组。
//
// 与人工排障用的 lock/unlock 不同，这一组命令的输出是稳定的 JSON 契约，
// 调用方按 success / status 两个字段分支，不需要解析错误文本。
var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "distributed coordination commands for autonomous agents (stable JSON output)",
	Long: `Distributed coordination commands designed for agent-to-agent collaboration.

Every subcommand writes one JSON object to stdout with a "success" boolean and a
"status" string, so callers branch on fields instead of parsing error text.
The storage backend is selected by the global --storage / --dsn flags.

  lock / renew / unlock / status   single-key lease lock
  with-lock                        hold a lock while running a child process
  leader-election                  one-shot or held leadership campaign
  semaphore / semaphore-release    rate limiting permits`,
}

// skillResult 是所有 skills 子命令的输出契约。
type skillResult struct {
	Success        bool            `json:"success"`
	Status         string          `json:"status,omitempty"`
	LockID         string          `json:"lock_id,omitempty"`
	OwnerID        string          `json:"owner_id,omitempty"`
	FencingToken   storage.Version `json:"fencing_token,omitempty"`
	RemainingLease string          `json:"remaining_lease,omitempty"`
	SlotID         string          `json:"slot_id,omitempty"`
	Capacity       int             `json:"capacity,omitempty"`
	LockCount      int             `json:"lock_count,omitempty"`
	Command        string          `json:"command,omitempty"`
	Output         string          `json:"output,omitempty"`
	Waiters        int             `json:"waiters,omitempty"`
	Message        string          `json:"message,omitempty"`
	Error          string          `json:"error,omitempty"`
}

// emit 把结果写成一行 JSON。返回值用于让 RunE 返回非 nil 错误以设置退出码。
func emit(fields skillResult, err error) error {
	if err != nil {
		fields.Success = false
		fields.Error = err.Error()
	} else {
		fields.Success = true
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if encodeErr := encoder.Encode(fields); encodeErr != nil {
		return encodeErr
	}
	return err
}

// skillContext 返回 skills 命令使用的上下文：跟随进程信号，但受全局 --timeout 约束。
// 每个命令自行判断是"获取/等待"阶段该用哪个。
func skillContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	return ctx, cancel
}

// skillLockOptions 构造锁选项。skills 命令里锁的存活由存储侧 TTL 决定，
// 因此默认关闭看门狗；只有 with-lock 这种进程会一直活到任务结束的命令才打开。
//
// ttl 的合法下限是 3 秒，这与 LeaseExpireAfter 的校验保持一致。
func skillLockOptions(lockID string, ttl time.Duration, watchdog bool) *storage_lock.StorageLockOptions {
	options := storage_lock.NewStorageLockOptions()
	options.LockId = lockID
	options.LeaseExpireAfter = ttl
	options.DisableWatchDog = !watchdog
	if watchdog {
		// 续租间隔必须给租约留足余量，短 TTL 时按比例收缩。
		refresh := ttl / 3
		if refresh <= 0 {
			refresh = time.Second
		}
		if refresh > storage_lock.DefaultLeaseRefreshInterval {
			refresh = storage_lock.DefaultLeaseRefreshInterval
		}
		options.LeaseRefreshInterval = refresh
	}
	return options
}

// skillLock 用已初始化的存储构造锁。
func skillLock(lockID string, ttl time.Duration, watchdog bool) (*storage_lock.StorageLock, error) {
	if rootStorage == nil {
		return nil, errors.New("storage is not initialized")
	}
	return storage_lock.NewStorageLockWithOptions(rootStorage, skillLockOptions(lockID, ttl, watchdog))
}

// isContended 判断错误是否表示"锁被别人持有/队列轮不到我"，而不是真的故障。
// 这类结果对调用方是正常分支，不该打印成异常。
func isContended(err error) bool {
	return errors.Is(err, storage_lock.ErrLockBusy) ||
		errors.Is(err, storage_lock.ErrLockAlreadyExists) ||
		errors.Is(err, storage_lock.ErrVersionMiss) ||
		errors.Is(err, storage_lock.ErrNotYourTurn) ||
		errors.Is(err, storage_lock.ErrMaxReentrantDepth) ||
		errors.Is(err, storage_lock.ErrUpgradeConflict)
}

// parseTTL 解析 --ttl 并检查下限。
func parseTTL(raw string) (time.Duration, error) {
	ttl, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid --ttl %q: %w", raw, err)
	}
	if ttl < 3*time.Second {
		return 0, fmt.Errorf("invalid --ttl %q: must be at least 3s", raw)
	}
	return ttl, nil
}

func init() {
	rootCmd.AddCommand(skillsCmd)
}
