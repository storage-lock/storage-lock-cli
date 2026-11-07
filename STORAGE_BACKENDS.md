# 存储后端指南

storage-lock-cli 本身是客户端工具，分布式锁需要一个共享的存储后端来保证跨进程/跨机器的原子性。

## 多 Agent 协作场景分类

### 场景 1：单机多 Agent（本地开发）
- **多个 Claude Code 会话在同一台机器上运行**
- **推荐**：SQLite3（免费，零依赖）
- **示例**：本地同时跑多个 Agent 处理不同任务，需要协调

### 场景 2：局域网多 Agent（自建服务器）
- **多台机器在同一个内网，有自建数据库**
- **推荐**：Redis/MariaDB/PostgreSQL（免费软件，需要自建）
- **示例**：团队多台开发机通过内网数据库协作

### 场景 3：云端多 Agent（分布式）
- **Agent 运行在不同网络环境，需要云端共享存储**
- **推荐**：云端数据库（免费试用或付费）
- **示例**：远程服务器、云端 CI/CD、多地域协作

---

## 免费存储后端（本地/自建）

## 免费存储后端对比（本地/自建）

| 后端 | 适用场景 | 跨机器 | 多进程 | 需要安装 | 难度 |
| --- | --- | --- | --- | --- | --- |
| **memory** | 单进程测试 | ❌ | ❌ | 无 | ⭐ |
| **sqlite3** | 单机多进程 | ❌ | ✅ | 无（Go 已内置） | ⭐ |
| **redis** | 局域网分布式 | ✅ | ✅ | Docker 或本地安装 | ⭐⭐ |
| **mariadb/mysql** | 局域网分布式 | ✅ | ✅ | Docker 或本地安装 | ⭐⭐ |
| **postgresql** | 局域网分布式 | ✅ | ✅ | Docker 或本地安装 | ⭐⭐ |

## 1. Memory 存储（单进程测试）

**适用场景**：单进程内部测试、快速验证功能（仅限单进程）

**特点**：
- ✅ 零依赖，无需安装任何服务
- ✅ 纯内存，速度最快
- ❌ **不支持多进程**（每个进程有独立内存实例）
- ❌ 不支持跨机器
- ❌ 进程重启后数据丢失

**重要限制**：
- ⚠️ Memory 存储是单进程内存存储，每个进程都有独立的内存实例
- ⚠️ **不能用于多进程互斥**，因为进程间无法共享内存
- ⚠️ 仅适用于单进程内的测试场景，多进程场景请使用 sqlite3 或其他共享存储

**使用方法**：
```bash
# memory 存储不需要 DSN
storage-lock-cli --storage=memory skills lock --id=my-lock --ttl=30s
```

**示例**：
```bash
# 获取锁
./storage-lock-cli --storage=memory skills lock --id=test-1 --ttl=30s

# 持锁运行命令
./storage-lock-cli --storage=memory skills with-lock --id=deploy --ttl=30s -- ./deploy.sh

# 信号量（限制并发数）
./storage-lock-cli --storage=memory skills semaphore --id=workers --capacity=3 --ttl=30s
```

## 2. SQLite3 存储（单机持久化）

**适用场景**：单机多进程、需要持久化、轻量级部署

**特点**：
- ✅ 零外部服务，文件数据库
- ✅ Go 内置驱动，无需额外安装
- ✅ 支持多进程并发（需要 WAL 模式）
- ❌ 不支持跨机器
- ✅ 数据持久化到文件

**使用方法**：
```bash
# DSN 为 SQLite3 数据库文件路径
storage-lock-cli --storage=sqlite3 --dsn="/path/to/locks.db" skills lock --id=my-lock --ttl=30s
```

**示例**：
```bash
# 使用临时文件
./storage-lock-cli --storage=sqlite3 --dsn="/tmp/locks.db" skills lock --id=test-1 --ttl=30s

# 使用项目目录
./storage-lock-cli --storage=sqlite3 --dsn="./locks.db" skills with-lock --id=deploy --ttl=30s -- ./deploy.sh
```

**注意事项**：
- SQLite3 单文件在多进程并发时性能有限，适合低并发场景
- 高并发建议使用 Redis 或关系型数据库

## 3. Redis 存储（跨机器推荐）

**适用场景**：跨机器分布式、高性能、低延迟

**特点**：
- ✅ 内存数据库，延迟最低
- ✅ 支持跨机器
- ✅ Lua 脚本保证 CAS 原子性
- ✅ Docker 快速启动
- ⚠️ 需要 Redis 服务

**安装 Redis（Docker）**：
```bash
# 快速启动 Redis
docker run -d -p 6379:6379 --name redis redis:latest

# 持久化数据
docker run -d -p 6379:6379 -v redis-data:/data --name redis redis:latest --appendonly yes
```

