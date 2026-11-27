package cmd

// 读写锁：读读不互斥、读写互斥。
//
// Agent 场景里最有价值的用法是"只读排查不被写操作阻塞"：
// 多个 Agent 可以同时读同一个资源，写操作必须独占。
// 用普通互斥锁会把这些只读任务串行化，白白浪费并发。

import (
	"context"
	"errors"

	"github.com/spf13/cobra"
	storage_lock "github.com/storage-lock/go-storage-lock"
)

var (
	skillRWID        string
	skillRWTTL       string
	skillRWOwner     string
	skillRWWait      bool
	skillRWMode      string
	skillRWRelease   bool
	skillRWUpgrade   bool
	skillRWDowngrade bool
)

const (
	rwModeRead  = "read"
	rwModeWrite = "write"
)

var skillRWLockCmd = &cobra.Command{
	Use:   "rw-lock --id ID --mode read|write [--wait | --unlock | --upgrade | --downgrade]",
	Short: "shared/exclusive lock: many readers may hold it, writers exclude everyone",
	Long: `Acquire, release, upgrade or downgrade a shared (read) / exclusive (write) lease.

  --mode=read             take a shared lock; other readers may hold it too
  --mode=write            take the exclusive lock; excludes every reader and writer
  --mode=read --unlock    release a shared lock (needs --owner)
  --mode=write --unlock   release an exclusive lock (needs --owner)
  --upgrade               atomically turn your shared lock into exclusive
                          (only succeeds when you are the only reader)
  --downgrade             atomically turn your exclusive lock into shared

Status values: held, busy (someone else holds it), released, lost.

Why this exists: a plain lock serialises read-only inspection work behind
writers. With rw-lock, many agents can inspect concurrently while a writer
still gets full exclusion.

Examples:
  storage-lock-cli --storage=cloud skills rw-lock --id=report --mode=read
  storage-lock-cli --storage=cloud skills rw-lock --id=report --mode=write --wait=60s
  storage-lock-cli --storage=cloud skills rw-lock --id=report --mode=write --unlock --owner="$OWNER"
  storage-lock-cli --storage=cloud skills rw-lock --id=report --upgrade --owner="$OWNER"`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if skillRWID == "" {
			return emit(skillResult{}, errors.New("--id is required"))
		}
		// --upgrade/--dowgrade 自带方向，不需要 --mode；其余情况必须指定模式。
		needMode := !skillRWUpgrade && !skillRWDowngrade
		if needMode && (skillRWMode != rwModeRead && skillRWMode != rwModeWrite) {
			return emit(skillResult{LockID: skillRWID}, errors.New("--mode must be read or write"))
		}
		if skillRWUpgrade && skillRWDowngrade {
			return emit(skillResult{LockID: skillRWID},
				errors.New("--upgrade and --downgrade are mutually exclusive"))
		}
		ttl, err := parseTTL(skillRWTTL)
		if err != nil {
			return emit(skillResult{LockID: skillRWID}, err)
		}
		lock, err := skillLock(skillRWID, ttl, false)
		if err != nil {
			return emit(skillResult{LockID: skillRWID}, err)
		}
		ctx, cancel := skillContext(cmd)
		defer cancel()
		owner := ownerOrDefault(skillRWOwner)

		switch {
		case skillRWUpgrade:
			if err := lock.TryUpgrade(ctx, owner); err != nil {
				return emitContended(skillResult{LockID: skillRWID, OwnerID: owner, Mode: rwModeWrite}, err)
			}
			return emit(skillResult{LockID: skillRWID, OwnerID: owner, Mode: rwModeWrite,
				Status: "held", Message: "upgraded to exclusive"}, nil)

		case skillRWDowngrade:
			if err := lock.Downgrade(ctx, owner); err != nil {
				return emitContended(skillResult{LockID: skillRWID, OwnerID: owner, Mode: rwModeRead}, err)
			}
			return emit(skillResult{LockID: skillRWID, OwnerID: owner, Mode: rwModeRead,
				Status: "held", Message: "downgraded to shared"}, nil)

		case skillRWRelease:
			releaseCtx, releaseCancel := context.WithTimeout(
				context.WithoutCancel(cmd.Context()), storage_lock.DefaultUnlockTimeout)
			defer releaseCancel()
			if err := releaseRW(releaseCtx, lock, owner, skillRWMode == rwModeRead); err != nil {
				return emit(skillResult{LockID: skillRWID, OwnerID: owner, Mode: skillRWMode,
					Status: "release_failed"}, err)
			}
			return emit(skillResult{LockID: skillRWID, OwnerID: owner, Mode: skillRWMode,
				Status: "released"}, nil)
		}

		var acquireErr error
		if skillRWMode == rwModeRead {
			acquireErr = acquireShared(ctx, lock, owner, skillRWWait)
		} else {
			acquireErr = acquireExclusive(ctx, lock, owner, skillRWWait)
		}
		if acquireErr != nil {
			return emitContended(skillResult{LockID: skillRWID, OwnerID: owner, Mode: skillRWMode}, acquireErr)
		}
		view, err := inspected(ctx, lock, skillRWID, owner)
		result := skillResult{
			LockID: skillRWID, OwnerID: owner, Mode: skillRWMode,
			ReadLock: skillRWMode == rwModeRead, Status: "held",
		}
		if err == nil {
			result.FencingToken = view.FencingToken
			result.RemainingLease = view.RemainingLease
			result.LockCount = view.LockCount
		}
		return emit(result, nil)
	},
}

