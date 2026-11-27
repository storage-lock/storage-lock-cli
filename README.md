# storage-lock-cli

用于分布式锁的安全运维、前台持有和 Agent 协作。

## 存储后端

CLI 本身是客户端工具，需要一个共享的存储后端来保证跨进程/跨机器的原子性。

### 多 Agent 协作场景

- **单机多 Agent**：SQLite3（免费，零依赖）
- **局域网多 Agent**：Redis/MariaDB/PostgreSQL（自建）
- **云端多 Agent**：`--storage=cloud`（推荐，零配置，免注册）

`cloud` 是给 Agent 用的默认后端：不填 DSN，首次调用自动向服务端领取租户凭据，
之后本机所有 Agent 共享同一命名空间：

```bash
storage-lock-cli --storage=cloud skills lock --id=deploy --ttl=30s
```

它同时解决了"服务端地址会变"这件事：

- 老地址还在 → 服务端 308 到新地址，客户端自动跟随；
- 老地址彻底下线 → 客户端拉**签名发现文档**（内置托管在 jsDelivr/GitHub，
  也可用 `STORAGE_LOCK_DISCOVERY_URL` 指向自建位置），验签通过后切换并写回
  `~/.config/storage-lock/endpoints.json`；
- 发现文档被篡改或冒充者自称同一身份 → 客户端用首次接触时**钉住的公钥**验签，
  验不过就明确报错，绝不把 secret 发出去。

所以换域名不需要重发 CLI、也不需要人工改每台机器的配置。

详细指南见 [STORAGE_BACKENDS.md](STORAGE_BACKENDS.md)，包含本地/自建/云端存储后端的完整对比和使用方法。

## 配置与命令

所有需要访问 Storage 的命令都需要：

```bash
storage-lock-cli --storage mysql --dsn 'user:password@tcp(host:3306)/db' <command>
```

公共参数：

- `--storage`：存储实现名称
- `--dsn`：连接串
- `--timeout`：命令超时
- `--output=json|table`：输出格式

### 运维命令

- `list`：列出锁记录，可用 `--active-only`、`--tombstones=false` 过滤
- `show <lock-id>`：显示有效持有、过期记录或释放墓碑
- `lock <lock-id> [--owner id]`：获取锁并前台持有；未指定 owner 时生成并输出 session owner，收到 SIGINT/SIGTERM 后尝试有界清理
- `unlock <lock-id> --owner id`：仅允许指定真实 owner 释放，不提供无主强制解锁
- `version`：显示 CLI 版本

示例：

```bash
storage-lock-cli --storage mysql --dsn "$DSN" --output table list
storage-lock-cli --storage postgres --dsn "$DSN" show jobs:daily
storage-lock-cli --storage mysql --dsn "$DSN" lock jobs:daily --owner deploy-2026
storage-lock-cli --storage mysql --dsn "$DSN" unlock jobs:daily --owner deploy-2026
```

### Agent 协作 SKILL 命令

`skills` 子命令组专为 Claude Code Agent 设计，提供 JSON 契约便于脚本调用：

- `skills lock|renew|unlock|status`：无状态租约锁（`status` 含 owner/令牌/剩余租期/等待者/元数据）
- `skills with-lock --id -- [CMD...]`：持锁运行子命令，看门狗续租
- `skills leader-election [--hold]`：领导者选举
- `skills semaphore|semaphore-release`：限制"同时有几个"在跑
- `skills rate-limit`：限制"每秒能调几次"（令牌桶）
- `skills rw-lock`：读写锁，多个读者可与一个写者互斥共存
- `skills once`：全区只允许成功执行一次的任务（部署/迁移）
- `skills queue`：可见性超时队列，消费者崩溃后任务自动回到队列
- `skills delayed-queue`：带延迟的队列，用于退避重试（不需要 sleep）
- `skills counter`：分布式原子计数器（含 CAS）
- `skills observe`：一次 List 拿到全局锁快照与僵尸锁统计

完整文档见 [go-storage-lock-skills](https://github.com/storage-lock/go-storage-lock-skills)。

安装 SKILL（需先安装 CLI）：

```bash
git clone https://github.com/storage-lock/go-storage-lock-skills.git
cd go-storage-lock-skills
cp -r storage-lock-* ~/.claude/skills/
```

## 安全边界

释放后的锁记录是墓碑，版本号用于 fencing token 严格递增。`delete` 和 `clear` 默认返回 unsupported，因为物理删除会使旧客户端重新创建记录时复用 fencing 历史并产生 ABA。进程崩溃时 CLI 不可能证明已释放，锁只能依靠租约过期恢复；业务资源仍必须通过 fencing token 校验。
