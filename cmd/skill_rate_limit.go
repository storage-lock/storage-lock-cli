package cmd

// 分布式令牌桶限流：控制多个 Agent 对**外部**资源的总调用速率。
//
// 与 semaphore 的区别：semaphore 限制"同时在跑几个"（并发度），
// rate limit 限制"每秒能做几次"（速率）。Agent 批量调用第三方 API 时，
// 后者才是真正会被封号的那条线。

import (
	"context"
	"errors"
	"time"

	"github.com/spf13/cobra"
	coordination "github.com/storage-lock/go-storage-lock-coordination"
)

var (
	skillRateID     string
	skillRateRPS    int64
	skillRateBurst  int64
	skillRateTokens bool
	skillRateRefill int64
	skillRateTake   int64
	skillRateWait   string
)

var skillRateLimitCmd = &cobra.Command{
	Use:   "rate-limit --id NS --rps N [--burst N] [--take N] [--wait DURATION | --tokens | --refill N]",
	Short: "distributed token-bucket rate limiter shared by all agents",
	Long: `Keep the cluster-wide call rate under N/s when hitting an external API.

  --take N (default 1)   consume N tokens; if not enough, report denied
  --wait DURATION        instead of failing, poll until tokens are available
  --tokens               read how many tokens are available right now
  --refill N             manually add N tokens (use with --rps=0 manual mode)

Status values:
  allowed     tokens consumed; proceed
  denied      not enough tokens; retry later (normal, not an error)
  tokens      current token count
  refilled    manual refill done

IMPORTANT: --rps and --burst are a deployment contract. Every agent using the
same namespace must pass the same values — the bucket is one shared record,
and a mismatched burst silently changes the effective limit.

Examples:
  storage-lock-cli --storage=cloud skills rate-limit --id=github-api --rps=10 --take=1
  storage-lock-cli --storage=cloud skills rate-limit --id=github-api --rps=10 --wait=30s
  storage-lock-cli --storage=cloud skills rate-limit --id=github-api --rps=10 --tokens`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if skillRateID == "" {
			return emit(skillResult{}, errors.New("--id is required"))
		}
		// 手动模式（--rps=0）只在显式 refill/tokens 时有意义；其他情况 rps 必须 > 0。
		if skillRateRPS <= 0 && !skillRateTokens && skillRateRefill <= 0 {
			return emit(skillResult{LockID: skillRateID},
				errors.New("--rps must be > 0 (use --tokens or --refill with --rps=0 for manual mode)"))
		}
		limiter, err := coordination.NewRateLimiter(rootStorage, skillRateID, coordination.RateLimiterOptions{
			TokensPerSecond: skillRateRPS,
			BurstCapacity:   skillRateBurst,
		})
		if err != nil {
			return emit(skillResult{LockID: skillRateID}, err)
		}
		ctx, cancel := skillContext(cmd)
		defer cancel()

		switch {
		case skillRateTokens:
			n, err := limiter.Tokens(ctx)
			if err != nil {
				return emit(skillResult{LockID: skillRateID}, err)
			}
			return emit(skillResult{LockID: skillRateID, Tokens: n, Value: n, Status: "tokens"}, nil)

		case skillRateRefill > 0:
			if err := limiter.Refill(ctx, skillRateRefill); err != nil {
				return emit(skillResult{LockID: skillRateID}, err)
			}
			n, _ := limiter.Tokens(ctx)
			return emit(skillResult{LockID: skillRateID, Tokens: n, Value: n, Status: "refilled"}, nil)

		default:
			return takeTokens(cmd, ctx, limiter)
		}
	},
}

// takeTokens 消耗令牌；--wait 时按短间隔轮询直到拿够或超时。
func takeTokens(cmd *cobra.Command, ctx context.Context, limiter *coordination.RateLimiter) error {
	take := skillRateTake
	if take <= 0 {
		take = 1
	}
	wait, err := waitDuration(skillRateWait)
	if err != nil {
		return emit(skillResult{LockID: skillRateID}, err)
	}
	deadline := time.Now().Add(wait)
	for {
		ok, err := limiter.TryAcquire(ctx, take)
		if err != nil {
			// 令牌不足是正常分支（denied），不是故障——否则 Agent 会把"限流生效"
			// 当成基础设施出错而中断整个任务。
			if coordination.IsNoTokens(err) {
				ok = false
			} else {
				return emit(skillResult{LockID: skillRateID}, err)
			}
		}
		if ok {
			n, _ := limiter.Tokens(ctx)
			return emit(skillResult{
				LockID: skillRateID, Status: "allowed", Tokens: n, Value: n,
				Message: "you may proceed",
			}, nil)
		}
		if wait <= 0 || time.Now().After(deadline) {
			n, _ := limiter.Tokens(ctx)
			return emit(skillResult{
				LockID: skillRateID, Status: "denied", Tokens: n, Value: n,
				Message: "not enough tokens; retry later or pass --wait",
			}, nil)
		}
		// 轮询间隔固定 200ms：令牌桶的补充粒度是秒，更密的轮询只会打爆共享锁服务。
		select {
		case <-ctx.Done():
			return emit(skillResult{LockID: skillRateID, Status: "denied"}, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func init() {
	skillRateLimitCmd.Flags().StringVar(&skillRateID, "id", "", "limiter namespace, shared by all agents (required)")
	skillRateLimitCmd.Flags().Int64Var(&skillRateRPS, "rps", 0, "tokens added per second (deployment contract)")
	skillRateLimitCmd.Flags().Int64Var(&skillRateBurst, "burst", 0, "max bucket size; defaults to --rps")
	skillRateLimitCmd.Flags().Int64Var(&skillRateTake, "take", 1, "tokens to consume per call")
	skillRateLimitCmd.Flags().StringVar(&skillRateWait, "wait", "", "poll for up to this duration instead of failing immediately")
	skillRateLimitCmd.Flags().BoolVar(&skillRateTokens, "tokens", false, "report available tokens without consuming")
	skillRateLimitCmd.Flags().Int64Var(&skillRateRefill, "refill", 0, "manually add N tokens (manual mode)")
	skillsCmd.AddCommand(skillRateLimitCmd)
}
