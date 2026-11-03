package cmd

import (
	"context"
	"errors"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	storage "github.com/storage-lock/go-storage"
	storage_lock "github.com/storage-lock/go-storage-lock"
)

var (
	skillLockID    string
	skillLockTTL   string
	skillLockOwner string
	skillLockWait  bool
)

// lock 是 skills 组里的"获取"命令。
// 它是无状态的：进程退出后看门狗不存在，租约就是一个从获取时刻起算的 TTL。
// 需要更长的持锁时间时，调用方必须在到期前调用 renew，或者改用 with-lock。
var skillAcquireCmd = &cobra.Command{
	Use:   "lock",
	Short: "acquire a lease lock and exit (lease is bounded by --ttl)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if skillLockID == "" {
			return emit(skillResult{}, errors.New("--id is required"))
		}
		ttl, err := parseTTL(skillLockTTL)
		if err != nil {
			return emit(skillResult{LockID: skillLockID}, err)
		}
		lock, err := skillLock(skillLockID, ttl, false)
		if err != nil {
			return emit(skillResult{LockID: skillLockID}, err)
		}
		owner := skillLockOwner
		if owner == "" {
			owner = "agent/" + uuid.NewString()
		}

		ctx, cancel := skillContext(cmd)
		defer cancel()
		if skillLockWait {
			// 阻塞获取要能被打断，避免 Agent 卡死后连 Ctrl+C 都不响应。
			ctx, cancel = signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
		}

		if skillLockWait {
			err = lock.Lock(ctx, owner)
		} else {
			err = lock.TryLock(ctx, owner)
		}
		if err != nil {
			result := skillResult{LockID: skillLockID, OwnerID: owner, Status: "busy"}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return emit(result, ctxErr)
			}
			if isContended(err) {
				return emit(result, err)
			}
			return emit(skillResult{LockID: skillLockID, OwnerID: owner}, err)
		}

		return emit(skillResult{
			LockID:         skillLockID,
			OwnerID:        owner,
			FencingToken:   mustFencingToken(ctx, lock, owner),
			RemainingLease: remainingLease(ctx, lock, owner),
			Status:         "held",
		}, nil)
	},
}

// mustFencingToken 取栅栏令牌；取不到时返回 0，不让一个只读查询拖垮已成立的持有。
// 令牌取不到本身就是异常，调用方看到 0 应当重新获取而不是继续写资源。
func mustFencingToken(ctx context.Context, lock *storage_lock.StorageLock, owner string) storage.Version {
	token, err := lock.GetFencingToken(ctx, owner)
	if err != nil {
		return 0
	}
	return token
}

// remainingLease 返回该 owner 视角的剩余租期，取不到时返回空串。
func remainingLease(ctx context.Context, lock *storage_lock.StorageLock, owner string) string {
	ttl, err := lock.TTL(ctx, owner)
	if err != nil {
		return ""
	}
	return ttl.Round(time.Millisecond).String()
}

var (
	skillRenewID    string
	skillRenewOwner string
	skillRenewTTL   string
)

var skillRenewCmd = &cobra.Command{
	Use:   "renew",
	Short: "extend the lease of a lock this owner still holds",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if skillRenewID == "" {
			return emit(skillResult{}, errors.New("--id is required"))
		}
		if skillRenewOwner == "" {
			return emit(skillResult{LockID: skillRenewID}, errors.New("--owner is required"))
		}
		ttl, err := parseTTL(skillRenewTTL)
		if err != nil {
			return emit(skillResult{LockID: skillRenewID, OwnerID: skillRenewOwner}, err)
		}
		lock, err := skillLock(skillRenewID, ttl, false)
		if err != nil {
			return emit(skillResult{LockID: skillRenewID, OwnerID: skillRenewOwner}, err)
		}
		ctx, cancel := skillContext(cmd)
		defer cancel()
		if err := lock.Renew(ctx, skillRenewOwner); err != nil {
			return emit(skillResult{LockID: skillRenewID, OwnerID: skillRenewOwner, Status: "lost"}, err)
		}
		return emit(skillResult{
			LockID:         skillRenewID,
			OwnerID:        skillRenewOwner,
			FencingToken:   mustFencingToken(ctx, lock, skillRenewOwner),
			RemainingLease: remainingLease(ctx, lock, skillRenewOwner),
			Status:         "held",
			Message:        "lease renewed",
		}, nil)
	},
}

