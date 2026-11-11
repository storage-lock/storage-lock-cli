package manager

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/golang-infrastructure/go-iterator"
	message_channel "github.com/golang-infrastructure/go-message-channel"
	"github.com/storage-lock/go-storage"
	storage_lock "github.com/storage-lock/go-storage-lock"
)

// fenceCAS 在同一把锁里先核对版本再写入，供管理入口测试驱动真正的 StorageLock。
type fenceCAS struct {
	mu   sync.Mutex
	data map[string]fenceRecord
}

type fenceRecord struct {
	version storage.Version
	json    string
}

func newFenceCAS() *fenceCAS {
	return &fenceCAS{data: make(map[string]fenceRecord)}
}

func (s *fenceCAS) GetName() string { return "fence-cas" }

func (s *fenceCAS) Capabilities() []storage.StorageCapability {
	return []storage.StorageCapability{
		storage.CapabilityCAS,
		storage.CapabilityReliableTime,
	}
}

func (s *fenceCAS) Init(context.Context) error  { return nil }
func (s *fenceCAS) Close(context.Context) error { return nil }

func (s *fenceCAS) GetTime(context.Context) (time.Time, error) { return time.Now(), nil }

func (s *fenceCAS) Get(_ context.Context, lockID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.data[lockID]
	if !ok {
		return "", storage_lock.ErrLockNotFound
	}
	return rec.json, nil
}

func (s *fenceCAS) CreateWithVersion(_ context.Context, lockID string, version storage.Version, info *storage.LockInformation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[lockID]; ok {
		return storage_lock.ErrLockAlreadyExists
	}
	s.data[lockID] = fenceRecord{version: version, json: info.ToJsonString()}
	return nil
}

func (s *fenceCAS) UpdateWithVersion(_ context.Context, lockID string, expected, newVersion storage.Version, info *storage.LockInformation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.data[lockID]
	if !ok || rec.version != expected {
		return storage_lock.ErrVersionMiss
	}
	s.data[lockID] = fenceRecord{version: newVersion, json: info.ToJsonString()}
	return nil
}

func (s *fenceCAS) DeleteWithVersion(_ context.Context, lockID string, expected storage.Version, _ *storage.LockInformation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.data[lockID]
	if !ok {
		return storage_lock.ErrLockNotFound
	}
	if rec.version != expected {
		return storage_lock.ErrVersionMiss
	}
	delete(s.data, lockID)
	return nil
}

func (s *fenceCAS) List(context.Context) (iterator.Iterator[*storage.LockInformation], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	slice := make([]*storage.LockInformation, 0, len(s.data))
	for _, rec := range s.data {
		info, err := storage.LockInformationFromJsonString(rec.json)
		if err != nil {
			return nil, err
		}
		slice = append(slice, info)
	}
	return iterator.FromSlice(slice), nil
}

func TestDeleteAndClearDoNotRewindFencing(t *testing.T) {
	ctx := context.Background()
	mgr := NewStorageLockManager(newFenceCAS())

	if err := mgr.Lock(ctx, "fence", "owner"); err != nil {
		t.Fatal(err)
	}
	before, err := mgr.Find(ctx, "fence")
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Delete(ctx, "fence"); err == nil {
		t.Fatal("delete physically removed or reported success")
	}
	kept, err := mgr.Find(ctx, "fence")
	if err != nil {
		t.Fatalf("record disappeared after delete: %v", err)
	}
	if kept.Version != before.Version || kept.LockId != "fence" {
		t.Fatalf("delete rewrote the record: before=%d after=%d", before.Version, kept.Version)
	}
	if err := mgr.Unlock(ctx, "fence", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Lock(ctx, "fence", "next"); err != nil {
		t.Fatal(err)
	}
	after, err := mgr.Find(ctx, "fence")
	if err != nil {
		t.Fatal(err)
	}
	if after.Version <= before.Version {
		t.Fatalf("fencing version rewound: before=%d after=%d", before.Version, after.Version)
	}

	if err := mgr.Lock(ctx, "other", "owner"); err != nil {
		t.Fatal(err)
	}
	otherBefore, err := mgr.Find(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	channel := message_channel.NewChannel[*storage.LockInformation](message_channel.NewChannelOptions[*storage.LockInformation]())
	if err := mgr.Clear(ctx, channel); err == nil {
		t.Fatal("clear physically removed or reported success")
	}
	if _, err := mgr.Find(ctx, "fence"); err != nil {
		t.Fatalf("clear removed fence: %v", err)
	}
	if _, err := mgr.Find(ctx, "other"); err != nil {
		t.Fatalf("clear removed other: %v", err)
	}
	if err := mgr.Unlock(ctx, "other", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Lock(ctx, "other", "next"); err != nil {
		t.Fatal(err)
	}
	otherAfter, err := mgr.Find(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	if otherAfter.Version <= otherBefore.Version {
		t.Fatalf("clear rewound fencing: before=%d after=%d", otherBefore.Version, otherAfter.Version)
	}
}
