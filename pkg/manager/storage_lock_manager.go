package manager

import (
	"context"
	message_channel "github.com/golang-infrastructure/go-message-channel"
	"github.com/storage-lock/go-storage-lock/pkg/storage"
	"github.com/storage-lock/go-storage-lock/pkg/storage_lock"
)

type StorageLockManager struct {
	storage storage.Storage
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
	lockInformation, err := storage.FromJsonString(get)
	return x.storage.DeleteWithVersion(ctx, lockId, lockInformation.Version, lockInformation)
}

func (x *StorageLockManager) Clear(ctx context.Context, channel message_channel.Channel[*storage.LockInformation]) error {
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

}

// Unlock 使用给定的ownerId尝试解锁
func (x *StorageLockManager) Unlock(ctx context.Context, lockId, ownerId string) error {
	lockInformation, err := x.Find(ctx, lockId)
	if err != nil {
		return err
	}
	lock := storage_lock.NewStorageLock(x.storage)
	lock.UnLock()
	
}

// Find 查询给定的锁的信息
func (x *StorageLockManager) Find(ctx context.Context, lockId string) (*storage.LockInformation, error) {
	jsonString, err := x.storage.Get(ctx, lockId)
	if err != nil {
		return nil, err
	}
	return storage.FromJsonString(jsonString)
}