var (
	skillUnlockID    string
	skillUnlockOwner string
)

var skillUnlockCmd = &cobra.Command{
	Use:   "unlock",
	Short: "release a lock held by the given owner",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if skillUnlockID == "" {
			return emit(skillResult{}, errors.New("--id is required"))
		}
		if skillUnlockOwner == "" {
			return emit(skillResult{LockID: skillUnlockID}, errors.New("--owner is required"))
		}
		lock, err := skillLock(skillUnlockID, storage_lock.DefaultLeaseExpireAfter, false)
		if err != nil {
			return emit(skillResult{LockID: skillUnlockID, OwnerID: skillUnlockOwner}, err)
		}
		// 释放要尽力完成：调用方取消不该把已经开始的解锁打断在中间。
		ctx, cancel := context.WithTimeout(context.WithoutCancel(cmd.Context()), storage_lock.DefaultUnlockTimeout)
		defer cancel()
		if err := lock.UnLock(ctx, skillUnlockOwner); err != nil {
			return emit(skillResult{LockID: skillUnlockID, OwnerID: skillUnlockOwner, Status: "unlock_failed"}, err)
		}
		return emit(skillResult{LockID: skillUnlockID, OwnerID: skillUnlockOwner, Status: "released"}, nil)
	},
}

var skillStatusID string

var skillStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "inspect a lock: owner, remaining lease, waiters",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if skillStatusID == "" {
			return emit(skillResult{}, errors.New("--id is required"))
		}
		lock, err := skillLock(skillStatusID, storage_lock.DefaultLeaseExpireAfter, false)
		if err != nil {
			return emit(skillResult{LockID: skillStatusID}, err)
		}
		ctx, cancel := skillContext(cmd)
		defer cancel()
		status, err := lock.Inspect(ctx)
		if err != nil {
			if errors.Is(err, storage_lock.ErrLockNotFound) {
				return emit(skillResult{LockID: skillStatusID, Status: "free"}, nil)
			}
			return emit(skillResult{LockID: skillStatusID}, err)
		}
		result := skillResult{
			LockID:    skillStatusID,
			OwnerID:   status.Information.OwnerId,
			Waiters:   len(status.Waiters),
			LockCount: status.Information.LockCount,
		}
		if status.Held {
			result.Status = "held"
			result.RemainingLease = status.RemainingLease.Round(time.Millisecond).String()
		} else {
			result.Status = "free"
			result.OwnerID = ""
		}
		if token, tokenErr := lock.GetFencingToken(ctx, status.Information.OwnerId); tokenErr == nil && status.Held {
			result.FencingToken = token
		}
		return emit(result, nil)
	},
}

func init() {
	skillAcquireCmd.Flags().StringVar(&skillLockID, "id", "", "lock identifier (required)")
	skillAcquireCmd.Flags().StringVar(&skillLockTTL, "ttl", "30s", "lease duration, at least 3s (default 30s)")
	skillAcquireCmd.Flags().StringVar(&skillLockOwner, "owner", "", "owner id (generated when omitted)")
	skillAcquireCmd.Flags().BoolVar(&skillLockWait, "wait", false, "block until the lock is acquired or --timeout expires")

	skillRenewCmd.Flags().StringVar(&skillRenewID, "id", "", "lock identifier (required)")
	skillRenewCmd.Flags().StringVar(&skillRenewOwner, "owner", "", "owner id that currently holds the lock (required)")
	skillRenewCmd.Flags().StringVar(&skillRenewTTL, "ttl", "30s", "new lease duration, at least 3s")

	skillUnlockCmd.Flags().StringVar(&skillUnlockID, "id", "", "lock identifier (required)")
	skillUnlockCmd.Flags().StringVar(&skillUnlockOwner, "owner", "", "owner id that currently holds the lock (required)")

	skillStatusCmd.Flags().StringVar(&skillStatusID, "id", "", "lock identifier (required)")

	skillsCmd.AddCommand(skillAcquireCmd, skillRenewCmd, skillUnlockCmd, skillStatusCmd)
}
