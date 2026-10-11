package cmd

import (
	"time"

	storage_lock "github.com/storage-lock/go-storage-lock"
)

func storageLockFor(lockID string) (*storage_lock.StorageLock, error) {
	options := storage_lock.NewStorageLockOptionsWithLockId(lockID)
	return storage_lock.NewStorageLockWithOptions(rootStorage, options)
}

func lockCleanupTimeout() time.Duration {
	if timeout <= 0 {
		return storage_lock.DefaultUnlockTimeout
	}
	if timeout < storage_lock.DefaultUnlockTimeout {
		return timeout
	}
	return storage_lock.DefaultUnlockTimeout
}