**安装 Redis（本地）**：
```bash
# Ubuntu/Debian
sudo apt-get install redis-server

# macOS
brew install redis
brew services start redis
```

**使用方法**：
```bash
# DSN 为 Redis URI
storage-lock-cli --storage=redis --dsn="redis://127.0.0.1:6379/0" skills lock --id=my-lock --ttl=30s

# 带密码
storage-lock-cli --storage=redis --dsn="redis://:password@127.0.0.1:6379/0" skills lock --id=my-lock --ttl=30s
```

**示例**：
```bash
# 获取锁
./storage-lock-cli --storage=redis --dsn="redis://127.0.0.1:6379/0" skills lock --id=test-1 --ttl=30s

# 持锁运行命令
./storage-lock-cli --storage=redis --dsn="redis://127.0.0.1:6379/0" skills with-lock --id=deploy --ttl=30s -- ./deploy.sh

# 领导者选举
./storage-lock-cli --storage=redis --dsn="redis://127.0.0.1:6379/0" skills leader-election --id=leader --ttl=30s --hold=5m
```

## 4. MariaDB/MySQL 存储（已有数据库）

**适用场景**：已有数据库、事务型应用、企业环境

**特点**：
- ✅ 支持跨机器
- ✅ 适合已有数据库环境
- ✅ 支持 SQL 查询排障
- ⚠️ 需要 MariaDB/MySQL 服务

**安装 MariaDB（Docker）**：
```bash
docker run -d -p 3306:3306 \
  -e MYSQL_ROOT_PASSWORD=rootpass \
  --name mariadb \
  mariadb:latest
```

**使用方法**：
```bash
# DSN 格式：user:password@tcp(host:port)/database
storage-lock-cli --storage=mariadb --dsn="root:rootpass@tcp(127.0.0.1:3306)/storage_lock" skills lock --id=my-lock --ttl=30s
```

**创建数据库**：
```sql
CREATE DATABASE storage_lock;
```

**示例**：
```bash
# 获取锁
./storage-lock-cli --storage=mariadb --dsn="root:rootpass@tcp(127.0.0.1:3306/storage_lock" skills lock --id=test-1 --ttl=30s

# 列出所有锁
./storage-lock-cli --storage=mariadb --dsn="root:rootpass@tcp(127.0.0.1:3306/storage_lock" list
```

## 5. PostgreSQL 存储（跨机器）

**适用场景**：跨机器分布式、已有 PostgreSQL

**特点**：
- ✅ 支持跨机器
- ✅ 适合已有 PostgreSQL 环境
- ⚠️ 需要 PostgreSQL 服务

**安装 PostgreSQL（Docker）**：
```bash
docker run -d -p 5432:5432 \
  -e POSTGRES_PASSWORD=postgres \
  --name postgres \
  postgres:latest
```

**使用方法**：
```bash
# DSN 格式：postgres://user:password@host:port/database?sslmode=disable
storage-lock-cli --storage=postgresql --dsn="postgres://postgres:postgres@127.0.0.1:5432/storage_lock?sslmode=disable" skills lock --id=my-lock --ttl=30s
```

**创建数据库**：
```sql
CREATE DATABASE storage_lock;
```

## 快速开始指南

### 本地测试（零依赖，推荐）
```bash
# 使用 sqlite3 存储（支持多进程）
./storage-lock-cli --storage=sqlite3 --dsn="./locks.db" skills lock --id=test --ttl=30s
```

### 单进程快速测试
```bash
# 使用 memory 存储（仅限单进程，不支持多进程互斥）
./storage-lock-cli --storage=memory skills lock --id=test --ttl=30s
```

### 跨机器分布式
```bash
# 启动 Redis
docker run -d -p 6379:6379 --name redis redis:latest

# 使用 Redis 存储
./storage-lock-cli --storage=redis --dsn="redis://127.0.0.1:6379/0" skills lock --id=test --ttl=30s
```

## 性能对比

| 后端 | 延迟 | 吞吐量 | 适用并发数 |
| --- | --- | --- | --- |
| memory | <1ms | 最高 | 单机进程 |
| sqlite3 | ~1-5ms | 中等 | 单机进程（低并发） |
| redis | ~1-2ms | 高 | 跨机器（高并发） |
| mariadb/mysql | ~5-10ms | 中高 | 跨机器（中高并发） |
| postgresql | ~5-10ms | 中高 | 跨机器（中高并发） |

## 云端存储后端（多 Agent 分布式协作）

如果你的 Agent 需要跨不同网络环境协作（例如云端 CI/CD、远程服务器、多地域），建议使用云端存储后端。

### 云端存储对比

