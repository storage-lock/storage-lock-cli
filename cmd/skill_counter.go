package cmd

// 分布式计数器：Agent 之间共享的原子计数。
//
// 典型用途：编号分配、配额消耗、轮询下标、CAS 保护的状态机。

import (
	"errors"

	"github.com/spf13/cobra"
	coordination "github.com/storage-lock/go-storage-lock-coordination"
)

var (
	skillCounterID     string
	skillCounterAdd    int64
	skillCounterSet    int64
	skillCounterCAS    string
	skillCounterReset  bool
	skillCounterGetOld bool
)

var skillCounterCmd = &cobra.Command{
	Use:   "counter --id NS [--add N | --set N | --cas EXPECT=NEW | --reset | (no flag = read)]",
	Short: "distributed atomic counter shared across agents",
	Long: `Read and modify a cluster-wide atomic counter.

  (no modifier)        read the current value
  --add N              atomically add N (negative subtracts); prints the new value
  --add N --get-old    add N and print the value BEFORE the change
  --set N              set the value unconditionally
  --cas EXPECT=NEW     compare-and-set: only succeeds if the current value is EXPECT
  --reset              set to 0

Status on success is "value"; --cas reports "cas_ok" / "cas_miss".
A cas_miss is a normal race outcome (someone changed it first), not an error.

Examples:
  storage-lock-cli --storage=cloud skills counter --id=order-seq --add=1
  storage-lock-cli --storage=cloud skills counter --id=deploy-gen --cas=7=8`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if skillCounterID == "" {
			return emit(skillResult{}, errors.New("--id is required"))
		}
		counter, err := coordination.NewAtomicLong(rootStorage, skillCounterID)
		if err != nil {
			return emit(skillResult{LockID: skillCounterID}, err)
		}
		ctx, cancel := skillContext(cmd)
		defer cancel()

		switch {
		case skillCounterReset:
			if err := counter.Reset(ctx); err != nil {
				return emit(skillResult{LockID: skillCounterID}, err)
			}
			return emit(skillResult{LockID: skillCounterID, Value: 0, Status: "value",
				Message: "counter reset"}, nil)

		case skillCounterCAS != "":
			expect, next, err := parseCAS(skillCounterCAS)
			if err != nil {
				return emit(skillResult{LockID: skillCounterID}, err)
			}
			ok, err := counter.CompareAndSet(ctx, expect, next)
			if err != nil {
				return emit(skillResult{LockID: skillCounterID}, err)
			}
			status := "cas_miss"
			msg := "current value was not " + itoa(expect)
			if ok {
				status = "cas_ok"
				msg = "updated"
			}
			return emit(skillResult{LockID: skillCounterID, Value: next, SetResult: ok,
				Status: status, Message: msg}, nil)

		case cmd.Flags().Changed("set"):
			if err := counter.Set(ctx, skillCounterSet); err != nil {
				return emit(skillResult{LockID: skillCounterID}, err)
			}
			return emit(skillResult{LockID: skillCounterID, Value: skillCounterSet, Status: "value"}, nil)

		case cmd.Flags().Changed("add"):
			var got int64
			var err error
			if skillCounterGetOld {
				got, err = counter.GetAndAdd(ctx, skillCounterAdd)
			} else {
				got, err = counter.AddAndGet(ctx, skillCounterAdd)
			}
			if err != nil {
				return emit(skillResult{LockID: skillCounterID}, err)
			}
			return emit(skillResult{LockID: skillCounterID, Value: got, Status: "value"}, nil)

		default:
			value, err := counter.Get(ctx)
			if err != nil {
				// 计数器从未被写过时底层记录不存在；对调用方而言这就是 0。
				// 计数器从未被写过时底层返回 Not Found；对调用方而言就是 0。
				return emit(skillResult{LockID: skillCounterID}, err)
			}
			return emit(skillResult{LockID: skillCounterID, Value: value, Status: "value"}, nil)
		}
	},
}

func init() {
	skillCounterCmd.Flags().StringVar(&skillCounterID, "id", "", "counter namespace, shared by all agents (required)")
	skillCounterCmd.Flags().Int64Var(&skillCounterAdd, "add", 0, "atomically add N (negative subtracts)")
	skillCounterCmd.Flags().BoolVar(&skillCounterGetOld, "get-old", false, "with --add, print the value before the change")
	skillCounterCmd.Flags().Int64Var(&skillCounterSet, "set", 0, "set the counter to N unconditionally")
	skillCounterCmd.Flags().StringVar(&skillCounterCAS, "cas", "", "compare-and-set, as EXPECT=NEW")
	skillCounterCmd.Flags().BoolVar(&skillCounterReset, "reset", false, "set the counter to 0")
	skillsCmd.AddCommand(skillCounterCmd)
}
