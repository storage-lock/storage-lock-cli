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
	coordination "github.com/storage-lock/go-storage-lock-coordination"
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
  semaphore / semaphore-release    limit how many agents run at once
  rate-limit                       limit how many calls per second
  rw-lock                          shared/exclusive lock (readers don't block each other)
  once                             run a task exactly once across the cluster
  queue                            visibility-timeout work queue
  delayed-queue                    work queue with per-item delay (backoff retries)
  counter                          distributed atomic counter
  observe                          one-shot snapshot of all locks`,
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

	// ItemID / Payload 用于队列类命令：Agent 需要把 id 带回去做 ack。
	ItemID  string `json:"item_id,omitempty"`
	Payload string `json:"payload,omitempty"`
	// ReadyAt 是延迟队列元素的可见时刻（RFC3339）。
	ReadyAt string `json:"ready_at,omitempty"`

	// observe 的分组计数：Agent 用它们判断"现在该不该动手"，
	// 比逐把查状态便宜得多。
	Total int `json:"total,omitempty"`
	Held  int `json:"held_count,omitempty"`
	Free  int `json:"free_count,omitempty"`
	Stale int `json:"stale,omitempty"`

	// 计数 / 数值类命令的结果。
	Value     int64  `json:"value,omitempty"`
	SetValue  *int64 `json:"set_value,omitempty"`
	SetResult bool   `json:"set_result,omitempty"`
	Tokens    int64  `json:"tokens,omitempty"`

	// Items 是 peek / list 类命令的明细。
	Items []coordination.PendingItem `json:"items,omitempty"`
	// Locks 是 observe 的逐锁明细；与 Items（队列元素）分开，
	// 避免两类结构挤在同一个字段里让调用方不得不猜类型。
	Locks []lockView `json:"locks,omitempty"`

	// 读锁模式标记：status 里区分 shared / exclusive。
	ReadLock bool     `json:"read_lock,omitempty"`
	Mode     string   `json:"mode,omitempty"`
	Meta     []string `json:"meta,omitempty"`

	// WriteIntentOwner / WriteIntentAge 暴露"有人在排队等写"，
	// 让只读 Agent 知道现在不该再做长任务，否则会阻塞写者。
	WriteIntentOwner string `json:"write_intent_owner,omitempty"`
	WriteIntentAge   string `json:"write_intent_age,omitempty"`
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
