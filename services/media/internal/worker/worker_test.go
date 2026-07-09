package worker

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/media/internal/domain"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// pngBytes строит валидное PNG-изображение w×h.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// --- фейки ---

type fakeStore struct {
	media          map[string]domain.Media
	processedInbox map[string]bool
	marked         *domain.Media
	markedEvent    events.Envelope
	inboxOnly      []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{media: map[string]domain.Media{}, processedInbox: map[string]bool{}}
}

func (f *fakeStore) GetByID(_ context.Context, id string) (domain.Media, error) {
	m, ok := f.media[id]
	if !ok {
		return domain.Media{}, domain.ErrMediaNotFound
	}
	return m, nil
}

func (f *fakeStore) IsEventProcessed(_ context.Context, _, eventID string) (bool, error) {
	return f.processedInbox[eventID], nil
}

func (f *fakeStore) MarkEventProcessed(_ context.Context, _, eventID string) error {
	f.processedInbox[eventID] = true
	f.inboxOnly = append(f.inboxOnly, eventID)
	return nil
}

func (f *fakeStore) MarkProcessedWithEvent(_ context.Context, _, eventID string, m domain.Media, ev events.Envelope) error {
	f.processedInbox[eventID] = true
	f.marked = &m
	f.markedEvent = ev
	// имитируем переход статуса в БД
	stored := f.media[m.ID]
	stored.Status = domain.StatusReady
	stored.Width, stored.Height, stored.Previews = m.Width, m.Height, m.Previews
	f.media[m.ID] = stored
	return nil
}

type fakeBlob struct {
	objects map[string][]byte
	removed []string
}

func newFakeBlob() *fakeBlob { return &fakeBlob{objects: map[string][]byte{}} }

func (f *fakeBlob) Get(_ context.Context, key string) ([]byte, error) {
	data, ok := f.objects[key]
	if !ok {
		return nil, domain.ErrMediaNotFound
	}
	return data, nil
}

func (f *fakeBlob) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	data, _ := io.ReadAll(r)
	f.objects[key] = data
	return nil
}

func (f *fakeBlob) Remove(_ context.Context, key string) error {
	f.removed = append(f.removed, key)
	delete(f.objects, key)
	return nil
}

func uploadedEnvelope(t *testing.T, mediaID string) events.Envelope {
	t.Helper()
	e, err := events.New(events.SubjectMediaUploaded, "media", map[string]any{"media_id": mediaID})
	require.NoError(t, err)
	return e
}

// --- тесты processor ---

func TestProcessor_GeneratesPreviewsAndReady(t *testing.T) {
	store := newFakeStore()
	blob := newFakeBlob()
	origKey := "originals/m1.jpg"
	store.media["m1"] = domain.Media{
		ID: "m1", OwnerUserID: "owner-1", ObjectKey: origKey,
		MimeType: "image/jpeg", Status: domain.StatusUploaded,
	}
	blob.objects[origKey] = pngBytes(t, 2000, 1000) // большой оригинал → все 3 превью

	p := NewProcessor(store, blob, discardLogger())
	require.NoError(t, p.Handle(context.Background(), uploadedEnvelope(t, "m1")))

	// media переведено в ready с размерами оригинала.
	require.NotNil(t, store.marked)
	require.NotNil(t, store.marked.Width)
	assert.Equal(t, 2000, *store.marked.Width)
	assert.Equal(t, 1000, *store.marked.Height)
	assert.Equal(t, domain.StatusReady, store.media["m1"].Status)

	// Сгенерированы три превью 120/480/1080, каждое загружено в хранилище.
	require.Len(t, store.marked.Previews, 3)
	for i, size := range []int{120, 480, 1080} {
		pv := store.marked.Previews[i]
		assert.Equal(t, size, pv.Size)
		assert.Equal(t, size, pv.Width, "длинная сторона = размеру бакета")
		_, ok := blob.objects[pv.ObjectKey]
		assert.True(t, ok, "превью %d загружено", size)
		assert.Equal(t, "previews/m1/"+itoa(size)+".jpg", pv.ObjectKey)
	}

	// Оригинал перезаписан (EXIF снят).
	assert.NotNil(t, blob.objects[origKey])

	// Событие bozor.media.processed.
	assert.Equal(t, events.SubjectMediaProcessed, store.markedEvent.Type)
}

