package creater

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	firebase_storage "github.com/storage-lock/go-firebase-storage"
	mariadb_storage "github.com/storage-lock/go-mariadb-storage"
	memory_storage "github.com/storage-lock/go-memory-storage"
	mongodb_storage "github.com/storage-lock/go-mongodb-storage"
	mysql_storage "github.com/storage-lock/go-mysql-storage"
	postgresql_storage "github.com/storage-lock/go-postgresql-storage"
	redis_storage "github.com/storage-lock/go-redis-storage"
	sqlite3_storage "github.com/storage-lock/go-sqlite3-storage"
	sqlserver_storage "github.com/storage-lock/go-sqlserver-storage"
	"github.com/storage-lock/go-storage"
	tidb_storage "github.com/storage-lock/go-tidb-storage"

	"github.com/google/uuid"
)

// StorageCreateFunc 用来创建Storage的函数
type StorageCreateFunc func(ctx context.Context, dsn string) (storage.Storage, error)

// 对于内置的Storage的支持
var storageCreateMap map[string]StorageCreateFunc = make(map[string]StorageCreateFunc)

// NOTE: 本文件曾引用 `github.com/storage-lock/go-storage-lock/pkg/storage/...` 下的
// mariadb/mysql/mongodb/postgresql/sql_server/tidb_storage 包——那是 2023 年中之前
// go-storage-lock 单体仓库时代的旧内部路径，各存储拆分为独立仓库（go-*-storage）后
// 已彻底不存在。本次按现行各独立仓库的构造器重写，逐个对照过各仓库导出的
// NewXxxStorage(ctx, options)/NewXxxConnectionManagerFromDsn(dsn) 签名。
//
// 注册表用各仓库导出的名称常量直接登记，而不是先 new 一个实例再 GetName()：
// 现行构造器会在创建时连库并 Init，没有 DSN 必然失败返回 nil，那样整个注册表
// 会是空的（历史上正是这个写法让 --storage 永远报 "does not exist"）。
func init() {
	// 生成一个固定的租户 ID，用于多租户隔离
	// 每台机器生成一次，确保同一台机器的所有 CLI 使用相同的租户 ID
	tenantID := getTenantID()

	// 默认公开 Firebase 项目（零配置，无需用户自己创建）
	// 注意：这是一个公开项目，任何人都能读写，仅适合开发测试
	const defaultFirebaseURL = "https://storage-lock-public.firebaseio.com"

	// firebase 存储适合云端多 Agent 协作，免费额度大，无需配置
	register("firebase-storage", func(ctx context.Context, dsn string) (storage.Storage, error) {
		// 如果用户没有提供 DSN，使用默认公开项目
		databaseURL := dsn
		if databaseURL == "" {
			databaseURL = defaultFirebaseURL
		}
		options := &firebase_storage.FirebaseStorageOptions{
			DatabaseURL: databaseURL,
			TenantID:    tenantID,
		}
		return firebase_storage.NewFirebaseStorage(ctx, options)
	})
	register("firebase", func(ctx context.Context, dsn string) (storage.Storage, error) {
		// 如果用户没有提供 DSN，使用默认公开项目
		databaseURL := dsn
		if databaseURL == "" {
			databaseURL = defaultFirebaseURL
		}
		options := &firebase_storage.FirebaseStorageOptions{
			DatabaseURL: databaseURL,
			TenantID:    tenantID,
		}
		return firebase_storage.NewFirebaseStorage(ctx, options)
	})
	// memory 存储适合单机多进程测试，无需外部依赖
	register(memory_storage.NewMemoryStorage().GetName(), func(ctx context.Context, dsn string) (storage.Storage, error) {
		return memory_storage.NewMemoryStorage(), nil
	})
	register(mariadb_storage.StorageName, func(ctx context.Context, dsn string) (storage.Storage, error) {
		connectionManager := mariadb_storage.NewMariadbConnectionManagerFromDsn(dsn)
		options := mariadb_storage.NewMariadbStorageOptions().SetConnectionManager(connectionManager)
		return mariadb_storage.NewMariadbStorage(ctx, options)
	})
	register(tidb_storage.StorageName, func(ctx context.Context, dsn string) (storage.Storage, error) {
		connectionManager := tidb_storage.NewTidbConnectionManagerFromDsn(dsn)
		options := tidb_storage.NewTidbStorageOptions().SetConnectionManager(connectionManager)
		return tidb_storage.NewTidbStorage(ctx, options)
	})
	register(mysql_storage.StorageName, func(ctx context.Context, dsn string) (storage.Storage, error) {
		connectionManager := mysql_storage.NewMysqlConnectionManagerFromDsn(dsn)
		options := mysql_storage.NewMysqlStorageOptions().SetConnectionManager(connectionManager)
		return mysql_storage.NewMysqlStorage(ctx, options)
	})
	// redis 走 Lua 脚本做 CAS，延迟最低，是 Agent 协作场景里成本最小的一种后端。
	register(redis_storage.RedisStorageName, func(ctx context.Context, dsn string) (storage.Storage, error) {
		options := redis_storage.NewRedisStorageOptionsWithURI(dsn)
		return redis_storage.NewRedisStorage(ctx, options)
	})
	register(mongodb_storage.MongoStorageName, func(ctx context.Context, dsn string) (storage.Storage, error) {
		connectionManager := mongodb_storage.NewMongoConnectionManagerFromURI(dsn)
		options := mongodb_storage.NewMongoStorageOptions().SetConnectionManager(connectionManager)
		return mongodb_storage.NewMongoStorage(ctx, options)
	})
	register(postgresql_storage.PostgresqlStorageName, func(ctx context.Context, dsn string) (storage.Storage, error) {
		connectionManager := postgresql_storage.NewPostgresqlConnectionGetterFromDsn(dsn)
		options := postgresql_storage.NewPostgresqlStorageOptions().SetConnectionManager(connectionManager)
		return postgresql_storage.NewPostgresqlStorage(ctx, options)
	})
	// sqlite3 适合单机多进程，无需外部服务
	register(sqlite3_storage.SqliteStorageName, func(ctx context.Context, dsn string) (storage.Storage, error) {
		// 保证 "sqlite3" 这个名字在运行时确实有驱动可用（详见 sqlite3_driver.go）。
		// 发行版是 CGO_ENABLED=0 构建的，只能靠纯 Go 驱动。
		_, dsn, err := sqliteDSN(dsn)
		if err != nil {
			return nil, err
		}
		connectionManager, err := sqlite3_storage.NewSqlite3ConnectionManager(dsn)
		if err != nil {
			return nil, err
		}
		options := sqlite3_storage.NewSqlite3StorageOptions().SetConnectionManager(connectionManager)
		return sqlite3_storage.NewSqlite3Storage(ctx, options)
	})
	register(sqlserver_storage.StorageName, func(ctx context.Context, dsn string) (storage.Storage, error) {
		connectionManager := sqlserver_storage.NewSqlServerConnectionManagerFromDsn(dsn)
		options := sqlserver_storage.NewSqlServerStorageOptions().SetConnectionManager(connectionManager)
		return sqlserver_storage.NewSqlServerStorage(ctx, options)
	})
	// sql-server-storage 的 StorageName 包含连字符，去掉 "-storage" 后是 "sql-server"。
	// 但用户习惯用 "sqlserver"（无连字符），额外注册别名兼容。
	if fn, ok := storageCreateMap["sql-server-storage"]; ok {
		storageCreateMap["sqlserver"] = fn
	}
}

