package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	storage_lock "github.com/storage-lock/go-storage-lock"
)

var (
	skillWithLockID    string
	skillWithLockTTL   string
	skillWithLockOwner string
)

// with-lock 在持有锁期间运行一个子进程，子进程退出后释放锁。
// 与 lock 命令不同，这里打开看门狗：只要 CLI 进程还活着，租约就会被续期，
// 所以任务时长可以远超 --ttl；真正打扰长时间任务的是进程被杀死或网络故障。
var skillWithLockCmd = &cobra.Command{
	Use:   "with-lock --id ID [--ttl DURATION] [--owner ID] -- COMMAND [ARG...]",
	Short: "run a child process while holding a lease lock, release afterwards",
	Long: `Acquire the lock, then run COMMAND with the remaining arguments.
The lease is renewed by a watchdog while COMMAND runs, so COMMAND may take
longer than --ttl. On COMMAND exit (0 or non-zero), on SIGINT/SIGTERM, the
lock is released best-effort and the child's own exit code is returned.

Examples:
  storage-lock-cli --storage=... --dsn=... skills with-lock --id=deploy -- make deploy
  storage-lock-cli --storage=... --dsn=... skills with-lock --id=migrate -- python migrate.py`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if skillWithLockID == "" {
			return emit(skillResult{}, errors.New("--id is required"))
		}
		ttl, err := parseTTL(skillWithLockTTL)
		if err != nil {
			return emit(skillResult{LockID: skillWithLockID}, err)
		}
		lock, err := skillLock(skillWithLockID, ttl, true)
		if err != nil {
			return emit(skillResult{LockID: skillWithLockID}, err)
		}
		owner := skillWithLockOwner
		if owner == "" {
			owner = "with-lock/" + uuid.NewString()
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		acquireCtx, acquireCancel := context.WithTimeout(ctx, timeout)
		if err := lock.Lock(acquireCtx, owner); err != nil {
			// 必须先读 Err 再取消：取消之后 Err 永远是 context.Canceled，
			// 会把"锁被别人占着""存储报错"这些真实原因全部掩盖掉。
			ctxErr := acquireCtx.Err()
			acquireCancel()
			result := skillResult{LockID: skillWithLockID, OwnerID: owner}
			if ctxErr != nil {
				cancel()
				return emit(result, ctxErr)
			}
			if isContended(err) {
				result.Status = "busy"
			}
			return emit(result, err)
		}
		acquireCancel()

		defer func() {
			// 子进程已经退出、上下文可能已取消，用独立超时尽力释放。
			releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(context.Background()), storage_lock.DefaultUnlockTimeout)
			defer releaseCancel()
			if releaseErr := lock.UnLock(releaseCtx, owner); releaseErr != nil {
				_, _ = fmt.Fprintf(os.Stderr, "warn: release lock %s failed: %v\n", skillWithLockID, releaseErr)
			}
		}()

		token := mustFencingToken(ctx, lock, owner)

		child := exec.CommandContext(ctx, args[0], args[1:]...)
		child.Stdin = os.Stdin
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		runErr := child.Run()

		// 打印"开始执行"的提示到 stderr，避免污染 stdout 的 JSON 契约——读写锁的人都有这个习惯。
		result := skillResult{
			LockID:         skillWithLockID,
			OwnerID:        owner,
			FencingToken:   token,
			Command:        args[0],
			Status:         "executed",
			RemainingLease: remainingLease(ctx, lock, owner),
		}
		if runErr == nil {
			return emit(result, nil)
		}
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			// 子进程自己失败：报给调用方但不是"命令出错"，保留退出码，
			// 锁仍然被释放（defer 已兜底）。
			result.Status = "child_exited"
			result.Message = fmt.Sprintf("child exited with status %d", exitErr.ExitCode())
			_ = emit(result, nil)
			return exitCodeError{code: exitErr.ExitCode(), err: exitErr}
		}
		// 上下文取消（被信号打断）或启动失败。
		if ctx.Err() != nil {
			result.Status = "interrupted"
			_ = emit(result, nil)
			return ctx.Err()
		}
		return emit(result, runErr)
	},
}

// exitCodeError 携带"要以这个退出码退出"的语义，由 Execute 识别后透传给 shell。
type exitCodeError struct {
	code int
	err  error
}

func (e exitCodeError) Error() string { return e.err.Error() }

func (e exitCodeError) Unwrap() error { return e.err }

func init() {
	skillWithLockCmd.Flags().StringVar(&skillWithLockID, "id", "", "lock identifier (required)")
	skillWithLockCmd.Flags().StringVar(&skillWithLockTTL, "ttl", "30s", "lease duration, at least 3s")
	skillWithLockCmd.Flags().StringVar(&skillWithLockOwner, "owner", "", "owner id (generated when omitted)")
	skillsCmd.AddCommand(skillWithLockCmd)
}
