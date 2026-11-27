package cmd

// 一次性任务：多个 Agent 抢同一个任务，全集群只允许成功执行一次。
//
// 与 lock 的区别：lock 释放后别人还能拿；once 一旦 Complete 就永久"已完成"，
// 之后任何人再抢都只会得到 already_ran。适合"部署/发版/数据迁移/发通知"
// 这类重复执行会造成真实损害的步骤。

import (
	"context"
	"errors"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	storage_lock "github.com/storage-lock/go-storage-lock"
	coordination "github.com/storage-lock/go-storage-lock-coordination"
)

var (
	skillOnceID    string
	skillOnceTTL   string
	skillOnceOwner string
	skillOnceHold  string
	skillOnceDone  bool
	skillOnceAbort bool
)

var skillOnceCmd = &cobra.Command{
	Use:   "once --id TASK [--hold DURATION | --complete | --abort] [--owner ID]",
	Short: "claim a run-once task: exactly one agent may execute it, ever",
	Long: `Claim exclusive, permanent right to run TASK.

States reported in "status":
  claimed      you won the run-once right; execute the work, then call --complete
  busy         another agent is running it right now (retry later)
  already_ran  it has been completed before; do nothing

Without --complete/--abort this command tries once, reports the outcome, and
leaves the claim held under a lease. If you cannot finish within the lease,
pass --hold to keep it alive until you exit, or re-run with --complete.

Examples:
  # try to claim, do the work, then mark it done
  storage-lock-cli --storage=cloud skills once --id=release-v1.2.3
  storage-lock-cli --storage=cloud skills once --id=release-v1.2.3 --complete --owner="$OWNER"

  # hold the claim while a long migration runs
  storage-lock-cli --storage=cloud skills once --id=migrate-2026-11 --hold=10m -- ./migrate.sh`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if skillOnceID == "" {
			return emit(skillResult{}, errors.New("--id is required"))
		}
		if skillOnceDone && skillOnceAbort {
			return emit(skillResult{LockID: skillOnceID}, errors.New("--complete and --abort are mutually exclusive"))
		}
		ttl, err := parseTTL(skillOnceTTL)
		if err != nil {
			return emit(skillResult{LockID: skillOnceID}, err)
		}

		ctx, cancel := skillContext(cmd)
		defer cancel()

		// --complete / --abort 是"结束一个已经抢到的执行权"，不需要重新抢。
		if skillOnceDone || skillOnceAbort {
			return finishOnce(ctx, skillOnceDone, skillOnceAbort)
		}

		task, err := coordination.NewOnceTask(rootStorage, skillOnceID,
			*skillLockOptions(skillOnceID, ttl, false))
		if err != nil {
			return emit(skillResult{LockID: skillOnceID}, err)
		}
		owner := skillOnceOwner
		if owner == "" {
			owner = "agent/" + uuid.NewString()
		}

		hold := time.Duration(0)
		if skillOnceHold != "" {
			if hold, err = time.ParseDuration(skillOnceHold); err != nil {
				return emit(skillResult{LockID: skillOnceID, OwnerID: owner}, err)
			}
			if hold < 0 {
				return emit(skillResult{LockID: skillOnceID, OwnerID: owner}, errors.New("--hold must not be negative"))
			}
		}

		run, err := task.TryRun(ctx, owner)
		if err != nil {
			result := skillResult{LockID: skillOnceID, OwnerID: owner}
			switch {
			case errors.Is(err, coordination.ErrTaskAlreadyRan):
				result.Status = "already_ran"
				result.Message = "this task has already been executed; do nothing"
				return emit(result, nil)
			case errors.Is(err, coordination.ErrTaskBusy):
				result.Status = "busy"
				result.Message = "another agent is running this task now"
				return emit(result, nil)
			}
			return emit(result, err)
		}
		// 崩溃兜底：没有显式完成的任务在看门狗失效后会自动重新可见。
		// 因此这里不"尽力完成"，只在 hold 模式下等到时长结束再交还。
		result := skillResult{
			LockID:       skillOnceID,
			OwnerID:      run.Owner,
			FencingToken: run.Token(),
			Status:       "claimed",
			Message:      "run it, then call --complete with this owner id",
		}

		if hold > 0 {
			runCtx, runCancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer runCancel()
			select {
			case <-runCtx.Done():
				return emit(result, runCtx.Err())
			case <-time.After(hold):
			}
			// hold 到点仍未 Complete：交还执行权（Abandon），让别的 Agent 重试。
			abandonCtx, abandonCancel := context.WithTimeout(
				context.WithoutCancel(context.Background()), storage_lock.DefaultUnlockTimeout)
			defer abandonCancel()
			_ = run.Abandon(abandonCtx)
			result.Message = "hold finished without --complete; right released"
		}
		return emit(result, nil)
	},
}

