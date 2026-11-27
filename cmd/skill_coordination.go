package cmd

import (
	"context"
	"errors"
	"time"

	"github.com/spf13/cobra"
	storage_lock "github.com/storage-lock/go-storage-lock"
	coordination "github.com/storage-lock/go-storage-lock-coordination"
)

var (
	skillElectionID    string
	skillElectionTTL   string
	skillElectionOwner string
	skillElectionHold  string
)

// leader-election 参与一把锁的竞选。
//
// 默认是"试一次"：拿得到就是 leader，拿不到就是 follower，进程立刻退出。
// --hold 给定时长时改为一直等到成为 leader，然后持续续租保持领导权到时长结束并主动让出。
var skillLeaderElectionCmd = &cobra.Command{
	Use:   "leader-election --id ID [--ttl DURATION] [--hold DURATION] [--owner ID]",
	Short: "campaign for leadership; leader wins exclusively, others report follower",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if skillElectionID == "" {
			return emit(skillResult{}, errors.New("--id is required"))
		}
		ttl, err := parseTTL(skillElectionTTL)
		if err != nil {
			return emit(skillResult{LockID: skillElectionID}, err)
		}
		elector, err := coordination.NewLeaderElector(rootStorage, skillElectionID, coordination.LeaderOptions{
			LockOptions: *skillLockOptions(skillElectionID, ttl, false),
			OwnerID:     skillElectionOwner,
		})
		if err != nil {
			return emit(skillResult{LockID: skillElectionID}, err)
		}

		hold := time.Duration(0)
		if skillElectionHold != "" {
			hold, err = time.ParseDuration(skillElectionHold)
			if err != nil {
				return emit(skillResult{LockID: skillElectionID}, err)
			}
			if hold < 0 {
				return emit(skillResult{LockID: skillElectionID}, errors.New("--hold must not be negative"))
			}
		}

		ctx, cancel := skillContext(cmd)
		defer cancel()

		var leadership *coordination.Leadership
		if hold > 0 {
			leadership, err = elector.Campaign(ctx)
			if err != nil {
				return emit(skillResult{LockID: skillElectionID}, err)
			}
		} else {
			leadership, err = elector.TryCampaign(ctx)
			if err != nil {
				result := skillResult{LockID: skillElectionID, Status: "follower"}
				if isContended(err) {
					return emit(result, err)
				}
				return emit(skillResult{LockID: skillElectionID}, err)
			}
		}
		defer func() {
			resignCtx, resignCancel := context.WithTimeout(context.WithoutCancel(context.Background()), storage_lock.DefaultUnlockTimeout)
			defer resignCancel()
			_ = leadership.Resign(resignCtx)
		}()

		if hold > 0 {
			// 保持领导权到时长结束：Leadership 自带续租 supervisor，我们只需等待。
			select {
			case <-ctx.Done():
				return emit(skillResult{LockID: skillElectionID, OwnerID: leadership.OwnerID(), Status: "leader"}, ctx.Err())
			case <-time.After(hold):
			case <-leadership.Lost():
				return emit(skillResult{LockID: skillElectionID, OwnerID: leadership.OwnerID(), Status: "lost"}, errors.New("leadership lost while holding"))
			}
		}

		return emit(skillResult{
			LockID:       skillElectionID,
			OwnerID:      leadership.OwnerID(),
			FencingToken: leadership.FencingToken(),
			Status:       "leader",
			Message:      "campaign won",
		}, nil)
	},
}

var (
	skillSemaphoreID    string
	skillSemaphoreCap   int
	skillSemaphoreTTL   string
	skillSemaphoreOwner string
	skillSemaphoreHold  string
)

// semaphore 从固定容量信号量里拿一个 permit。
// 默认是"试一次"：拿得到返回 permit（_slot_id 给出占用槽位），拿不到返回 no_permit。
// --hold 给定时长时，拿到 permit 后保持到时长结束再主动释放。
var skillSemaphoreCmd = &cobra.Command{
	Use:   "semaphore --id NS --capacity N [--ttl DURATION] [--hold DURATION]",
	Short: "acquire a permit from a fixed-capacity distributed semaphore",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if skillSemaphoreID == "" {
			return emit(skillResult{}, errors.New("--id is required"))
		}
		if skillSemaphoreCap <= 0 {
			return emit(skillResult{LockID: skillSemaphoreID}, errors.New("--capacity must be > 0"))
		}
		ttl, err := parseTTL(skillSemaphoreTTL)
		if err != nil {
			return emit(skillResult{LockID: skillSemaphoreID}, err)
		}
		options := coordination.SemaphoreOptions{
			LockOptions: *skillLockOptions(skillSemaphoreID, ttl, false),
			OwnerID:     skillSemaphoreOwner,
		}
		hold := time.Duration(0)
		if skillSemaphoreHold != "" {
			hold, err = time.ParseDuration(skillSemaphoreHold)
			if err != nil {
				return emit(skillResult{LockID: skillSemaphoreID}, err)
			}
			if hold < 0 {
				return emit(skillResult{LockID: skillSemaphoreID}, errors.New("--hold must not be negative"))
			}
		}
		// 一次性获取：permit 按 ttl 自动过期；--hold 期间由 supervisor 持续续租。
		if hold <= 0 {
			options.PermitTTL = ttl
		}
		semaphore, err := coordination.NewSemaphore(rootStorage, skillSemaphoreID, skillSemaphoreCap, options)
		if err != nil {
			return emit(skillResult{LockID: skillSemaphoreID}, err)
		}

		ctx, cancel := skillContext(cmd)
		defer cancel()

		var permit *coordination.Permit
		if hold > 0 {
			permit, err = semaphore.Acquire(ctx)
		} else {
			permit, err = semaphore.TryAcquire(ctx)
		}
		if err != nil {
			if errors.Is(err, coordination.ErrNoPermitAvailable) {
				return emit(skillResult{LockID: skillSemaphoreID, Status: "no_permit", Message: "semaphore saturated"}, err)
			}
			if isContended(err) {
				return emit(skillResult{LockID: skillSemaphoreID, Status: "no_permit"}, err)
			}
			return emit(skillResult{LockID: skillSemaphoreID}, err)
		}
		defer func() {
			releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(context.Background()), storage_lock.DefaultUnlockTimeout)
			defer releaseCancel()
			_ = permit.Release(releaseCtx)
		}()

		if hold > 0 {
			select {
			case <-ctx.Done():
				return emit(skillResult{LockID: skillSemaphoreID, OwnerID: permit.OwnerID(), SlotID: permit.SlotID(), Status: "held"}, ctx.Err())
			case <-time.After(hold):
			case <-permit.Lost():
				return emit(skillResult{LockID: skillSemaphoreID, OwnerID: permit.OwnerID(), SlotID: permit.SlotID(), Status: "lost"}, errors.New("permit lease lost while holding"))
			}
		}

		return emit(skillResult{
			LockID:       skillSemaphoreID,
			OwnerID:      permit.OwnerID(),
			FencingToken: permit.FencingToken(),
			SlotID:       permit.SlotID(),
			Capacity:     semaphore.Capacity(),
			Status:       "permit_acquired",
		}, nil)
	},
}

