package worker

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type fakeFlushStore struct {
	added map[string]int64
	err   error
}

func (f *fakeFlushStore) AddViews(_ context.Context, counts map[string]int64) error {
	f.added = counts
	return f.err
}

type fakeBuffer struct {
	drain    map[string]int64
	drainErr error
	restored map[string]int64
}

func (f *fakeBuffer) Drain(context.Context) (map[string]int64, error) {
	return f.drain, f.drainErr
}

func (f *fakeBuffer) Restore(_ context.Context, counts map[string]int64) error {
	f.restored = counts
	return nil
}

func TestViewFlusher_Flush(t *testing.T) {
	store := &fakeFlushStore{}
	buf := &fakeBuffer{drain: map[string]int64{"a": 3, "b": 5}}
	NewViewFlusher(store, buf, time.Minute, discardLogger()).Flush(context.Background())
	assert.Equal(t, map[string]int64{"a": 3, "b": 5}, store.added, "снятые счётчики записаны пачкой")
	assert.Nil(t, buf.restored, "успешный флеш не возвращает буфер")
}

func TestViewFlusher_EmptyNoop(t *testing.T) {
	store := &fakeFlushStore{}
	buf := &fakeBuffer{drain: map[string]int64{}}
	NewViewFlusher(store, buf, time.Minute, discardLogger()).Flush(context.Background())
	assert.Nil(t, store.added, "пустой буфер — БД не трогаем")
}

func TestViewFlusher_DBErrorRestores(t *testing.T) {
	store := &fakeFlushStore{err: assertErr}
	buf := &fakeBuffer{drain: map[string]int64{"a": 2}}
	NewViewFlusher(store, buf, time.Minute, discardLogger()).Flush(context.Background())
	assert.Equal(t, map[string]int64{"a": 2}, buf.restored, "при сбое БД счётчики возвращаются в буфер")
}

func TestViewFlusher_DrainErrorNoWrite(t *testing.T) {
	store := &fakeFlushStore{}
	buf := &fakeBuffer{drainErr: assertErr}
	NewViewFlusher(store, buf, time.Minute, discardLogger()).Flush(context.Background())
	assert.Nil(t, store.added, "ошибка снятия буфера — БД не трогаем")
}
