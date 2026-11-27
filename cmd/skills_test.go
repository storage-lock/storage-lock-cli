package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	storage_lock "github.com/storage-lock/go-storage-lock"
)

func TestParseTTL(t *testing.T) {
	cases := []struct {
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{raw: "30s", want: 30 * time.Second},
		{raw: "3s", want: 3 * time.Second},
		{raw: "1m", want: time.Minute},
		{raw: "2s", wantErr: true},
		{raw: "0s", wantErr: true},
		{raw: "-5s", wantErr: true},
		{raw: "abc", wantErr: true},
	}
	for _, c := range cases {
		got, err := parseTTL(c.raw)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseTTL(%q) 期望报错，实际返回 %v", c.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseTTL(%q) 意外报错: %v", c.raw, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseTTL(%q) = %v, 期望 %v", c.raw, got, c.want)
		}
	}
}

func TestIsContended(t *testing.T) {
	contended := []error{
		storage_lock.ErrLockBusy,
		storage_lock.ErrLockAlreadyExists,
		storage_lock.ErrVersionMiss,
		storage_lock.ErrNotYourTurn,
		storage_lock.ErrMaxReentrantDepth,
		storage_lock.ErrUpgradeConflict,
	}
	for _, err := range contended {
		if !isContended(err) {
			t.Errorf("isContended(%v) = false, 期望 true", err)
		}
	}
	if isContended(errors.New("some storage failure")) {
		t.Error("普通存储错误不应被判定为竞争")
	}
	if isContended(nil) {
		t.Error("nil 不应被判定为竞争")
	}
}

// TestSkillLockOptionsWatchdogMargin 校验看门狗模式下续租间隔始终给租约留足余量，
// 与 storage_lock.checkStorageLockOptions 的约束一致：
// LeaseExpireAfter - LeaseRefreshInterval >= max(1s, LeaseExpireAfter/3)。
func TestSkillLockOptionsWatchdogMargin(t *testing.T) {
	ttls := []time.Duration{
		3 * time.Second, 4 * time.Second, 5 * time.Second, 10 * time.Second,
		30 * time.Second, time.Minute, time.Hour,
	}
	for _, ttl := range ttls {
		options := skillLockOptions("probe", ttl, true)
		if options.LockId != "probe" {
			t.Errorf("ttl=%v: LockId 未被设置", ttl)
		}
		if options.LeaseExpireAfter != ttl {
			t.Errorf("ttl=%v: LeaseExpireAfter = %v", ttl, options.LeaseExpireAfter)
		}
		if options.DisableWatchDog {
			t.Errorf("ttl=%v: 看门狗模式却禁用了看门狗", ttl)
		}
		margin := ttl - options.LeaseRefreshInterval
		required := ttl / 3
		if required < time.Second {
			required = time.Second
		}
		if options.LeaseRefreshInterval <= 0 {
			t.Errorf("ttl=%v: LeaseRefreshInterval 必须为正", ttl)
		}
		if options.LeaseRefreshInterval >= ttl {
			t.Errorf("ttl=%v: LeaseRefreshInterval(%v) 不应大于等于租约", ttl, options.LeaseRefreshInterval)
		}
		if margin < required {
			t.Errorf("ttl=%v: 续租余量 %v 小于要求的 %v", ttl, margin, required)
		}
	}

	// 无看门狗模式：租约严格等于 ttl，续租间隔保持默认值且不参与运行。
	for _, ttl := range ttls {
		options := skillLockOptions("probe", ttl, false)
		if !options.DisableWatchDog {
			t.Errorf("ttl=%v: 期望禁用看门狗", ttl)
		}
		if options.LeaseExpireAfter != ttl {
			t.Errorf("ttl=%v: LeaseExpireAfter = %v", ttl, options.LeaseExpireAfter)
		}
		if options.LeaseRefreshInterval != storage_lock.DefaultLeaseRefreshInterval {
			t.Errorf("ttl=%v: 无看门狗时续租间隔应保持默认 %v，实际 %v",
				ttl, storage_lock.DefaultLeaseRefreshInterval, options.LeaseRefreshInterval)
		}
	}
}

