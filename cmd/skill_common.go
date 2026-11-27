package cmd

// Agent 侧 skills 命令的公共基础件。
//
// 这里的每个函数都服务于同一件事：让 Agent 能从**一行 JSON** 里拿到足够的信息
// 决定下一步，而不需要再发一条查询命令。
//
// 三条硬约定：
//  1. 所有 skills 子命令只往 stdout 写一个 JSON 对象（由 emit 保证）；
//  2. "竞争/被别人拿着"是正常分支（success:false + status:"busy"），不是故障；
//  3. 需要外部状态（owner、token、slot）才能继续的操作，第一次调用必须把状态
//     返回给调用方——Agent 没有跨命令的进程内记忆。

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	storage "github.com/storage-lock/go-storage"
	storage_lock "github.com/storage-lock/go-storage-lock"
)

// kvFlags 把 "k=v" 形式的 --meta 参数解析成有序 map。
//
// 用途：Agent 常需要给锁附加业务上下文（任务 ID、仓库名、CI 编号），
// 这些信息随锁记录原子保存，之后 `skills status` / 观测方都能看到。
// 用重复的 --meta 而不是 JSON 字符串，是因为 shell 里拼 JSON 引号很容易出错，
// 而 Agent 最怕的就是"命令因为引号被拆坏、报了一个看不懂的错误"。
func kvFlags(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for _, item := range raw {
		k, v, ok := strings.Cut(item, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, errors.New("--meta must be key=value, got " + strconv.Quote(item))
		}
		out[k] = v
	}
	return out, nil
}

