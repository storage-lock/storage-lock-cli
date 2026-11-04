# storage-lock-cli

用于分布式锁的安全运维、前台持有和 Agent 协作。

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

- `skills lock|renew|unlock|status`：无状态租约锁
- `skills with-lock --id -- [CMD...]`：持锁运行子命令，看门狗续租
- `skills leader-election [--hold]`：领导者选举
- `skills semaphore|semaphore-release`：固定容量信号量

完整文档见 [go-storage-lock-skills](https://github.com/storage-lock/go-storage-lock-skills)。

安装 SKILL（需先安装 CLI）：

```bash
git clone https://github.com/storage-lock/go-storage-lock-skills.git
cd go-storage-lock-skills
cp -r storage-lock-* ~/.claude/skills/
```

## 安全边界

释放后的锁记录是墓碑，版本号用于 fencing token 严格递增。`delete` 和 `clear` 默认返回 unsupported，因为物理删除会使旧客户端重新创建记录时复用 fencing 历史并产生 ABA。进程崩溃时 CLI 不可能证明已释放，锁只能依靠租约过期恢复；业务资源仍必须通过 fencing token 校验。