// acquireShared / acquireExclusive 打开看门狗（watchdog=true）：
// 读写锁命令的持有期通常介于"一次调用"和"with-lock 全程"之间，
// 由调用方通过 --ttl 决定边界；这里不做进程内续租，与 lock 命令保持一致。
func acquireShared(ctx context.Context, lock *storage_lock.StorageLock, owner string, wait bool) error {
	if wait {
		// RLock 会循环重试直到成功或 ctx 结束，正是"阻塞获取"的语义。
		return lock.RLock(ctx, owner)
	}
	return lock.TryRLock(ctx, owner)
}

func acquireExclusive(ctx context.Context, lock *storage_lock.StorageLock, owner string, wait bool) error {
	if wait {
		return lock.WLock(ctx, owner)
	}
	return lock.TryWLock(ctx, owner)
}

func releaseRW(ctx context.Context, lock *storage_lock.StorageLock, owner string, read bool) error {
	if read {
		return lock.RUnLock(ctx, owner)
	}
	return lock.WUnLock(ctx, owner)
}

// emitContended 把"没拿到"翻译成 busy，并保留 JSON 契约。
func emitContended(fields skillResult, err error) error {
	if isContended(err) {
		fields.Status = "busy"
		fields.Message = contentionMessage(err)
		return emit(fields, nil)
	}
	return emit(fields, err)
}

func init() {
	skillRWLockCmd.Flags().StringVar(&skillRWID, "id", "", "lock identifier (required)")
	skillRWLockCmd.Flags().StringVar(&skillRWMode, "mode", "", "read (shared) or write (exclusive); required")
	skillRWLockCmd.Flags().StringVar(&skillRWTTL, "ttl", "30s", "lease duration, at least 3s")
	skillRWLockCmd.Flags().StringVar(&skillRWOwner, "owner", "", "owner id (generated when omitted)")
	skillRWLockCmd.Flags().BoolVar(&skillRWWait, "wait", true,
		"block until acquired (default true); use --wait=false for try-once")
	skillRWLockCmd.Flags().BoolVar(&skillRWRelease, "unlock", false, "release this mode's lock (needs --owner)")
	skillRWLockCmd.Flags().BoolVar(&skillRWUpgrade, "upgrade", false, "atomically upgrade read to write")
	skillRWLockCmd.Flags().BoolVar(&skillRWDowngrade, "downgrade", false, "atomically downgrade write to read")
	skillsCmd.AddCommand(skillRWLockCmd)
}
