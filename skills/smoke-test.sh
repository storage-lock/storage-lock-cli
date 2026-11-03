#!/usr/bin/env bash
# storage-lock-cli skills 端到端冒烟测试。
#
# 需要真实可用的存储后端：默认 sl-mariadb（127.0.0.1:13309）。
#   CLI=./storage-lock-cli STORAGE=mariadb DSN='root:rootpass@tcp(127.0.0.1:13309)/storage_lock_test' ./skills/smoke-test.sh
#
# 覆盖：单 key 锁生命周期、多进程互斥、选主唯一性、信号量容量、with-lock 续租与退出码透传。
set -u

CLI=${CLI:-storage-lock-cli}
STORAGE=${STORAGE:-mariadb}
DSN=${DSN:-root:rootpass@tcp(127.0.0.1:13309)/storage_lock_test}
NS="smoke-$$-$(date +%s)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

pass=0
fail=0

ok()   { pass=$((pass + 1)); printf 'PASS  %s\n' "$1"; }
bad()  { fail=$((fail + 1)); printf 'FAIL  %s\n' "$1"; }
check_contains() { # $1=名称 $2=JSON $3=期望子串
  if printf '%s' "$2" | grep -qF -- "$3"; then ok "$1"; else bad "$1 (期望包含 $3，实际: $2)"; fi
}
check_eq() { # $1=名称 $2=实际 $3=期望
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (实际 $2，期望 $3)"; fi
}

run() { "$CLI" --storage="$STORAGE" --dsn="$DSN" --timeout=20s skills "$@"; }

echo "== 1. 单 key 锁生命周期"
out=$(run lock --id="$NS/l1" --ttl=10s --owner=bot-a 2>&1)
check_contains "lock 获取成功" "$out" '"status":"held"'
check_contains "lock 返回 owner" "$out" '"owner_id":"bot-a"'
out=$(run status --id="$NS/l1" 2>&1)
check_contains "status 为 held" "$out" '"status":"held"'
out=$(run renew --id="$NS/l1" --owner=bot-a --ttl=10s 2>&1)
check_contains "renew 成功" "$out" '"message":"lease renewed"'
out=$(run unlock --id="$NS/l1" --owner=bot-a 2>&1)
check_contains "unlock 成功" "$out" '"status":"released"'
out=$(run status --id="$NS/l1" 2>&1)
check_contains "释放后 status 为 free" "$out" '"status":"free"'

echo "== 2. 10 进程并发抢同一把锁，只应 1 个成功"
for i in $(seq 1 10); do
  run lock --id="$NS/l2" --ttl=6s --owner="w$i" >"$TMP/l2-$i.json" 2>&1 &
done
wait
held=$(grep -l '"status":"held"' "$TMP"/l2-*.json 2>/dev/null | wc -l)
busy=$(grep -l '"status":"busy"' "$TMP"/l2-*.json 2>/dev/null | wc -l)
check_eq "并发持有数" "$held" "1"
check_eq "并发竞争失败数" "$busy" "9"

echo "== 3a. 领导权持有时，其他人竞选必须是 follower（真互斥）"
run leader-election --id="$NS/e1" --ttl=12s --owner=holder --hold=4s >"$TMP/e1-holder.json" 2>&1 &
holder_pid=$!
sleep 1
out=$(run leader-election --id="$NS/e1" --ttl=12s --owner=challenger 2>&1)
check_contains "持有期间挑战者被拒" "$out" '"status":"follower"'
wait "$holder_pid"
out=$(run leader-election --id="$NS/e1" --ttl=12s --owner=challenger 2>&1)
check_contains "让出后挑战者当选" "$out" '"status":"leader"'