var (
	skillSemRelID    string
	skillSemRelSlot  string
	skillSemRelOwner string
)

// semaphore-release 按 slot+owner 释放之前拿到的 permit，用于跨进程归还。
var skillSemaphoreReleaseCmd = &cobra.Command{
	Use:   "semaphore-release --slot SLOT --owner OWNER",
	Short: "release a semaphore permit held by an owner on a specific slot",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if skillSemRelSlot == "" {
			return emit(skillResult{}, errors.New("--slot is required"))
		}
		if skillSemRelOwner == "" {
			return emit(skillResult{LockID: skillSemRelSlot}, errors.New("--owner is required"))
		}
		// slot 就是一把独立的 StorageLock，owner 是获取时的唯一身份。
		lock, err := skillLock(skillSemRelSlot, storage_lock.DefaultLeaseExpireAfter, false)
		if err != nil {
			return emit(skillResult{LockID: skillSemRelSlot, OwnerID: skillSemRelOwner}, err)
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(cmd.Context()), storage_lock.DefaultUnlockTimeout)
		defer cancel()
		if err := lock.UnLock(ctx, skillSemRelOwner); err != nil {
			if errors.Is(err, storage_lock.ErrLockNotFound) {
				// 幂等释放：slot 已不存在说明 permit 早已归还或从未被占，
				// 对调用方来说"确保释放"的目标已经达成。
				return emit(skillResult{LockID: skillSemRelSlot, OwnerID: skillSemRelOwner, SlotID: skillSemRelSlot, Status: "released", Message: "already released"}, nil)
			}
			return emit(skillResult{LockID: skillSemRelSlot, OwnerID: skillSemRelOwner, Status: "release_failed"}, err)
		}
		return emit(skillResult{LockID: skillSemRelSlot, OwnerID: skillSemRelOwner, SlotID: skillSemRelSlot, Status: "released"}, nil)
	},
}

func init() {
	skillLeaderElectionCmd.Flags().StringVar(&skillElectionID, "id", "", "election identifier (required)")
	skillLeaderElectionCmd.Flags().StringVar(&skillElectionTTL, "ttl", "30s", "leadership lease duration, at least 3s")
	skillLeaderElectionCmd.Flags().StringVar(&skillElectionOwner, "owner", "", "owner id (generated when omitted)")
	skillLeaderElectionCmd.Flags().StringVar(&skillElectionHold, "hold", "", "keep leadership for this duration, then resign (e.g. 5m; empty means try once)")

	skillSemaphoreCmd.Flags().StringVar(&skillSemaphoreID, "id", "", "semaphore namespace (required)")
	skillSemaphoreCmd.Flags().IntVar(&skillSemaphoreCap, "capacity", 0, "fixed capacity, must be identical across all clients (required)")
	skillSemaphoreCmd.Flags().StringVar(&skillSemaphoreTTL, "ttl", "30s", "permit lease duration, at least 3s")
	skillSemaphoreCmd.Flags().StringVar(&skillSemaphoreOwner, "owner", "", "owner id (generated when omitted)")
	skillSemaphoreCmd.Flags().StringVar(&skillSemaphoreHold, "hold", "", "hold the permit for this duration, then release")

	skillSemaphoreReleaseCmd.Flags().StringVar(&skillSemRelID, "id", "", "semaphore namespace (informational)")
	skillSemaphoreReleaseCmd.Flags().StringVar(&skillSemRelSlot, "slot", "", "slot id returned by semaphore (required)")
	skillSemaphoreReleaseCmd.Flags().StringVar(&skillSemRelOwner, "owner", "", "owner id returned by semaphore (required)")

	skillsCmd.AddCommand(skillLeaderElectionCmd, skillSemaphoreCmd, skillSemaphoreReleaseCmd)
}