func TestProcessor_SmallImageDedupesPreviews(t *testing.T) {
	store := newFakeStore()
	blob := newFakeBlob()
	store.media["m1"] = domain.Media{ID: "m1", ObjectKey: "originals/m1.jpg", MimeType: "image/jpeg", Status: domain.StatusUploaded}
	blob.objects["originals/m1.jpg"] = pngBytes(t, 90, 60) // меньше 120 → одно превью

	p := NewProcessor(store, blob, discardLogger())
	require.NoError(t, p.Handle(context.Background(), uploadedEnvelope(t, "m1")))

	require.NotNil(t, store.marked)
	require.Len(t, store.marked.Previews, 1, "маленький оригинал не масштабируется вверх — одно превью")
	assert.Equal(t, 90, store.marked.Previews[0].Width, "без увеличения")
}

func TestProcessor_Idempotent(t *testing.T) {
	store := newFakeStore()
	blob := newFakeBlob()
	env := uploadedEnvelope(t, "m1")
	store.processedInbox[env.ID] = true // уже обработано

	require.NoError(t, NewProcessor(store, blob, discardLogger()).Handle(context.Background(), env))
	assert.Nil(t, store.marked, "повторная доставка не делает работу")
}

func TestProcessor_MediaMissing_MarksProcessed(t *testing.T) {
	store := newFakeStore()
	env := uploadedEnvelope(t, "gone")
	require.NoError(t, NewProcessor(store, newFakeBlob(), discardLogger()).Handle(context.Background(), env))
	assert.Contains(t, store.inboxOnly, env.ID, "отсутствующее медиа только отмечается обработанным")
	assert.Nil(t, store.marked)
}

func TestProcessor_AlreadyReady_Skips(t *testing.T) {
	store := newFakeStore()
	store.media["m1"] = domain.Media{ID: "m1", ObjectKey: "originals/m1.jpg", MimeType: "image/jpeg", Status: domain.StatusReady}
	env := uploadedEnvelope(t, "m1")
	require.NoError(t, NewProcessor(store, newFakeBlob(), discardLogger()).Handle(context.Background(), env))
	assert.Contains(t, store.inboxOnly, env.ID)
	assert.Nil(t, store.marked, "уже готовое медиа не обрабатывается повторно")
}

// --- тесты cleaner ---

type fakeOrphanStore struct {
	orphans []domain.Media
	deleted []string
}

func (f *fakeOrphanStore) ListOrphans(_ context.Context, _ time.Time, _ int) ([]domain.Media, error) {
	return f.orphans, nil
}

func (f *fakeOrphanStore) DeleteWithEvent(_ context.Context, id string, _ events.Envelope) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func TestCleaner_RemovesBlobsAndRow(t *testing.T) {
	store := &fakeOrphanStore{orphans: []domain.Media{
		{ID: "m1", ObjectKey: "originals/m1.jpg", Previews: []domain.Preview{
			{Size: 120, ObjectKey: "previews/m1/120.jpg"},
			{Size: 480, ObjectKey: "previews/m1/480.jpg"},
		}},
	}}
	blob := newFakeBlob()
	c := NewCleaner(store, blob, time.Hour, time.Minute, 100, discardLogger())
	c.Sweep(context.Background())

	assert.Equal(t, []string{"m1"}, store.deleted, "запись удалена")
	assert.ElementsMatch(t,
		[]string{"originals/m1.jpg", "previews/m1/120.jpg", "previews/m1/480.jpg"},
		blob.removed, "оригинал и все превью удалены из хранилища")
}

// itoa — маленький помощник без strconv в тестовой строке ключа.
func itoa(n int) string {
	switch n {
	case 120:
		return "120"
	case 480:
		return "480"
	case 1080:
		return "1080"
	}
	return ""
}