# one-shot 竞选拿到后立即让出，因此并发调用可能"接力"式地先后都报 leader。
# 这不是双持有：栅栏令牌必须互不相同（后一个严格更大），否则说明 CAS 失效。
echo "== 3b. one-shot 并发竞选：令牌互不相同，不存在双成功"
for i in $(seq 1 5); do
  run leader-election --id="$NS/e2" --ttl=12s --owner="d$i" >"$TMP/e2-$i.json" 2>&1 &
done
wait
tokens=$(grep -h '"status":"leader"' "$TMP"/e2-*.json 2>/dev/null | grep -o '"fencing_token":[0-9]*' | cut -d: -f2 | sort -n)
unique_tokens=$(printf '%s\n' "$tokens" | grep -c . )
distinct_tokens=$(printf '%s\n' "$tokens" | sort -u | grep -c .)
check_eq "leader 令牌互不相同" "$distinct_tokens" "$unique_tokens"
check_contains "至少产生 1 个 leader" "$(cat "$TMP"/e2-*.json)" '"status":"leader"'

echo "== 4. 信号量 capacity=2：2 个持有者占满后第 3 个被拒"
run semaphore --id="$NS/s1" --capacity=2 --ttl=10s --owner=h1 --hold=4s >"$TMP/s1-a.json" 2>&1 &
run semaphore --id="$NS/s1" --capacity=2 --ttl=10s --owner=h2 --hold=4s >"$TMP/s1-b.json" 2>&1 &
sleep 1
out=$(run semaphore --id="$NS/s1" --capacity=2 --ttl=10s --owner=h3 2>&1)
check_contains "第 3 个被拒" "$out" '"status":"no_permit"'
wait
held_permits=$(grep -l '"status":"permit_acquired"' "$TMP"/s1-a.json "$TMP"/s1-b.json 2>/dev/null | wc -l)
check_eq "已发放 permit 数" "$held_permits" "2"

echo "== 5. with-lock：租约短于任务时长仍保持互斥，退出码透传"
run with-lock --id="$NS/w1" --ttl=3s --owner=wl -- sh -c 'sleep 5' >"$TMP/w1.json" 2>&1 &
wl_pid=$!
sleep 1
out=$(run lock --id="$NS/w1" --ttl=10s --owner=probe 2>&1)
check_contains "任务执行期间锁仍被持有" "$out" '"status":"busy"'
wait "$wl_pid"
out=$(run status --id="$NS/w1" 2>&1)
check_contains "任务结束后锁已释放" "$out" '"status":"free"'

run with-lock --id="$NS/w2" --ttl=5s -- sh -c 'exit 7' >"$TMP/w2.json" 2>&1
code=$?
check_eq "子进程退出码透传" "$code" "7"
check_contains "child_exited 状态" "$(cat "$TMP/w2.json")" '"status":"child_exited"'

echo "== 6. 租约到期后其他 Agent 可自动接管（无需人工清理）"
run lock --id="$NS/l3" --ttl=3s --owner=dead >/dev/null 2>&1
out=$(run lock --id="$NS/l3" --ttl=10s --owner=next 2>&1)
check_contains "到期前被拒" "$out" '"status":"busy"'
sleep 4
out=$(run lock --id="$NS/l3" --ttl=10s --owner=next 2>&1)
check_contains "到期后可接管" "$out" '"status":"held"'

echo "== 7. 看门狗长跑：ttl=3s 的任务持续 8s 期间不被抢占"
run with-lock --id="$NS/w3" --ttl=3s --owner=soak -- sh -c 'sleep 8' >"$TMP/w3.json" 2>&1 &
soak_pid=$!
sleep 5
out=$(run lock --id="$NS/w3" --ttl=10s --owner=intruder 2>&1)
check_contains "看门狗持续续租，5s 时仍被持有" "$out" '"status":"busy"'
wait "$soak_pid"
out=$(run status --id="$NS/w3" 2>&1)
check_contains "长跑结束后已释放" "$out" '"status":"free"'

echo
echo "结果: PASS=$pass FAIL=$fail"
[ "$fail" -eq 0 ]
