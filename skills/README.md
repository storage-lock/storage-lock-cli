# Agent Skills：用分布式锁协作

这一组 SKILL 让多个 Agent 通过 `storage-lock-cli skills` 子命令使用同一套分布式锁服务协作，而不需要每个 Agent 自己写 Go 代码。所有命令基于仓库里的 `go-storage-lock` / `go-storage-lock-coordination`，共享同一套存储后端（MariaDB、MySQL、TiDB、PostgreSQL、MongoDB、SQL Server 等）。

## 安装

```bash
# 在仓库根目录构建（go.work 已包含 storage-lock-cli）
cd storage-lock-cli && go build -o ~/bin/storage-lock-cli .

# 或者直接安装到 GOPATH/bin
cd storage-lock-cli && go install .
```

把 `skills/` 下的四个目录复制到 Claude Code 的技能目录：

```bash
cp -r storage-lock-cli/skills/storage-lock-* ~/.claude/skills/
```

### 验证安装

```bash
# 需要真实存储后端，默认用 sl-mariadb
CLI=storage-lock-cli ./skills/smoke-test.sh
```

脚本会跑完单 key 锁生命周期、10 进程互斥、5 进程选主、信号量容量、`with-lock` 续租与退出码透传，最后输出 `PASS=n FAIL=n`。

然后重启 Claude Code 会话，`/skills` 中会看到：

| Skill | 用途 |
| --- | --- |
| `storage-lock-lock` | 获取 / 续租 / 释放 / 查询一把独占租约锁 |
| `storage-lock-with-lock` | 持锁运行一个子命令，任务结束自动释放（长任务首选） |
| `storage-lock-leader-election` | 多个 Agent 中选出一个 leader，可保持领导权 |
| `storage-lock-semaphore` | 固定容量信号量，限制同时并发的 Agent 数量 |

## 统一约定

所有 `storage-lock-cli skills` 子命令**只向 stdout 输出一个 JSON 对象**，字段固定：

```json
{"success":true,"status":"held","lock_id":"resource-x","owner_id":"agent/...","fencing_token":1,"remaining_lease":"30s"}
```

- `success`：布尔值，命令是否成功完成（"锁被别人拿着" 属于 `success:false`，但不是故障）。
- `status`：语义化状态，调用方按它分支，不要解析 `error` 文本。
- `fencing_token`：单调递增的栅栏令牌，写共享状态时应带上它。
- `owner_id`：后续 `renew` / `unlock` / `semaphore-release` 需要的持有者标识。

全局参数（所有子命令通用）：

```bash
--storage=mariadb                      # 后端实现，也接受 mariadb-storage 全名
--dsn='root:rootpass@tcp(127.0.0.1:13309)/storage_lock_test'
--timeout=30s                          # 单条命令上限
```

已接入并通过端到端验证的后端（`--storage` 值）：

| 后端 | 示例 DSN |
| --- | --- |
| `redis` | `redis://127.0.0.1:6379/0` |
| `mariadb` | `root:rootpass@tcp(127.0.0.1:13309)/storage_lock_test` |
| `mysql` | `root:rootpass@tcp(127.0.0.1:3306)/storage_lock_test` |
| `postgresql` | `postgres://postgres:pgpass@127.0.0.1:5432/storage_lock_test?sslmode=disable` |
| `mongodb` | `mongodb://127.0.0.1:27017/storage_lock_test` |
| `tidb` | `root:@tcp(127.0.0.1:14000)/storage_lock_test` |

另有 `sql-server` 已注册但**暂不可用**：`go-sqlserver-storage` 直接复用 `Sql92Provider`，其 SQL 用 `?` 占位符，但 mssql 驱动不支持 `?`（需命名参数 `@p1`、`@p2`），需该仓实现专用 Provider 后才能验证。选后端只看两点：**并发下 CAS 必须原子**、**能给出统一的当前时间**——这正是不支持 CAS 的存储（如 ClickHouse）不能用于 Agent 协作的原因。

**所有参与协作的 Agent 必须使用相同的 `--storage` 和 `--dsn`**，否则它们看到的不是同一套锁。

## 状态字典

| 命令 | 状态 | 含义 |
| --- | --- | --- |
| `lock` | `held` | 获取成功 |
| | `busy` | 被其他 owner 持有 |
| `renew` | `held` | 续租成功 |
| | `lost` | 租约已不属于该 owner |
| `unlock` | `released` | 释放成功 |
| | `unlock_failed` | 释放失败（如租约已过期） |
| `status` | `held` / `free` | 当前是否有人持有 |
| `with-lock` | `executed` | 子进程退出 0，锁已释放 |
| | `child_exited` | 子进程非 0 退出，CLI 透传子进程退出码 |
| | `interrupted` | 收到 SIGINT/SIGTERM |
| `leader-election` | `leader` / `follower` / `lost` | 竞选结果（不带 `--hold` 时是"试探"，当选后立即让出，并发调用可能接力式先后当选；带 `--hold` 时排队等待并保持领导权） |
| `semaphore` | `permit_acquired` / `no_permit` / `held` / `lost` | permit 结果 |
| `semaphore-release` | `released` / `release_failed` | 归还结果（幂等） |

## 选择哪个 Skill

- **只是一段不能并发的临界区** → `storage-lock-lock`
- **整个任务（脚本、部署、迁移）都要独占** → `storage-lock-with-lock`
- **只需要一个 Agent 干活，其他 Agent 看着/等待** → `storage-lock-leader-election`
- **最多 N 个 Agent 同时干活** → `storage-lock-semaphore`

## 现实约束

- `skills lock` 不打开看门狗：租约严格等于 `--ttl`。任务超过 ttl 就用 `with-lock`，或在到期前 `renew`。
- `with-lock` 与 `leader-election --hold` 期间由看门狗续租，进程活着就不会过期；进程被杀死后最多 `--ttl` 由别的 Agent 接手。
- 释放是"逻辑删除 + 保留版本记录"（tombstone），所以 fencing token 单调递增，不会因为反复加解锁而回退。
- 信号量的 `--capacity` 是部署契约：同一 namespace 的所有 Agent 必须写同一个值。