// finishOnce 在已有执行权上做 Complete / Abandon。
//
// 调用方只需要记住 --id 与 owner：CLI 进程无状态，执行权活在存储里。
func finishOnce(ctx context.Context, complete, abandon bool) error {
	if skillOnceOwner == "" {
		return emit(skillResult{LockID: skillOnceID},
			errors.New("--owner is required to complete or abort (it was returned by the claim"))
	}
	// Complete/Abandon 必须作用在**同一个 owner 的执行权**上。
	//
	// OnceTask.TryRun 的语义是"已经由同一个 owner 持有时直接返回该执行权"
	// （见 once.go：Inspect 到 OwnerId==owner 且仍被持有时不再抢），
	// 所以这一步不会重新抢、也不会把"完成"变成"再执行一次"。
	// 这也正是 --complete 必须带 --owner 的原因：owner 是执行权的唯一凭据。
	task, err := coordination.NewOnceTask(rootStorage, skillOnceID,
		*skillLockOptions(skillOnceID, storage_lock.DefaultLeaseExpireAfter, false))
	if err != nil {
		return emit(skillResult{LockID: skillOnceID}, err)
	}
	if abandon {
		run, err := task.TryRun(ctx, skillOnceOwner)
		if err != nil {
			return emit(skillResult{LockID: skillOnceID, OwnerID: skillOnceOwner, Status: "lost"},
				errors.Join(err, errors.New("cannot abort: this owner no longer holds the claim")))
		}
		if err := run.Abandon(ctx); err != nil {
			return emit(skillResult{LockID: skillOnceID, OwnerID: skillOnceOwner, Status: "lost"}, err)
		}
		return emit(skillResult{LockID: skillOnceID, OwnerID: skillOnceOwner, Status: "aborted"}, nil)
	}
	if !complete {
		return emit(skillResult{LockID: skillOnceID}, errors.New("nothing to do"))
	}
	// 完成语义：把 claim 标记为 done，之后任何 TryRun 都返回 ErrTaskAlreadyRan。
	// 用 owner 重新进入再 Complete，失败即说明执行权已不属于它。
	run, err := task.TryRun(ctx, skillOnceOwner)
	if err != nil {
		result := skillResult{LockID: skillOnceID, OwnerID: skillOnceOwner, Status: "lost"}
		if errors.Is(err, coordination.ErrTaskAlreadyRan) {
			result.Status = "already_ran"
			return emit(result, nil)
		}
		if errors.Is(err, coordination.ErrTaskBusy) {
			result.Status = "lost"
			result.Message = "claim is held by a different owner"
			return emit(result, nil)
		}
		return emit(result, err)
	}
	if err := run.Complete(ctx); err != nil {
		return emit(skillResult{LockID: skillOnceID, OwnerID: skillOnceOwner, Status: "lost"}, err)
	}
	return emit(skillResult{
		LockID: skillOnceID, OwnerID: skillOnceOwner,
		FencingToken: run.Token(), Status: "completed",
		Message: "task is permanently marked done",
	}, nil)
}

func init() {
	skillOnceCmd.Flags().StringVar(&skillOnceID, "id", "", "task identifier, shared by all agents (required)")
	skillOnceCmd.Flags().StringVar(&skillOnceTTL, "ttl", "30s", "claim lease duration, at least 3s")
	skillOnceCmd.Flags().StringVar(&skillOnceOwner, "owner", "", "owner id (generated on claim; required for --complete/--abort)")
	skillOnceCmd.Flags().StringVar(&skillOnceHold, "hold", "", "keep the claim alive for this duration, then release it")
	skillOnceCmd.Flags().BoolVar(&skillOnceDone, "complete", false, "mark the task permanently done (needs --owner)")
	skillOnceCmd.Flags().BoolVar(&skillOnceAbort, "abort", false, "give the claim back so another agent may retry (needs --owner)")
	skillsCmd.AddCommand(skillOnceCmd)
}
