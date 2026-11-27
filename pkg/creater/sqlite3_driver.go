package creater

// SQLite 驱动注册与自动选择。
//
// ## 为什么需要"选择"而不是写死一个名字
//
// 两个主流驱动的注册名不同：
//
//	mattn/go-sqlite3   注册 "sqlite3"，需要 CGO
//	modernc.org/sqlite 注册 "sqlite"，纯 Go
//
// 而 `go-sqlite3-storage` 默认写的是 "sqlite3"。发行版二进制用 CGO_ENABLED=0
// 交叉编译（goreleaser），mattn 不可用，只剩下 modernc——名字对不上，
// 用户会看到一个"注册了 sqlite3 后端但一用就报 unknown driver"的可执行文件。
//
// 这里固定把 modernc 链接进来（它是纯 Go，全平台可用），并在启动时
// 把 "sqlite3" 这个名字转成实际可用的驱动名。

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"

	"modernc.org/sqlite"
)

// cgoDriverAvailable 报告是否注册了需要 CGO 的 "sqlite3" 驱动。
//
// 用 sql.Drivers() 而不是构建标签判断：构建标签只能表达"编译时有没有 CGO"，
// 而这里真正关心的是"运行时有没有人注册了这个名字"——
// 下游库完全可能自己 import 了 mattn，那它就该被优先使用。
func cgoDriverAvailable() bool {
	for _, name := range sql.Drivers() {
		if name == "sqlite3" {
			return true
		}
	}
	return false
}

var (
	sqliteDriverOnce sync.Once
	sqliteDriverName string
)

// sqliteDSN 返回同时兼容两种驱动命名习惯的 DSN。
//
// 做法：
//   - 若运行时已有 "sqlite3"（通常来自 mattn + CGO），原样使用；
//   - 否则把 modernc 的 "sqlite" 以 "sqlite3" 之名注册一次，
//     这样 go-sqlite3-storage 的默认驱动名无需改动，DSN 写法对用户完全不变。
//
// 注册名冲突由 Once 保证只发生一次；重复注册会 panic（database/sql 不允许）。
func sqliteDSN(dsn string) (driver, dsnOut string, err error) {
	sqliteDriverOnce.Do(func() {
		if cgoDriverAvailable() {
			sqliteDriverName = "sqlite3"
			return
		}
		// modernc 的驱动实例可以直接注册到任意名字下：
		// 它的 Driver 实现了 driver.Driver，与 mattn 的行为差异在锁语义层面已抹平
		// （go-sqlite3-storage 现在用错误码/文案双路判断主键冲突，不依赖具体驱动类型）。
		sql.Register("sqlite3", &sqlite.Driver{})
		sqliteDriverName = "sqlite3"
	})
	if sqliteDriverName == "" {
		return "", "", fmt.Errorf("creater: sqlite driver not initialised")
	}
	return sqliteDriverName, applySQLiteDefaults(dsn), nil
}

// applySQLiteDefaults 补上单机多进程协作所需的连接参数。
//
// ## 为什么必须补
//
// SQLite 默认是 journal_mode=delete + busy_timeout=0：一旦有第二进程正在写，
// 本次写入立刻返回 SQLITE_BUSY，而不是等一会儿。CLI 每次调用都是新进程，
// 连发两条命令（push 然后 claim）就会直接失败：
//
//	{"error":"database is locked (5) (SQLITE_BUSY)"}
//
// 这不是偶发，是"单机零依赖后端"的默认体验，必须修。
//
// 参数取值：
//   - busy_timeout=5000  遇到锁就等最多 5s，而不是立刻失败；
//   - journal_mode=WAL   读写不互斥（读不会被写阻塞），多进程并发的基础；
//   - synchronous=NORMAL WAL 下的合理折中（FULL 每次都 fsync，慢一个量级）；
//   - txlock=immediate   写事务一开始就拿写锁，避免升级锁时死锁。
//
// 只在用户**没有显式指定**该参数时补默认值：用户写的 DSN 永远优先，
// 否则"我这个库要 FULL 同步"这类明确意图会被静默改掉。
func applySQLiteDefaults(dsn string) string {
	if dsn == "" {
		dsn = ":memory:"
	}
	path, query, hasQuery := strings.Cut(dsn, "?")
	params := map[string]string{}
	if hasQuery {
		for _, kv := range strings.Split(query, "&") {
			if kv == "" {
				continue
			}
			k, v, _ := strings.Cut(kv, "=")
			params[k] = v
		}
	}
	// 参数名两种驱动都认（modernc 读 _busy_timeout/_journal_mode，
	// mattn 读 _busy_timeout/_journal_mode 以及 busy_timeout/journal_mode）。
	defaults := [][2]string{
		{"_busy_timeout", "5000"},
		{"_journal_mode", "WAL"},
		{"_synchronous", "NORMAL"},
		{"_txlock", "immediate"},
	}
	for _, kv := range defaults {
		k, v := kv[0], kv[1]
		if _, ok := params[k]; ok {
			continue
		}
		// 用户可能用不带下划线的写法（mattn 也接受），同样视为已指定。
		if _, ok := params[strings.TrimPrefix(k, "_")]; ok {
			continue
		}
		params[k] = v
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+params[k])
	}
	return path + "?" + strings.Join(out, "&")
}
