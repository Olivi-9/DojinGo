package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"DojinGo/internal/config"
)

type Store interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

func New(cfg config.StorageConfig) (Store, error) {
	switch cfg.Type {
	case "memory":
		return NewMemoryStore(cfg.MaxEntries), nil
	case "file":
		return NewFileStore(cfg.Path, cfg.MaxEntries)
	default:
		return nil, fmt.Errorf("unsupported storage type %q", cfg.Type)
	}
}

type memoryEntry struct {
	Value      string
	ExpiresAt  time.Time
	LastAccess time.Time
}

type MemoryStore struct {
	mu         sync.RWMutex
	maxEntries int
	entries    map[string]memoryEntry
}

func NewMemoryStore(maxEntries int) *MemoryStore {
	if maxEntries <= 0 {
		maxEntries = 1024
	}
	return &MemoryStore{
		maxEntries: maxEntries,
		entries:    make(map[string]memoryEntry, maxEntries),
	}
}

func (s *MemoryStore) Get(_ context.Context, key string) (string, bool, error) {
	now := time.Now()

	s.mu.RLock()
	entry, ok := s.entries[key]
	s.mu.RUnlock()
	if !ok {
		return "", false, nil
	}
	if !entry.ExpiresAt.IsZero() && now.After(entry.ExpiresAt) {
		s.mu.Lock()
		delete(s.entries, key)
		s.mu.Unlock()
		return "", false, nil
	}

	entry.LastAccess = now
	s.mu.Lock()
	s.entries[key] = entry
	s.mu.Unlock()
	return entry.Value, true, nil
}

func (s *MemoryStore) Set(_ context.Context, key, value string, ttl time.Duration) error {
	now := time.Now()
	entry := memoryEntry{
		Value:      value,
		LastAccess: now,
	}
	if ttl > 0 {
		entry.ExpiresAt = now.Add(ttl)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = entry
	if len(s.entries) > s.maxEntries {
		s.evictOldestLocked()
	}
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	delete(s.entries, key)
	s.mu.Unlock()
	return nil
}

func (s *MemoryStore) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	first := true
	for key, entry := range s.entries {
		if first || entry.LastAccess.Before(oldest) {
			oldestKey = key
			oldest = entry.LastAccess
			first = false
		}
	}
	if !first {
		delete(s.entries, oldestKey)
	}
}

type fileEntry struct {
	Value      string    `json:"value"`
	ExpiresAt  time.Time `json:"expires_at"`
	LastAccess time.Time `json:"last_access"`
}

type FileStore struct {
	mu         sync.Mutex
	dir        string
	maxEntries int
}

func NewFileStore(dir string, maxEntries int) (*FileStore, error) {
	if dir == "" {
		return nil, errors.New("storage path is required for file storage")
	}
	if maxEntries <= 0 {
		maxEntries = 1024
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir %q: %w", dir, err)
	}
	return &FileStore{dir: dir, maxEntries: maxEntries}, nil
}

func (s *FileStore) Get(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.pathForKey(key)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read cache file %q: %w", path, err)
	}

	var entry fileEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return "", false, fmt.Errorf("decode cache file %q: %w", path, err)
	}
	if !entry.ExpiresAt.IsZero() && time.Now().After(entry.ExpiresAt) {
		_ = os.Remove(path)
		return "", false, nil
	}

	entry.LastAccess = time.Now()
	if err := s.writeLocked(path, entry); err != nil {
		return "", false, err
	}
	return entry.Value, true, nil
}

func (s *FileStore) Set(_ context.Context, key, value string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := fileEntry{
		Value:      value,
		LastAccess: time.Now(),
	}
	if ttl > 0 {
		entry.ExpiresAt = time.Now().Add(ttl)
	}

	if err := s.writeLocked(s.pathForKey(key), entry); err != nil {
		return err
	}
	return s.evictIfNeededLocked()
}

func (s *FileStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.pathForKey(key)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete cache file %q: %w", path, err)
	}
	return nil
}

func (s *FileStore) pathForKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".json")
}

func (s *FileStore) writeLocked(path string, entry fileEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode cache entry: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write cache file %q: %w", path, err)
	}
	return nil
}

func (s *FileStore) evictIfNeededLocked() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("read cache dir %q: %w", s.dir, err)
	}
	if len(entries) <= s.maxEntries {
		return nil
	}

	type candidate struct {
		Name       string
		LastAccess time.Time
	}
	var oldest candidate
	first := true
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(s.dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var decoded fileEntry
		if err := json.Unmarshal(data, &decoded); err != nil {
			continue
		}
		if first || decoded.LastAccess.Before(oldest.LastAccess) {
			oldest = candidate{Name: path, LastAccess: decoded.LastAccess}
			first = false
		}
	}
	if !first {
		_ = os.Remove(oldest.Name)
	}
	return nil
}