// TestEmitContract 校验所有 Agent 侧命令依赖的输出契约：
// 成功时 success=true 且无 error 字段，失败时 success=false 且带 error。
func TestEmitContract(t *testing.T) {
	run := func(fields skillResult, err error) map[string]any {
		old := os.Stdout
		r, w, pipeErr := os.Pipe()
		if pipeErr != nil {
			t.Fatalf("创建管道失败: %v", pipeErr)
		}
		os.Stdout = w
		emitErr := emit(fields, err)
		_ = w.Close()
		os.Stdout = old
		data, readErr := io.ReadAll(r)
		if readErr != nil {
			t.Fatalf("读取输出失败: %v", readErr)
		}
		if err != nil && !errors.Is(emitErr, err) {
			t.Fatalf("emit 应原样返回错误，实际 %v", emitErr)
		}
		var decoded map[string]any
		if unmarshalErr := json.Unmarshal(data, &decoded); unmarshalErr != nil {
			t.Fatalf("输出不是合法 JSON: %v (%q)", unmarshalErr, string(data))
		}
		return decoded
	}

	success := run(skillResult{Status: "held", LockID: "x", OwnerID: "o", FencingToken: 7}, nil)
	if success["success"] != true {
		t.Errorf("成功结果 success 应为 true，实际 %v", success["success"])
	}
	if _, exists := success["error"]; exists {
		t.Error("成功结果不应包含 error 字段")
	}
	if success["status"] != "held" || success["lock_id"] != "x" || success["owner_id"] != "o" {
		t.Errorf("成功结果字段不符合契约: %v", success)
	}
	if success["fencing_token"] != float64(7) {
		t.Errorf("fencing_token = %v, 期望 7", success["fencing_token"])
	}

	busy := run(skillResult{Status: "busy", LockID: "x"}, storage_lock.ErrLockBusy)
	if busy["success"] != false {
		t.Errorf("竞争结果 success 应为 false，实际 %v", busy["success"])
	}
	if busy["status"] != "busy" {
		t.Errorf("status = %v, 期望 busy", busy["status"])
	}
	if busy["error"] == "" {
		t.Error("失败结果必须带 error 文本")
	}
}

// TestKvFlags 验证 --meta 的解析：引号容易出错的形状都要能给出可读错误。
func TestKvFlags(t *testing.T) {
	got, err := kvFlags([]string{"task=42", "repo=api", "note=a=b"})
	if err != nil {
		t.Fatalf("合法输入不应报错: %v", err)
	}
	if got["task"] != "42" || got["repo"] != "api" || got["note"] != "a=b" {
		t.Fatalf("解析结果不对: %v", got)
	}
	if _, err := kvFlags([]string{"no-equals"}); err == nil {
		t.Fatal("缺少 = 应报错")
	}
	if _, err := kvFlags([]string{"=value"}); err == nil {
		t.Fatal("空 key 应报错")
	}
}

// TestParseCAS 验证 --cas 的 EXPECT=NEW 解析。
func TestParseCAS(t *testing.T) {
	expect, next, err := parseCAS("7=8")
	if err != nil || expect != 7 || next != 8 {
		t.Fatalf("parseCAS(7=8) = %d,%d,%v", expect, next, err)
	}
	expect, next, err = parseCAS("-3=0")
	if err != nil || expect != -3 || next != 0 {
		t.Fatalf("负数应支持: %d,%d,%v", expect, next, err)
	}
	for _, bad := range []string{"7", "a=8", "7=b", "5=5"} {
		if _, _, err := parseCAS(bad); err == nil {
			t.Fatalf("parseCAS(%q) 应报错", bad)
		}
	}
}

// TestWaitDuration 验证"等待时长"可以为 0 且不接受负数。
func TestWaitDuration(t *testing.T) {
	if d, err := waitDuration(""); err != nil || d != 0 {
		t.Fatalf("空串应返回 0, nil，得到 %v, %v", d, err)
	}
	if d, err := waitDuration("200ms"); err != nil || d != 200*time.Millisecond {
		t.Fatalf("短时长应被接受（与 TTL 的 3s 下限无关），得到 %v, %v", d, err)
	}
	if _, err := waitDuration("-1s"); err == nil {
		t.Fatal("负时长应报错")
	}
}

// TestOwnerOrDefault 保证生成的 owner 非空且每次都不同——
// owner 是后续 ack/unlock 的唯一凭据，重复会让两个 Agent 争抢同一份执行权。
func TestOwnerOrDefault(t *testing.T) {
	if ownerOrDefault("mine") != "mine" {
		t.Fatal("显式 owner 不应被改写")
	}
	a, b := ownerOrDefault(""), ownerOrDefault("")
	if a == "" || b == "" {
		t.Fatal("应生成非空 owner")
	}
	if a == b {
		t.Fatal("两次生成不应相同")
	}
}

// TestLockMode 保证读写模式字符串稳定：Agent 按 mode 分支，不能中途改名。
func TestLockMode(t *testing.T) {
	if lockMode(true) != "read" || lockMode(false) != "write" {
		t.Fatal("mode 字符串必须稳定为 read/write")
	}
}
