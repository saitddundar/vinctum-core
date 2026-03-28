// Package storage provides a chunk storage backend for the transfer service.
// Chunks are stored on the local filesystem under a configurable base directory,
// organised as: <base>/<transfer_id>/<chunk_index>.chunk
package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type ChunkStore interface {
	SaveChunk(transferID string, chunkIndex int32, data []byte) (hash string, err error)

	LoadChunk(transferID string, chunkIndex int32) (data []byte, err error)

	ListChunks(transferID string) ([]int32, error)

	DeleteTransfer(transferID string) error
}

type FileStore struct {
	baseDir string
	mu      sync.RWMutex
}

func NewFileStore(baseDir string) (*FileStore, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create base dir: %w", err)
	}
	return &FileStore{baseDir: baseDir}, nil
}

func (fs *FileStore) transferDir(transferID string) string {
	return filepath.Join(fs.baseDir, transferID)
}

func (fs *FileStore) chunkPath(transferID string, chunkIndex int32) string {
	return filepath.Join(fs.transferDir(transferID), fmt.Sprintf("%06d.chunk", chunkIndex))
}

func (fs *FileStore) SaveChunk(transferID string, chunkIndex int32, data []byte) (string, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	dir := fs.transferDir(transferID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create transfer dir: %w", err)
	}

	path := fs.chunkPath(transferID, chunkIndex)

	// Write to a temp file first, then rename for atomicity.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", fmt.Errorf("write chunk: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("rename chunk: %w", err)
	}

	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

func (fs *FileStore) LoadChunk(transferID string, chunkIndex int32) ([]byte, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	path := fs.chunkPath(transferID, chunkIndex)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read chunk: %w", err)
	}
	return data, nil
}

func (fs *FileStore) ListChunks(transferID string) ([]int32, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	dir := fs.transferDir(transferID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read transfer dir: %w", err)
	}

	var indices []int32
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".chunk") {
			continue
		}
		numStr := strings.TrimSuffix(name, ".chunk")
		n, err := strconv.Atoi(numStr)
		if err != nil {
			continue
		}
		indices = append(indices, int32(n))
	}
	sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })
	return indices, nil
}

func (fs *FileStore) DeleteTransfer(transferID string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	dir := fs.transferDir(transferID)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete transfer dir: %w", err)
	}
	return nil
}

func (fs *FileStore) Reassemble(transferID string, w io.Writer) error {
	indices, err := fs.ListChunks(transferID)
	if err != nil {
		return err
	}

	for _, idx := range indices {
		data, err := fs.LoadChunk(transferID, idx)
		if err != nil {
			return fmt.Errorf("load chunk %d: %w", idx, err)
		}
		if _, err := w.Write(data); err != nil {
			return fmt.Errorf("write chunk %d: %w", idx, err)
		}
	}
	return nil
}
