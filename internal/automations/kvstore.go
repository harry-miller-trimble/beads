package automations

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// KVStore provides per-plugin namespaced key-value storage backed by a JSON file.
// Keys are namespaced as "pluginName:key" to prevent cross-plugin access.
// Values are stored as base64-encoded bytes for arbitrary data support.
type KVStore struct {
	path string
	mu   sync.RWMutex
	data map[string][]byte
}

// NewKVStore creates a KV store backed by the given directory.
// The store file is created at dir/kv.json.
func NewKVStore(dir string) (*KVStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil { //nolint:gosec // plugin data dir
		return nil, fmt.Errorf("create kv dir: %w", err)
	}
	kv := &KVStore{
		path: filepath.Join(dir, "kv.json"),
		data: make(map[string][]byte),
	}
	// Load existing data if present.
	raw, err := os.ReadFile(kv.path) //nolint:gosec // path from trusted config
	if err == nil {
		_ = json.Unmarshal(raw, &kv.data)
	}
	return kv, nil
}

// Get retrieves a value by namespaced key. Returns nil if not found.
func (kv *KVStore) Get(key string) ([]byte, error) {
	kv.mu.RLock()
	defer kv.mu.RUnlock()
	val, ok := kv.data[key]
	if !ok {
		return nil, nil
	}
	return val, nil
}

// Set stores a value under a namespaced key and persists to disk.
func (kv *KVStore) Set(key string, value []byte) error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	kv.data[key] = append([]byte(nil), value...) // defensive copy
	return kv.flush()
}

// Delete removes a key and persists to disk.
func (kv *KVStore) Delete(key string) error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	delete(kv.data, key)
	return kv.flush()
}

// Keys returns all keys for a given plugin namespace.
func (kv *KVStore) Keys(pluginName string) []string {
	kv.mu.RLock()
	defer kv.mu.RUnlock()
	prefix := pluginName + ":"
	var keys []string
	for k := range kv.data {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			keys = append(keys, k[len(prefix):])
		}
	}
	return keys
}

// flush writes the current state to disk.
func (kv *KVStore) flush() error {
	data, err := json.MarshalIndent(kv.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal kv: %w", err)
	}
	return os.WriteFile(kv.path, data, 0600) //nolint:gosec // plugin data file
}
