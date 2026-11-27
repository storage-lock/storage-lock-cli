# Changelog

## v0.0.3
_发布日期：2026-10-24_

- build: require published go-events v0.0.5

## v0.0.2
_发布日期：2026-10-12_

- build: 去掉最后一批本地 replace，改为已发布版本

## v0.0.1
_发布日期：2026-10-12_

- build: 依赖改为已发布版本，移除本地 replace

## v0.2.0（首个带二进制发布的版本）

### 发布

- 新增 `.goreleaser.yaml` 与 `.github/workflows/release.yaml`：打 `v*` tag 自动
  构建 Linux/macOS/Windows × amd64/arm64(+linux armv7)、生成 sha256 校验和、
  创建 GitHub Release。
- 版本号由 `-ldflags` 注入；`--version` 与 `version` 子命令输出结构化信息，
  且不再要求 `--storage`（新机器上第一步就能查到版本）。

### 修复

- **发行二进制里 sqlite3 后端不可用**：goreleaser 用 `CGO_ENABLED=0` 交叉编译，
  而 mattn/go-sqlite3 需要 CGO。现在内置纯 Go 驱动 `modernc.org/sqlite`，
  并在运行时把它注册成 `sqlite3`，DSN 写法与行为对用户完全不变。
- **sqlite3 连续命令报 `SQLITE_BUSY`**：SQLite 默认 `busy_timeout=0` +
  `journal_mode=delete`，第二个进程一写就立刻失败。现在给未显式指定参数的
  DSN 自动补 `busy_timeout=5000`、`journal_mode=WAL`、`synchronous=NORMAL`、
  `txlock=immediate`；用户写明的参数一律优先。
- 依赖升级到 go-storage-lock / coordination / remote-storage / sqlite3-storage
  的 v0.2.0，解锁 7 个新协作原语与云端地址迁移能力。

## Unreleased

### 新增：Agent 协作原语（`skills` 子命令组扩容）

从 8 个命令扩到 15 个，覆盖底层库已有但此前没暴露给 Agent 的能力：

- `skills once`：全集群只允许成功执行一次的任务（`claimed` / `busy` / `already_ran`），
  支持 `--complete` / `--abort` / `--hold`；
- `skills queue`：可见性超时工作队列，消费者崩溃后元素自动重新可见（`--push/--claim/--ack/--peek/--wait`）；
- `skills delayed-queue`：每个元素带延迟的队列，用于退避重试而不是 `sleep`；
- `skills counter`：分布式原子计数器（读/加/设/CAS/重置）；
- `skills rate-limit`：分布式令牌桶限流（`allowed`/`denied`，`--wait` 轮询）；
- `skills rw-lock`：读写锁（`--mode=read|write`、`--upgrade`/`--downgrade`/`--unlock`）；
- `skills observe`：一次 `List` 拿到全局快照（总数/持有数/僵尸数）并按 id 排序，便于 diff；
- `skills status` 现在返回完整视图：owner、fencing token、剩余租期、等待者、写意图、`--meta` 元数据；
- `skills lock` 新增 `--meta key=value`（随锁记录原子保存）与 `--fair`（跨进程 FIFO 排队）。

### 修复

- **冷启动并发首次使用会分裂成多个租户**（最严重）：同一台机器上 N 个 Agent 进程
  同时首次使用云端后端时，每个进程各自领取一份 secret，落进互不相干的命名空间，
  表现为"分布式锁偶尔完全失效且不报错"。实测 12 进程出现 2~7 个持锁者。
  现在领取凭据是临界区，用跨进程文件锁（flock / LockFileEx）排队并二次读文件；
  修复后 5 轮 × 12 进程冷启动均为精确 1 个持锁者。已加子进程级回归测试。
- `skills lock --meta` 会把调用方申请的 `--ttl` 悄悄改回默认 5 分钟：
  元数据写入会刷新租约，而当时用默认选项构造锁。现改用同一份选项。
- `skills rw-lock` 的 `--upgrade`/`--downgrade` 不再强制要求 `--mode`。
- `skills queue`/`delayed-queue` 的 `--claim` 之前未注册成 flag（只能走默认分支）。
- 队列 `Peek` 结果缺少 JSON tag，输出成 Go 字段名，Agent 无法按字段解析。
- docs: 添加公开 Firebase 项目创建指南
- feat: 内置默认公开 Firebase 项目实现零配置
- feat: 新增 Firebase Realtime Database 存储支持
- docs: 补充云端存储后端指南用于多 Agent 分布式协作
- chore: 更新 skills 子模块到最新提交
- feat: 新增 memory 和 sqlite3 免费存储后端支持
- docs: README 新增 Agent 协作 SKILL 章节并链接独立仓库
- chore: skills 改为 git submodule，指向 go-storage-lock-skills 仓库
- chore: 删除本地 skills 目录，准备改为 submodule
- feat: 接入 sqlserver 别名并标注暂不可用，TiDB 验证通过
- test: 修正选主断言，改为直接验证互斥窗口与令牌递增
- test: 冒烟脚本补充租约到期接管与看门狗长跑场景
- feat: 接入 redis 后端，Agent 协作可用最低延迟的存储
- fix: with-lock 获取失败时先读 ctx.Err 再取消，避免掩盖真实原因
- test: 补齐 skills 命令的单元测试与端到端冒烟脚本
- feat: 增加供 Agent 协作的 skills 命令组与 SKILL 定义
