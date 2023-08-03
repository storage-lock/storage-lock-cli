package creater

import (
	"context"
	"fmt"
	"github.com/storage-lock/go-storage-lock/pkg/storage"
	"github.com/storage-lock/go-storage-lock/pkg/storage/mariadb_storage"
	"github.com/storage-lock/go-storage-lock/pkg/storage/mongodb_storage"
	"github.com/storage-lock/go-storage-lock/pkg/storage/mysql_storage"
	"github.com/storage-lock/go-storage-lock/pkg/storage/postgresql_storage"
	"github.com/storage-lock/go-storage-lock/pkg/storage/sql_server_storage"
	"github.com/storage-lock/go-storage-lock/pkg/storage/tidb_storage"
	"sort"
	"strings"
)

// StorageCreateFunc 用来创建Storage的函数
type StorageCreateFunc func(ctx context.Context, dsn string) (storage.Storage, error)

// 对于内置的Storage的支持
var storageCreateMap map[string]StorageCreateFunc = make(map[string]StorageCreateFunc)

func init() {

	// mariadb_storage
	mariadbStorage, _ := mariadb_storage.NewMariaDbStorage(nil, nil)
	storageCreateMap[strings.ToLower(mariadbStorage.GetName())] = func(ctx context.Context, dsn string) (storage.Storage, error) {
		connectionProvider := mariadb_storage.NewMariaStorageConnectionProviderFromDSN(dsn)
		options := mariadb_storage.NewMariaStorageOptions()
		options.WithConnectionProvider(connectionProvider)
		return mariadb_storage.NewMariaDbStorage(ctx, options)
	}

	// tidb_storage
	tidbStorage, _ := tidb_storage.NewTidbStorage(nil, nil)
	storageCreateMap[strings.ToLower(tidbStorage.GetName())] = func(ctx context.Context, dsn string) (storage.Storage, error) {
		connectionProvider := tidb_storage.NewTidbConnectionProviderFromDSN(dsn)
		options := tidb_storage.NewTidbStorageOptions()
		options.WithConnectionProvider(connectionProvider)
		return tidb_storage.NewTidbStorage(ctx, options)
	}

	// mysql_storage
	mysqlStorage, _ := mysql_storage.NewMySQLStorage(nil, nil)
	storageCreateMap[strings.ToLower(mysqlStorage.GetName())] = func(ctx context.Context, dsn string) (storage.Storage, error) {
		connectionProvider := mysql_storage.NewMySQLConnectionProviderFromDSN(dsn)
		options := mysql_storage.NewMySQLStorageOptions()
		options.WithConnectionProvider(connectionProvider)
		return mysql_storage.NewMySQLStorage(ctx, options)
	}

	// mongodb_storage
	mongoStorage, _ := mongodb_storage.NewMongoStorage(nil, nil)
	storageCreateMap[strings.ToLower(mongoStorage.GetName())] = func(ctx context.Context, dsn string) (storage.Storage, error) {
		connectionProvider := mongodb_storage.NewMongoConnectionProvider(dsn)
		options := mongodb_storage.NewMongoStorageOptions()
		options.WithConnectionProvider(connectionProvider)
		return mongodb_storage.NewMongoStorage(ctx, options)
	}

	// postgresql_storage
	postgresqlStorage, _ := postgresql_storage.NewPostgreSQLStorage(nil, nil)
	storageCreateMap[strings.ToLower(postgresqlStorage.GetName())] = func(ctx context.Context, dsn string) (storage.Storage, error) {
		connectionProvider := postgresql_storage.NewPostgreSQLConnectionGetterFromDSN(dsn)
		options := &postgresql_storage.PostgreSQLStorageOptions{}
		options.ConnectionProvider = connectionProvider
		return postgresql_storage.NewPostgreSQLStorage(ctx, options)
	}

	// sql_server_storage
	sqlServerStorage, _ := sql_server_storage.NewSqlServerStorage(nil, nil)
	storageCreateMap[strings.ToLower(sqlServerStorage.GetName())] = func(ctx context.Context, dsn string) (storage.Storage, error) {
		connectionProvider := sql_server_storage.NewSqlServerStorageConnectionGetterFromDSN(dsn)
		options := sql_server_storage.NewSqlServerStorageOptions()
		options.WithConnectionProvider(connectionProvider)
		return sql_server_storage.NewSqlServerStorage(ctx, options)
	}

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
	createFunc, exists := storageCreateMap[storageName]
	if !exists {
		return nil, fmt.Errorf("Storage %s does not exist", storageName)
	}
	return createFunc(context.Background(), dsn)
}