| 云存储 | 提供商 | 免费额度 | 免费试用 | 延迟 | 适合场景 |
| --- | --- | --- | --- | --- | --- |
| **Upstash Redis** | Upstash | 10K 命令/天 | 有 | 低（全球边缘） | 跨地域 Agent 协作 |
| **Redis Cloud** | Redis Labs | 30MB 存储 | 有 | 中等 | 标准云端 Redis |
| **Cloud SQL (PostgreSQL)** | Google Cloud | 无 | 90 天 | 中等 | 关系型数据库 |
| **Amazon RDS (PostgreSQL)** | AWS | 无 | 12 个月 | 中等 | 关系型数据库 |
| **Azure Database (PostgreSQL)** | Microsoft Azure | 无 | 12 个月 | 中等 | 关系型数据库 |

### 推荐方案：Upstash Redis（免费额度大，跨地域延迟低）

**为什么推荐 Upstash**：
- ✅ 免费额度：10K 命令/天（适合开发和测试）
- ✅ 全球边缘部署，延迟低
- ✅ Serverless，按需付费
- ✅ 支持 HTTP/REST API（防火墙友好）
- ✅ 无需运维，开箱即用

**注册和创建**：
1. 访问 https://upstash.com/
2. 注册账号（支持 GitHub 登录）
3. 创建 Redis 数据库
4. 获取 REST URL 或 Redis URL

**使用方法**：
```bash
# 使用 Upstash Redis
storage-lock-cli --storage=redis --dsn="redis://default:password@host:port" skills lock --id=my-lock --ttl=30s

# Upstash 也支持 REST API（如果防火墙限制 Redis 端口）
# 需要使用 Upstash 的 Go SDK，目前 CLI 暂不支持 REST API
```

### 其他云端存储

**Redis Cloud**：
```bash
# 注册：https://redis.com/try-free/
# 创建数据库后获取连接信息
storage-lock-cli --storage=redis --dsn="rediss://:password@host:port" skills lock --id=my-lock --ttl=30s
```

**Google Cloud SQL (PostgreSQL)**：
```bash
# 创建 PostgreSQL 实例
# 获取连接信息
storage-lock-cli --storage=postgresql --dsn="postgres://user:password@host:port/dbname?sslmode=require" skills lock --id=my-lock --ttl=30s
```

**Amazon RDS (PostgreSQL)**：
```bash
# 创建 RDS PostgreSQL 实例
# 获取连接信息
storage-lock-cli --storage=postgresql --dsn="postgres://user:password@host:port/dbname?sslmode=require" skills lock --id=my-lock --ttl=30s
```

### 云端存储注意事项

1. **安全性**：
   - 使用 SSL/TLS 连接（`sslmode=require` 或 `rediss://`）
   - 不要在命令行中硬编码密码，使用环境变量
   - 使用 IAM 权限控制访问

2. **成本控制**：
   - 免费额度有限，生产环境注意监控用量
   - 设置预算告警
   - 低频场景考虑 Serverless 方案

3. **网络延迟**：
   - 跨地域延迟可能较高，增加 `--ttl` 到 10-30 秒
   - 选择离 Agent 最近的区域

4. **高可用**：
   - 云端数据库通常自带高可用和备份
   - 注意维护窗口可能影响可用性

## 选择建议

### 根据场景选择

- **本地测试/开发**：memory（最快）或 sqlite3（持久化）
- **单机生产**：sqlite3（低并发）或 Redis（高并发）
- **局域网多机器**：Redis（推荐）或 MariaDB/PostgreSQL（已有数据库）
- **云端多 Agent 协作**：Upstash Redis（免费额度大）或 Redis Cloud
- **企业环境**：MariaDB/PostgreSQL（与现有数据库集成）
- **AWS 环境**：Amazon RDS PostgreSQL
- **GCP 环境**：Cloud SQL PostgreSQL
- **Azure 环境**：Azure Database PostgreSQL

## 查看支持的存储后端

```bash
# 列出所有支持的存储后端
./storage-lock-cli --help
# 输出：supported: mariadb, mariadb-storage, memory, memory-storage, mysql, mysql-storage, postgresql, postgresql-storage, redis, redis-storage, sqlite3, sqlite3-storage, sql-server, sqlserver-storage, tidb, tidb-storage
```

## 注意事项

1. **时间同步**：跨机器场景下，所有机器的系统时间需要同步（NTP），否则可能导致租约计算错误
2. **网络延迟**：跨机器场景下，`--ttl` 应大于网络往返时间，建议至少 5 秒
3. **故障恢复**：进程崩溃后，锁会在 TTL 到期后自动释放，无需手动清理
4. **Fencing Token**：在执行不可逆操作前，务必验证 fencing token，防止过期锁的误操作