// register 登记一个存储实现，同时接受全名和去掉 "-storage" 后缀的简称，
// 例如 mariadb-storage 与 mariadb 都能用。
func register(name string, createFunc StorageCreateFunc) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return
	}
	if _, exists := storageCreateMap[name]; !exists {
		storageCreateMap[name] = createFunc
	}
	short := strings.TrimSuffix(name, "-storage")
	if short != name && short != "" {
		if _, exists := storageCreateMap[short]; !exists {
			storageCreateMap[short] = createFunc
		}
	}
}

// getTenantID 生成或获取租户 ID，用于多租户隔离
// 优先使用环境变量 STORAGE_LOCK_TENANT_ID，否则生成一个随机 UUID 并持久化
func getTenantID() string {
	// 优先使用环境变量
	if tenantID := strings.TrimSpace(strings.ToLower(os.Getenv("STORAGE_LOCK_TENANT_ID"))); tenantID != "" {
		return tenantID
	}

	// 从文件读取持久化的租户 ID
	homeDir, err := os.UserHomeDir()
	if err == nil {
		tenantFile := homeDir + "/.storage-lock-tenant-id"
		if data, err := os.ReadFile(tenantFile); err == nil {
			if tenantID := strings.TrimSpace(string(data)); tenantID != "" {
				return tenantID
			}
		}
		// 生成新的租户 ID 并持久化
		tenantID := uuid.New().String()
		_ = os.WriteFile(tenantFile, []byte(tenantID), 0600)
		return tenantID
	}

	// 如果无法持久化，生成一个随机 UUID
	return uuid.New().String()
}

// ListSupportStorageName 列出当前支持的Storage都有哪些
func ListSupportStorageName() []string {
	storageNameSlice := make([]string, 0)
	for name := range storageCreateMap {
		storageNameSlice = append(storageNameSlice, name)
	}
	sort.Strings(storageNameSlice)
	return storageNameSlice
}

// CreateStorage 根据名称和DSN创建Storage
func CreateStorage(storageName string, dsn string) (storage.Storage, error) {
	storageName = strings.ToLower(strings.TrimSpace(storageName))
	createFunc, exists := storageCreateMap[storageName]
	if !exists {
		return nil, fmt.Errorf("Storage %s does not exist", storageName)
	}
	return createFunc(context.Background(), dsn)
}
