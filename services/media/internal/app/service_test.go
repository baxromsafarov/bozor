package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/media/internal/domain"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeStore struct {
	countByAd int
	inserted  *domain.Media
	events    []events.Envelope
	insertErr error
	getByID   map[string]domain.Media
}

func (f *fakeStore) CountByAd(context.Context, string) (int, error) { return f.countByAd, nil }

func (f *fakeStore) InsertWithEvent(_ context.Context, m domain.Media, ev events.Envelope) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.inserted = &m
	f.events = append(f.events, ev)
	return nil
}

func (f *fakeStore) GetByID(_ context.Context, id string) (domain.Media, error) {
	m, ok := f.getByID[id]
	if !ok {
		return domain.Media{}, domain.ErrMediaNotFound
	}
	return m, nil
}

type fakeBlob struct {
	putKey   string
	putBytes int64
	removed  []string
	putErr   error
}

func (f *fakeBlob) Bucket() string { return "bozor-media" }

func (f *fakeBlob) Put(_ context.Context, key string, r io.Reader, size int64, _ string) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.putKey = key
	f.putBytes = size
	_, _ = io.Copy(io.Discard, r)
	return nil
}

func (f *fakeBlob) Remove(_ context.Context, key string) error {
	f.removed = append(f.removed, key)
	return nil
}

func (f *fakeBlob) PublicURL(key string) string { return "http://cdn/bozor-media/" + key }

func newSvc(store *fakeStore, blob *fakeBlob) *Service {
	return NewService(store, blob, domain.Limits{MaxSizeBytes: 1000, MaxPerAd: 3}, discardLogger())
}

func pngData(n int) []byte {
	d := make([]byte, n)
	copy(d, []byte("\x89PNG\r\n\x1a\n"))
	return d
}

func TestUpload_HappyPath(t *testing.T) {
	store := &fakeStore{}
	blob := &fakeBlob{}
	up, err := newSvc(store, blob).Upload(context.Background(), UploadInput{
		OwnerUserID: "owner-1", MimeType: "image/png", Data: pngData(200),
	})
	require.NoError(t, err)
	assert.Equal(t, "owner-1", up.Media.OwnerUserID)
	assert.Equal(t, domain.StatusUploaded, up.Media.Status)
	assert.Equal(t, int64(200), up.Media.SizeBytes)
	assert.Contains(t, up.Media.ObjectKey, "originals/")
	assert.Contains(t, up.Media.ObjectKey, ".png")
	assert.Equal(t, up.Media.ObjectKey, blob.putKey, "объект залит по тому же ключу")
	assert.Equal(t, int64(200), blob.putBytes)
	require.NotNil(t, store.inserted)
	require.Len(t, store.events, 1)
	assert.Equal(t, events.SubjectMediaUploaded, store.events[0].Type)
	assert.Contains(t, up.PublicURL, up.Media.ObjectKey)
}

func TestUpload_MissingOwner(t *testing.T) {
	_, err := newSvc(&fakeStore{}, &fakeBlob{}).Upload(context.Background(), UploadInput{
		MimeType: "image/png", Data: pngData(10),
	})
	assert.ErrorIs(t, err, domain.ErrMissingOwner)
}

func TestUpload_UnsupportedType(t *testing.T) {
	blob := &fakeBlob{}
	_, err := newSvc(&fakeStore{}, blob).Upload(context.Background(), UploadInput{
		OwnerUserID: "o", MimeType: "application/pdf", Data: []byte("%PDF-1.4"),
	})
	assert.ErrorIs(t, err, domain.ErrUnsupportedType)
	assert.Empty(t, blob.putKey, "невалидный тип не заливается в хранилище")
}

func TestUpload_TooLarge(t *testing.T) {
	_, err := newSvc(&fakeStore{}, &fakeBlob{}).Upload(context.Background(), UploadInput{
		OwnerUserID: "o", MimeType: "image/png", Data: pngData(1001),
	})
	assert.ErrorIs(t, err, domain.ErrFileTooLarge)
}

func TestUpload_AdLimitExceeded(t *testing.T) {
	ad := "ad-1"
	store := &fakeStore{countByAd: 3} // лимит MaxPerAd=3
	blob := &fakeBlob{}
	_, err := newSvc(store, blob).Upload(context.Background(), UploadInput{
		OwnerUserID: "o", AdID: &ad, MimeType: "image/png", Data: pngData(10),
	})
	assert.ErrorIs(t, err, domain.ErrAdMediaLimit)
	assert.Empty(t, blob.putKey, "при превышении лимита не заливаем")
}

func TestUpload_CompensatesOnInsertFailure(t *testing.T) {
	store := &fakeStore{insertErr: errors.New("db down")}
	blob := &fakeBlob{}
	_, err := newSvc(store, blob).Upload(context.Background(), UploadInput{
		OwnerUserID: "o", MimeType: "image/png", Data: pngData(10),
	})
	require.Error(t, err)
	require.Len(t, blob.removed, 1, "объект удалён из хранилища после сбоя вставки")
	assert.Equal(t, blob.putKey, blob.removed[0])
}

func TestGet_NotFound(t *testing.T) {
	_, err := newSvc(&fakeStore{getByID: map[string]domain.Media{}}, &fakeBlob{}).
		Get(context.Background(), "nope")
	assert.ErrorIs(t, err, domain.ErrMediaNotFound)
}

func TestGet_ReturnsPublicURL(t *testing.T) {
	store := &fakeStore{getByID: map[string]domain.Media{
		"m1": {ID: "m1", ObjectKey: "originals/m1.png", Status: domain.StatusUploaded},
	}}
	up, err := newSvc(store, &fakeBlob{}).Get(context.Background(), "m1")
	require.NoError(t, err)
	assert.Equal(t, "http://cdn/bozor-media/originals/m1.png", up.PublicURL)
}
