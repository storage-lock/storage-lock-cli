package cmd

// 可见性超时队列：Agent 之间用队列传递"待处理任务"。
//
// 与普通消息队列最大的区别是**处理方崩溃后元素自动重新可见**：
// Claim 会给元素加一把租约锁，处理完 Ack 才真正移除；进程被杀死时租约到期，
// 元素回到队列里，别的 Agent 可以接手。这正是 Agent 协作最需要的语义——
// Agent 经常被 SIGKILL，没有它就会"任务凭空消失"。

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
	skillQueueID      string
	skillQueueTTL     string
	skillQueueOwner   string
	skillQueuePayload string
	skillQueuePushID  string
	skillQueueItemID  string
	skillQueuePeek    bool
	skillQueueClaim   bool
	skillQueueWait    string
)

var skillQueueCmd = &cobra.Command{
	Use:   "queue --id QUEUE (--push | --claim | --ack ITEM | --peek)",
	Short: "visibility-timeout work queue: crashed consumers lose their claim automatically",
	Long: `A distributed work queue whose items become visible again if the consumer dies.

Modes (exactly one per invocation):
  --push --payload TEXT [--item ID]   enqueue an item (ID defaults to a new uuid)
  --claim                             take the oldest visible item; prints its id+payload
  --ack ITEM --owner OWNER            confirm an item is done and remove it
  --peek                              list currently visible items without claiming

Claiming holds a lease. If the process dies before --ack, the lease expires and
the item becomes claimable again — no orphaned work.

Status values:
  pushed    enqueued
  claimed   you own it; process it, then --ack
  acked     removed
  empty     nothing visible right now

Examples:
  storage-lock-cli --storage=cloud skills queue --id=jobs --push --payload="build:42"
  storage-lock-cli --storage=cloud skills queue --id=jobs --claim
  storage-lock-cli --storage=cloud skills queue --id=jobs --ack=build-42 --owner="$OWNER"`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if skillQueueID == "" {
			return emit(skillResult{}, errors.New("--id is required"))
		}
		ttl, err := parseTTL(skillQueueTTL)
		if err != nil {
			return emit(skillResult{LockID: skillQueueID}, err)
		}
		q, err := coordination.NewQueue(rootStorage, skillQueueID,
			*skillLockOptions(skillQueueID, ttl, true))
		if err != nil {
			return emit(skillResult{LockID: skillQueueID}, err)
		}
		ctx, cancel := skillContext(cmd)
		defer cancel()

		switch {
		case skillQueuePeek:
			return emitQueuePeek(ctx, q)

		case skillQueuePushID != "" || cmd.Flags().Changed("push"):
			return emitQueuePush(ctx, q)

		case skillQueueItemID != "":
			return emitQueueAck(ctx, q)

		case skillQueueClaim:
			return emitQueueClaim(cmd, ctx, q)

		default:
			return emit(skillResult{LockID: skillQueueID},
				errors.New("specify one of --push, --claim, --ack ITEM, --peek"))
		}
	},
}

func emitQueuePush(ctx context.Context, q *coordination.Queue) error {
	id := skillQueuePushID
	if id == "" {
		id = "item-" + uuid.NewString()
	}
	if err := q.Push(ctx, id, skillQueuePayload); err != nil {
		result := skillResult{LockID: skillQueueID, ItemID: id, Status: "busy"}
		if isContended(err) {
			result.Message = "an item with this id already exists in the queue"
			return emit(result, nil)
		}
		return emit(skillResult{LockID: skillQueueID, ItemID: id}, err)
	}
	return emit(skillResult{
		LockID: skillQueueID, ItemID: id, Payload: skillQueuePayload,
		Status: "pushed", Message: "enqueued",
	}, nil)
}

