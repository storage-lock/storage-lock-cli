package cmd

// 延迟队列：元素在指定时刻之后才可见。
//
// Agent 协作里的典型用法是**退避重试**：任务失败了不要立刻重试（会撞上同样的
// 上游故障），而是放进延迟队列，5 分钟后再由任意 Agent 领走。

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	storage_lock "github.com/storage-lock/go-storage-lock"
	coordination "github.com/storage-lock/go-storage-lock-coordination"
)

var (
	skillDQID      string
	skillDQTTL     string
	skillDQOwner   string
	skillDQPayload string
	skillDQPushID  string
	skillDQDelay   string
	skillDQItemID  string
	skillDQPeek    bool
	skillDQClaim   bool
)

var skillDelayedQueueCmd = &cobra.Command{
	Use:   "delayed-queue --id QUEUE (--push --delay DURATION | --claim | --ack ITEM | --peek)",
	Short: "queue whose items only become visible after a delay (backoff retries)",
	Long: `A visibility-timeout queue where each item is hidden until its delay elapses.

Modes (exactly one per invocation):
  --push --payload TEXT --delay 5m    enqueue an item that becomes visible in 5m
  --claim                             take the oldest item whose delay has elapsed
  --ack ITEM --owner OWNER            confirm an item is done
  --peek                              list items whose delay has elapsed

Unlike a sleeping process, a delayed item survives restarts and can be claimed
by any agent in the cluster — use it for retry-with-backoff instead of sleep.

Examples:
  storage-lock-cli --storage=cloud skills delayed-queue --id=retries --push --payload='{"job":"build-42"}' --delay=5m
  storage-lock-cli --storage=cloud skills delayed-queue --id=retries --claim`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if skillDQID == "" {
			return emit(skillResult{}, errors.New("--id is required"))
		}
		ttl, err := parseTTL(skillDQTTL)
		if err != nil {
			return emit(skillResult{LockID: skillDQID}, err)
		}
		q, err := coordination.NewDelayedQueue(rootStorage, skillDQID,
			*skillLockOptions(skillDQID, ttl, true))
		if err != nil {
			return emit(skillResult{LockID: skillDQID}, err)
		}
		ctx, cancel := skillContext(cmd)
		defer cancel()

		switch {
		case skillDQPeek:
			items, err := q.Peek(ctx)
			if err != nil {
				return emit(skillResult{LockID: skillDQID}, err)
			}
			return emit(skillResult{LockID: skillDQID, Status: "peek", Items: items}, nil)

		case cmd.Flags().Changed("push"):
			return emitDQPush(ctx, q)

		case skillDQItemID != "":
			return emitDQAck(ctx, q)

		case skillDQClaim:
			return emitDQClaim(ctx, q)

		default:
			return emit(skillResult{LockID: skillDQID},
				errors.New("specify one of --push, --claim, --ack ITEM, --peek"))
		}
	},
}

func emitDQClaim(ctx context.Context, q *coordination.DelayedQueue) error {
	item, err := q.Claim(ctx, ownerOrDefault(skillDQOwner))
	if err != nil {
		if errors.Is(err, coordination.ErrQueueEmpty) {
			return emit(skillResult{LockID: skillDQID, Status: "empty",
				Message: "no item is visible yet (delays not elapsed or all claimed)"}, nil)
		}
		return emit(skillResult{LockID: skillDQID}, err)
	}
	return emit(skillResult{
		LockID: skillDQID, OwnerID: item.Owner, ItemID: item.ID, Payload: item.Payload,
		ReadyAt: item.ReadyAt.UTC().Format(time.RFC3339Nano), FencingToken: item.Token,
		Status:  "claimed",
		Message: "process it, then call --ack=" + item.ID + " --owner=" + item.Owner,
	}, nil)
}

func emitDQPush(ctx context.Context, q *coordination.DelayedQueue) error {
	if skillDQDelay == "" {
		return emit(skillResult{LockID: skillDQID}, errors.New("--delay is required with --push (e.g. 5m)"))
	}
	delay, err := time.ParseDuration(skillDQDelay)
	if err != nil || delay <= 0 {
		return emit(skillResult{LockID: skillDQID}, errors.New("--delay must be a positive duration (e.g. 30s, 5m)"))
	}
	id := skillDQPushID
	if id == "" {
		id = "item-" + uuid.NewString()
	}
	if err := q.Push(ctx, id, skillDQPayload, delay); err != nil {
		if isContended(err) {
			return emit(skillResult{LockID: skillDQID, ItemID: id, Status: "busy",
				Message: "an item with this id already exists"}, nil)
		}
		return emit(skillResult{LockID: skillDQID, ItemID: id}, err)
	}
	return emit(skillResult{
		LockID: skillDQID, ItemID: id, Payload: skillDQPayload,
		ReadyAt: time.Now().Add(delay).UTC().Format(time.RFC3339Nano),
		Status:  "pushed", Message: "visible after " + delay.String(),
	}, nil)
}

func emitDQAck(ctx context.Context, q *coordination.DelayedQueue) error {
	if skillDQOwner == "" {
		return emit(skillResult{LockID: skillDQID, ItemID: skillDQItemID},
			errors.New("--owner is required to ack (it was returned by --claim"))
	}
	ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), storage_lock.DefaultUnlockTimeout)
	defer ackCancel()
	// DelayedItem 的 owner 带 "dq-owner:" 前缀，Claim 返回的就是它，
	// 调用方原样带回即可；这里不再加工，避免两侧前缀不一致。
	if err := q.AckByOwner(ackCtx, skillDQItemID, skillDQOwner); err != nil {
		return emit(skillResult{LockID: skillDQID, ItemID: skillDQItemID, OwnerID: skillDQOwner,
			Status: "ack_failed"}, err)
	}
	return emit(skillResult{LockID: skillDQID, ItemID: skillDQItemID, OwnerID: skillDQOwner,
		Status: "acked"}, nil)
}

func init() {
	skillDelayedQueueCmd.Flags().StringVar(&skillDQID, "id", "", "queue name, shared by all agents (required)")
	skillDelayedQueueCmd.Flags().StringVar(&skillDQTTL, "ttl", "60s", "claim lease duration, at least 3s")
	skillDelayedQueueCmd.Flags().StringVar(&skillDQOwner, "owner", "", "owner id (generated on claim; required for --ack)")
	skillDelayedQueueCmd.Flags().StringVar(&skillDQPayload, "payload", "", "item payload for --push")
	skillDelayedQueueCmd.Flags().StringVar(&skillDQPushID, "item", "", "explicit item id for --push (default: generated)")
	skillDelayedQueueCmd.Flags().StringVar(&skillDQDelay, "delay", "", "how long the pushed item stays hidden (e.g. 5m)")
	skillDelayedQueueCmd.Flags().Bool("push", false, "push a new item onto the queue")
	skillDelayedQueueCmd.Flags().StringVar(&skillDQItemID, "ack", "", "item id to acknowledge (requires --owner)")
	skillDelayedQueueCmd.Flags().BoolVar(&skillDQPeek, "peek", false, "list items whose delay has elapsed")
	skillDelayedQueueCmd.Flags().BoolVar(&skillDQClaim, "claim", false, "take the oldest item whose delay has elapsed")
	skillsCmd.AddCommand(skillDelayedQueueCmd)
}
