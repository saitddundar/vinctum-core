package storage_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/saitddundar/vinctum-core/services/transfer/storage"
)

func tempStore(t *testing.T) *storage.FileStore {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "vinctum-test-chunks-"+t.Name())
	t.Cleanup(func() { os.RemoveAll(dir) })

	store, err := storage.NewFileStore(dir)
	require.NoError(t, err)
	return store
}

func TestSaveAndLoadChunk(t *testing.T) {
	s := tempStore(t)
	data := []byte("hello vinctum chunk data")

	hash, err := s.SaveChunk("tx-1", 0, data)
	require.NoError(t, err)
	assert.NotEmpty(t, hash)

	loaded, err := s.LoadChunk("tx-1", 0)
	require.NoError(t, err)
	assert.Equal(t, data, loaded)
}

func TestListChunks(t *testing.T) {
	s := tempStore(t)

	for i := int32(0); i < 5; i++ {
		_, err := s.SaveChunk("tx-2", i, []byte{byte(i)})
		require.NoError(t, err)
	}

	indices, err := s.ListChunks("tx-2")
	require.NoError(t, err)
	assert.Equal(t, []int32{0, 1, 2, 3, 4}, indices)
}

func TestListChunks_Empty(t *testing.T) {
	s := tempStore(t)

	indices, err := s.ListChunks("nonexistent")
	require.NoError(t, err)
	assert.Empty(t, indices)
}

func TestDeleteTransfer(t *testing.T) {
	s := tempStore(t)

	_, _ = s.SaveChunk("tx-3", 0, []byte("data"))
	_, _ = s.SaveChunk("tx-3", 1, []byte("more"))

	err := s.DeleteTransfer("tx-3")
	require.NoError(t, err)

	indices, err := s.ListChunks("tx-3")
	require.NoError(t, err)
	assert.Empty(t, indices)
}

func TestReassemble(t *testing.T) {
	s := tempStore(t)

	chunks := [][]byte{
		[]byte("hello "),
		[]byte("vinctum "),
		[]byte("world"),
	}
	for i, c := range chunks {
		_, err := s.SaveChunk("tx-4", int32(i), c)
		require.NoError(t, err)
	}

	var buf bytes.Buffer
	err := s.Reassemble("tx-4", &buf)
	require.NoError(t, err)
	assert.Equal(t, "hello vinctum world", buf.String())
}

func TestLoadChunk_NotFound(t *testing.T) {
	s := tempStore(t)

	_, err := s.LoadChunk("tx-nope", 0)
	assert.Error(t, err)
}
