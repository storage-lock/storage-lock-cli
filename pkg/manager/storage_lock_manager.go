package manager

import (
	"context"
	message_channel "github.com/golang-infrastructure/go-message-channel"
	"github.com/storage-lock/go-storage"
	storage_lock "github.com/storage-lock/go-storage-lock"
)

// NOTE: 本文件曾引用 `github.com/storage-lock/go-storage-lock/pkg/storage` 与
// `.../pkg/storage_lock`——那是 2023 年中之前单体仓库时代的旧内部路径，存储拆分为
// 独立仓库（go-storage / go-*-storage）后已不存在。按现行 API 重写：storage 相关类型
// 来自 go-storage，锁操作经 storage_lock.NewStorageLock(storage, lockId) 构造。
type StorageLockManager struct {
	storage storage.Storage
}

func NewStorageLockManager(s storage.Storage) *StorageLockManager {
	return &StorageLockManager{storage: s}
}

// List 列出当前的所有锁
func (x *StorageLockManager) List(ctx context.Context) ([]*storage.LockInformation, error) {
	list, err := x.storage.List(ctx)
	if err != nil {
		return nil, err
	}
	lockSlice := make([]*storage.LockInformation, 0)
	for list.Next() {
		lockInformation := list.Value()
		lockSlice = append(lockSlice, lockInformation)
	}
	return lockSlice, nil
}

func (x *StorageLockManager) Delete(ctx context.Context, lockId string) error {
	get, err := x.storage.Get(ctx, lockId)
	if err != nil {
		return err
	}
	lockInformation, err := storage.LockInformationFromJsonString(get)
	if err != nil {
		return err
	}
	return x.storage.DeleteWithVersion(ctx, lockId, lockInformation.Version, lockInformation)
}

func (x *StorageLockManager) Clear(ctx context.Context, channel *message_channel.Channel[*storage.LockInformation]) error {
	list, err := x.storage.List(ctx)
	if err != nil {
		return err
	}
	for list.Next() {
		lockInformation := list.Value()
		err := x.storage.DeleteWithVersion(ctx, lockInformation.LockId, lockInformation.Version, lockInformation)
		if err != nil {
			return err
		}
		_ = channel.Send(ctx, lockInformation)
	}
	return nil
}

func (x *StorageLockManager) Lock(ctx context.Context, lockId, ownerId string) error {
	lock, err := storage_lock.NewStorageLock(x.storage, lockId)
	if err != nil {
		return err
	}
	return lock.Lock(ctx, ownerId)
}

// Unlock 使用给定的ownerId尝试解锁
func (x *StorageLockManager) Unlock(ctx context.Context, lockId, ownerId string) error {
	lock, err := storage_lock.NewStorageLock(x.storage, lockId)
	if err != nil {
		return err
	}
	return lock.UnLock(ctx, ownerId)
}

// Find 查询给定的锁的信息
func (x *StorageLockManager) Find(ctx context.Context, lockId string) (*storage.LockInformation, error) {
	jsonString, err := x.storage.Get(ctx, lockId)
	if err != nil {
		return nil, err
	}
	return storage.LockInformationFromJsonString(jsonString)
}