// emitQueueClaim 抢占一个可见元素。
//
// --wait 给定时长时轮询直到拿到元素或超时：Agent 常见用法是"消费者进程常驻，
// 没有任务就等一会儿"。轮询间隔取 1s——队列是存储扫描而不是 watch，
// 太短会打爆共享锁服务。
func emitQueueClaim(cmd *cobra.Command, ctx context.Context, q *coordination.Queue) error {
	owner := skillQueueOwner
	if owner == "" {
		owner = "agent/" + uuid.NewString()
	}
	wait := time.Duration(0)
	if skillQueueWait != "" {
		w, err := time.ParseDuration(skillQueueWait)
		if err != nil {
			return emit(skillResult{LockID: skillQueueID, OwnerID: owner}, err)
		}
		if w > 0 {
			wait = w
		}
	}
	deadline := time.Now().Add(wait)
	for {
		item, err := q.Claim(ctx, owner)
		if err == nil {
			itemCtx, itemCancel := item.Context(ctx)
			_ = itemCtx
			itemCancel()
			return emit(skillResult{
				LockID: skillQueueID, OwnerID: item.Owner, ItemID: item.ID,
				Payload: item.Payload, FencingToken: item.Token, Status: "claimed",
				Message: "process it, then call --ack=" + item.ID + " --owner=" + item.Owner,
			}, nil)
		}
		if !errors.Is(err, coordination.ErrQueueEmpty) {
			return emit(skillResult{LockID: skillQueueID, OwnerID: owner}, err)
		}
		if wait <= 0 || time.Now().After(deadline) {
			return emit(skillResult{LockID: skillQueueID, OwnerID: owner, Status: "empty",
				Message: "no visible item"}, nil)
		}
		select {
		case <-ctx.Done():
			return emit(skillResult{LockID: skillQueueID, OwnerID: owner, Status: "empty"}, ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

func emitQueueAck(ctx context.Context, q *coordination.Queue) error {
	if skillQueueOwner == "" {
		return emit(skillResult{LockID: skillQueueID, ItemID: skillQueueItemID},
			errors.New("--owner is required to ack (it was returned by --claim"))
	}
	// Ack 走 AckByOwner：Claim 与 Ack 通常发生在**不同进程**（Agent 每次调用都是新进程），
	// owner 是租约凭证，对不上时底层 UnLock 会拒绝，不会误删别人的元素。
	ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), storage_lock.DefaultUnlockTimeout)
	defer ackCancel()
	if err := q.AckByOwner(ackCtx, skillQueueItemID, skillQueueOwner); err != nil {
		return emit(skillResult{LockID: skillQueueID, ItemID: skillQueueItemID, OwnerID: skillQueueOwner,
			Status: "ack_failed"}, err)
	}
	return emit(skillResult{LockID: skillQueueID, ItemID: skillQueueItemID, OwnerID: skillQueueOwner,
		Status: "acked"}, nil)
}

func emitQueuePeek(ctx context.Context, q *coordination.Queue) error {
	items, err := q.Peek(ctx)
	if err != nil {
		return emit(skillResult{LockID: skillQueueID}, err)
	}
	result := skillResult{LockID: skillQueueID, Status: "peek", Items: items}
	return emit(result, nil)
}

func init() {
	skillQueueCmd.Flags().StringVar(&skillQueueID, "id", "", "queue name, shared by all agents (required)")
	skillQueueCmd.Flags().StringVar(&skillQueueTTL, "ttl", "60s", "claim lease duration, at least 3s")
	skillQueueCmd.Flags().StringVar(&skillQueueOwner, "owner", "", "owner id (generated on claim; required for --ack)")
	skillQueueCmd.Flags().StringVar(&skillQueuePayload, "payload", "", "item payload for --push")
	skillQueueCmd.Flags().StringVar(&skillQueuePushID, "item", "", "explicit item id for --push (default: generated)")
	skillQueueCmd.Flags().Bool("push", false, "push a new item onto the queue")
	skillQueueCmd.Flags().StringVar(&skillQueueItemID, "ack", "", "item id to acknowledge (requires --owner)")
	skillQueueCmd.Flags().BoolVar(&skillQueuePeek, "peek", false, "list visible items without claiming")
	skillQueueCmd.Flags().BoolVar(&skillQueueClaim, "claim", false, "take the oldest visible item")
	skillQueueCmd.Flags().StringVar(&skillQueueWait, "wait", "", "with --claim, poll for this long before reporting empty (e.g. 30s)")
	skillsCmd.AddCommand(skillQueueCmd)
}
