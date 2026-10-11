# storage-lock-cli

用于分布式锁的安全运维和前台持有。

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

命令：

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

## 安全边界

释放后的锁记录是墓碑，版本号用于 fencing token 严格递增。`delete` 和 `clear` 默认返回 unsupported，因为物理删除会使旧客户端重新创建记录时复用 fencing 历史并产生 ABA。进程崩溃时 CLI 不可能证明已释放，锁只能依靠租约过期恢复；业务资源仍必须校验 fencing token。
