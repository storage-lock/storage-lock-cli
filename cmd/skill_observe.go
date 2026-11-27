package cmd

// observe：一次性拿到整个命名空间的锁视图。
//
// Agent 在动手前经常需要回答"现在集群里在跑什么"：
// 有没有别人占着我要的资源、有没有僵尸锁、队列里堆了多少。
// 用 list 也行，但 list 是给人看的表格/全量 JSON；observe 是给 Agent 的分组摘要，
// 并且**只依赖一次 List**，不会因为逐把查状态而放大成 N 次往返。

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	storage_lock "github.com/storage-lock/go-storage-lock"
)

var (
	skillObservePrefix string
	skillObserveFilter string
	skillObserveLimit  int
	skillObserveStale  string
)

var skillObserveCmd = &cobra.Command{
	Use:   "observe [--prefix P] [--filter held|free] [--stale DURATION] [--limit N]",
	Short: "one-shot snapshot of all locks, grouped and summarised for agents",
	Long: `Summarise every lock record visible to this storage in a single pass.

  --prefix P        only locks whose id starts with P (e.g. queue-jobs-)
  --filter held     only currently held locks
  --filter free     only released tombstones
  --stale 10m       additionally report locks whose lease expired long ago
                    (zombies: owner died without releasing)
  --limit N         cap the item list (summary counts are always exact)

Output fields:
  total / held / free   counts across the whole prefix
  stale                 records held-forever-but-expired (needs manual cleanup)
  items                 per-lock rows with owner, remaining lease, write intent

This replaces a chain of "list then status-per-lock" calls: one List round trip,
no per-lock queries.

Examples:
  storage-lock-cli --storage=cloud skills observe
  storage-lock-cli --storage=cloud skills observe --filter=held --prefix=queue-jobs-`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := skillContext(cmd)
		defer cancel()

		iter, err := storage_lock.ListLocks(ctx, rootStorage, skillObservePrefix)
		if err != nil {
			return emit(skillResult{}, err)
		}

		staleAfter := time.Duration(0)
		if skillObserveStale != "" {
			if staleAfter, err = waitDuration(skillObserveStale); err != nil {
				return emit(skillResult{}, err)
			}
		}

		// 用服务端时间做基准：本机时钟偏移会让"已过期"的判断在多机之间不一致。
		now, err := rootStorage.GetTime(ctx)
		if err != nil {
			return emit(skillResult{}, err)
		}

		var (
			total, held, free, stale int
			items                    []lockView
		)
		for iter.Next() {
			info := iter.Value()
			if info == nil {
				continue
			}
			total++
			isHeld := info.LockCount > 0 && now.Before(info.LeaseExpireTime)
			if isHeld {
				held++
			} else {
				free++
			}
			if staleAfter > 0 && !isHeld && !info.LeaseExpireTime.IsZero() &&
				now.Sub(info.LeaseExpireTime) >= staleAfter {
				stale++
			}
			switch skillObserveFilter {
			case "held":
				if !isHeld {
					continue
				}
			case "free":
				if isHeld {
					continue
				}
			case "":
			default:
				return emit(skillResult{}, errBadFilter(skillObserveFilter))
			}
			if skillObserveLimit > 0 && len(items) >= skillObserveLimit {
				continue
			}
			view := lockView{
				LockID:           info.LockId,
				OwnerID:          info.OwnerId,
				Version:          info.Version,
				LockCount:        info.LockCount,
				ReadLock:         info.ReadLock,
				Waiters:          len(info.Waiters),
				LockBeginTime:    info.LockBeginTime.UTC().Format(time.RFC3339Nano),
				LeaseExpireTime:  info.LeaseExpireTime.UTC().Format(time.RFC3339Nano),
				Extra:            metaSlice(info.Extra),
				WriteIntentOwner: info.WriteIntentOwner,
			}
			if isHeld {
				view.Status = "held"
				view.RemainingLease = info.LeaseExpireTime.Sub(now).Round(time.Millisecond).String()
			} else {
				view.Status = "free"
				view.OwnerID = ""
			}
			items = append(items, view)
		}
		// 稳定的排序：Agent 反复 observe 时能直接 diff，而不是被随机顺序淹没。
		sort.Slice(items, func(i, j int) bool { return items[i].LockID < items[j].LockID })
		return emit(skillResult{
			Status:  "snapshot",
			Total:   total,
			Held:    held,
			Free:    free,
			Stale:   stale,
			Locks:   items,
			Message: observeSummary(total, held, stale),
		}, nil)
	},
}

func observeSummary(total, held, stale int) string {
	var b strings.Builder
	b.WriteString(itoaCount(total))
	b.WriteString(" record(s), ")
	b.WriteString(itoaCount(held))
	b.WriteString(" held")
	if stale > 0 {
		b.WriteString(", ")
		b.WriteString(itoaCount(stale))
		b.WriteString(" stale")
	}
	return b.String()
}

func errBadFilter(v string) error {
	return errors.New("--filter must be held or free, got " + strconv.Quote(v))
}

func init() {
	skillObserveCmd.Flags().StringVar(&skillObservePrefix, "prefix", "", "only locks whose id starts with this prefix")
	skillObserveCmd.Flags().StringVar(&skillObserveFilter, "filter", "", "held or free")
	skillObserveCmd.Flags().IntVar(&skillObserveLimit, "limit", 100, "cap the item list (0 = unlimited); counts stay exact")
	skillObserveCmd.Flags().StringVar(&skillObserveStale, "stale", "",
		"report locks whose lease expired longer ago than this (e.g. 10m)")
	skillsCmd.AddCommand(skillObserveCmd)
}