// metaSlice 把 map 还原成 "k=v" 切片（便于输出与测试排序稳定）。
func metaSlice(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// acquireMode 决定"拿不到锁"时是立刻返回还是等待。
type acquireMode int

const (
	// tryOnce 只试一次：拿不到就是 busy。
	tryOnce acquireMode = iota
	// waitForever 阻塞到拿到或 ctx 结束（--wait）。
	waitForever
)

// lockWithMode 按模式获取锁。
//
// 单独抽出来是因为 7 个命令里有一半需要"要么阻塞要么立刻返回"的同一套分支，
// 分散写会漂移出不同的状态字符串——而状态字符串就是 Agent 的分支依据。
func lockWithMode(ctx context.Context, lock *storage_lock.StorageLock, owner string, mode acquireMode) error {
	if mode == waitForever {
		return lock.Lock(ctx, owner)
	}
	return lock.TryLock(ctx, owner)
}

// contendedResult 把"没拿到锁"翻译成 Agent 能直接用的结果。
//
// 返回的 status 固定为 busy / lost 两种：前者是别人拿着，后者是自己曾经持有但已经丢了。
// 这两种对 Agent 的下一步完全不同（等 vs 重新走一遍流程），不能合并。
func contendedResult(fields skillResult, err error) skillResult {
	if fields.Status == "" {
		fields.Status = "busy"
	}
	if errors.Is(err, storage_lock.ErrLeaseExpired) {
		fields.Status = "lost"
	}
	// 已经在任何客户端上持有（不同 owner）；把它翻成可读原因，
	// 但 success 仍为 false——错误文本只是补充信息。
	fields.Message = contentionMessage(err)
	return fields
}

// contentionMessage 给出"为什么没拿到"，不带堆栈、不带哨兵错误名。
func contentionMessage(err error) string {
	switch {
	case errors.Is(err, storage_lock.ErrLockBusy),
		errors.Is(err, storage_lock.ErrLockAlreadyExists):
		return "held by another owner"
	case errors.Is(err, storage_lock.ErrVersionMiss):
		return "lost a race for the lock; retry or wait"
	case errors.Is(err, storage_lock.ErrNotYourTurn):
		return "another waiter is ahead of you in the fair queue"
	case errors.Is(err, storage_lock.ErrMaxReentrantDepth):
		return "reentrant depth limit reached"
	case errors.Is(err, storage_lock.ErrLeaseExpired):
		return "lease expired"
	default:
		return ""
	}
}

// lockSnapshot 是 skills status 的完整视图。
//
// 与 go-storage-lock 的 LockStatus 相比多做了两件事：
//   - 时间全部转成便于解析的字符串（RFC3339 与 duration），不输出 Go 的 time.Time 结构；
//   - 补齐 Agent 真正需要的"我还能不能继续"判断所需字段（held / remaining_lease / waiters）。
func lockSnapshot(status *storage_lock.LockStatus, token storage.Version) lockView {
	info := status.Information
	view := lockView{
		LockID:       info.LockId,
		OwnerID:      info.OwnerId,
		Version:      info.Version,
		LockCount:    info.LockCount,
		FencingToken: token,
		ReadLock:     info.ReadLock,
		Waiters:      len(status.Waiters),
		// 时间用服务端统一时钟算出的剩余租期，而不是本机时钟：
		// 多台机器各算各的会导致对"是否已过期"判断不一致。
		RemainingLease:  status.RemainingLease.Round(time.Millisecond).String(),
		LockBeginTime:   info.LockBeginTime.UTC().Format(time.RFC3339Nano),
		LeaseExpireTime: info.LeaseExpireTime.UTC().Format(time.RFC3339Nano),
		Extra:           metaSlice(info.Extra),
	}
	if status.Held {
		view.Status = "held"
	} else {
		// 已释放的墓碑记录仍然保留（fencing token 不能回退），
		// 所以"存在记录"不等于"被持有"。
		view.Status = "free"
		view.OwnerID = ""
		view.FencingToken = 0
	}
	if status.WriteIntentOwner != "" {
		view.WriteIntentOwner = status.WriteIntentOwner
		view.WriteIntentAge = status.WriteIntentAge.Round(time.Millisecond).String()
	}
	return view
}

// lockView 是 skills 里"锁的完整视图"的 JSON 形态。
//
// 字段刻意用扁平结构：Agent 解析 `data.remaining_lease` 比解析嵌套对象可靠得多，
// 而且扁平结构在 --output=table 时也更好读。
type lockView struct {
	LockID           string          `json:"lock_id"`
	OwnerID          string          `json:"owner_id,omitempty"`
	Version          storage.Version `json:"version"`
	LockCount        int             `json:"lock_count"`
	FencingToken     storage.Version `json:"fencing_token,omitempty"`
	ReadLock         bool            `json:"read_lock,omitempty"`
	Status           string          `json:"status"`
	RemainingLease   string          `json:"remaining_lease,omitempty"`
	LockBeginTime    string          `json:"lock_begin_time,omitempty"`
	LeaseExpireTime  string          `json:"lease_expire_time,omitempty"`
	Waiters          int             `json:"waiters,omitempty"`
	WriteIntentOwner string          `json:"write_intent_owner,omitempty"`
	WriteIntentAge   string          `json:"write_intent_age,omitempty"`
	Extra            []string        `json:"meta,omitempty"`
}

// inspected 拿一把锁的完整视图，并把"记录不存在"翻译成 free。
//
// owner 是调用方自报的持有者：只有它等于记录里的 OwnerId 时才查 fencing token
// （令牌按 owner 校验，查别人的锁会返回错误而不是"顺手看到"）。
func inspected(ctx context.Context, lock *storage_lock.StorageLock, lockID, owner string) (lockView, error) {
	status, err := lock.Inspect(ctx)
	if errors.Is(err, storage_lock.ErrLockNotFound) {
		return lockView{LockID: lockID, Status: "free"}, nil
	}
	if err != nil {
		return lockView{}, err
	}
	var token storage.Version
	if status.Held {
		// 令牌只对持有者有意义；拿不到（例如查的是别人的锁）就留空。
		if owner == status.Information.OwnerId {
			if t, err := lock.GetFencingToken(ctx, owner); err == nil {
				token = t
			}
		} else if t, err := lock.GetFencingToken(ctx, status.Information.OwnerId); err == nil {
			token = t
		}
	}
	return lockSnapshot(status, token), nil
}

// ownerOrDefault 返回非空的 owner：Agent 不传时生成一个唯一值。
//
// 统一在这里生成而不是每个命令各写一遍：owner 是后续 ack/unlock 的唯一凭据，
// 生成规则漂移会让"返回的 owner 和存储里的 owner 对不上"。
func ownerOrDefault(owner string) string {
	if owner != "" {
		return owner
	}
	return "agent/" + uuid.NewString()
}

// parseCAS 解析 --cas 的 EXPECT=NEW 形式。
//
// 用 "=" 而不是 ":"：值可能为负数，":" 在 shell 里没歧义但 "=" 更符合
// 比较的直觉，且 Agent 生成参数时不易写错。
func parseCAS(raw string) (expect, next int64, err error) {
	left, right, ok := strings.Cut(raw, "=")
	if !ok {
		return 0, 0, errors.New("--cas must be EXPECT=NEW, e.g. --cas=7=8")
	}
	if expect, err = strconv.ParseInt(strings.TrimSpace(left), 10, 64); err != nil {
		return 0, 0, errors.New("--cas EXPECT is not an integer: " + strconv.Quote(left))
	}
	if next, err = strconv.ParseInt(strings.TrimSpace(right), 10, 64); err != nil {
		return 0, 0, errors.New("--cas NEW is not an integer: " + strconv.Quote(right))
	}
	if expect == next {
		return 0, 0, errors.New("--cas EXPECT and NEW must differ (otherwise the update is a no-op)")
	}
	return expect, next, nil
}

// itoa 是 strconv.FormatInt 的短别名，减少格式化处的噪音。
func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// waitDuration 解析可选的等待时长；空串表示不等待。
//
// 与 parseTTL 分开：TTL 有 3 秒下限（存储层的租约约束），
// 而"等多久拿令牌"完全是调用方的选择，可以短于一秒。
func waitDuration(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, errors.New("invalid duration " + strconv.Quote(raw))
	}
	if d < 0 {
		return 0, errors.New("duration must not be negative")
	}
	return d, nil
}

// lockMode 把 ReadLock 标记翻译成 Agent 可读的模式名。
func lockMode(read bool) string {
	if read {
		return rwModeRead
	}
	return rwModeWrite
}

// statusMessage 给一行可读的解释，避免 Agent 只看到 status 猜上下文。
func statusMessage(v lockView) string {
	switch v.Status {
	case "held":
		if v.ReadLock {
			return "shared lock, " + itoaCount(v.LockCount) + " reader(s)"
		}
		return "exclusive lock held"
	case "free":
		return "not held right now (released tombstone or never acquired)"
	default:
		return ""
	}
}

func itoaCount(n int) string { return strconv.Itoa(n) }

// skillLockWithFair 构造锁，可选开启跨进程公平排队。
//
// Fair 会让等待者按登记顺序写进同一份锁记录，多进程共用一条存储就能排出同一顺序；
// 代价是每次获取多一次 CAS。默认关闭是因为大多数 Agent 场景并不需要严格 FIFO，
// 而多出来的那次写会直接体现在延迟上。
func skillLockWithFair(lockID string, ttl time.Duration, watchdog bool, fair bool) (*storage_lock.StorageLock, error) {
	options := skillLockOptions(lockID, ttl, watchdog)
	options.Fair = fair
	return storage_lock.NewStorageLockWithOptions(rootStorage, options)
}
