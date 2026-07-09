package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/media/internal/app"
	"bozor/services/media/internal/domain"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeService подменяет use-cases медиа.
type fakeService struct {
	gotInput     app.UploadInput
	uploadErr    error
	getResult    app.Uploaded
	getErr       error
	gotRequester string
}

func (f *fakeService) Upload(_ context.Context, in app.UploadInput) (app.Uploaded, error) {
	f.gotInput = in
	if f.uploadErr != nil {
		return app.Uploaded{}, f.uploadErr
	}
	m := domain.Media{
		ID: "media-1", OwnerUserID: in.OwnerUserID, AdID: in.AdID, Bucket: "bozor-media",
		ObjectKey: "originals/media-1.png", MimeType: in.MimeType, SizeBytes: int64(len(in.Data)),
		Status: domain.StatusUploaded, CreatedAt: time.Unix(0, 0).UTC(),
	}
	return app.Uploaded{Media: m, PublicURL: "http://cdn/bozor-media/" + m.ObjectKey}, nil
}

func (f *fakeService) Get(_ context.Context, _, requesterID string) (app.Uploaded, error) {
	f.gotRequester = requesterID
	if f.getErr != nil {
		return app.Uploaded{}, f.getErr
	}
	return f.getResult, nil
}

func newRouter(svc Service) http.Handler {
	return NewRouter(Deps{Log: discardLogger(), Handler: NewHandler(svc, 10<<20, discardLogger())})
}

// pngData возвращает валидную для http.DetectContentType PNG-сигнатуру + паддинг.
func pngData(n int) []byte {
	d := make([]byte, n)
	copy(d, []byte("\x89PNG\r\n\x1a\n"))
	return d
}

// multipartUpload собирает multipart-тело с файлом (если content != nil) и полями.
func multipartUpload(t *testing.T, content []byte, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if content != nil {
		fw, err := w.CreateFormFile("file", "upload.bin")
		require.NoError(t, err)
		_, err = fw.Write(content)
		require.NoError(t, err)
	}
	for k, v := range fields {
		require.NoError(t, w.WriteField(k, v))
	}
	require.NoError(t, w.Close())
	return &buf, w.FormDataContentType()
}

// uploadReq строит POST /api/v1/media с multipart-телом и опциональным владельцем.
func uploadReq(t *testing.T, owner string, content []byte, fields map[string]string) *http.Request {
	t.Helper()
	body, contentType := multipartUpload(t, content, fields)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media", body)
	req.Header.Set("Content-Type", contentType)
	if owner != "" {
		req.Header.Set("X-User-Id", owner)
	}
	return req
}

func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUpload_AnonUnauthorized(t *testing.T) {
	svc := &fakeService{}
	rec := serve(newRouter(svc), uploadReq(t, "", pngData(200), nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "unauthorized")
	assert.Empty(t, svc.gotInput.OwnerUserID, "аноним не доходит до use-case")
}

func TestUpload_HappyPath(t *testing.T) {
	svc := &fakeService{}
	rec := serve(newRouter(svc), uploadReq(t, "owner-1", pngData(200), map[string]string{"ad_id": "ad-9"}))
	require.Equal(t, http.StatusCreated, rec.Code)

	assert.Equal(t, "owner-1", svc.gotInput.OwnerUserID)
	require.NotNil(t, svc.gotInput.AdID)
	assert.Equal(t, "ad-9", *svc.gotInput.AdID)
	assert.Equal(t, "image/png", svc.gotInput.MimeType, "тип определён по содержимому")

	var resp mediaResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "media-1", resp.ID)
	assert.Contains(t, resp.URL, "originals/media-1.png")
	assert.Equal(t, "uploaded", resp.Status)
}

func TestUpload_MissingFile(t *testing.T) {
	svc := &fakeService{}
	rec := serve(newRouter(svc), uploadReq(t, "owner-1", nil, map[string]string{"ad_id": "ad-9"}))
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "missing_file")
}

func TestUpload_UnsupportedType_SniffedFromContent(t *testing.T) {
	// Клиент шлёт PDF; сервер обязан определить тип по содержимому, а не по имени.
	svc := &fakeService{uploadErr: domain.ErrUnsupportedType}
	rec := serve(newRouter(svc), uploadReq(t, "owner-1", []byte("%PDF-1.4\n1 0 obj\n"), nil))
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "unsupported_type")
	assert.Equal(t, "application/pdf", svc.gotInput.MimeType, "тип взят из содержимого (sniff), не от клиента")
}

func TestUpload_AdLimitConflict(t *testing.T) {
	svc := &fakeService{uploadErr: domain.ErrAdMediaLimit}
	rec := serve(newRouter(svc), uploadReq(t, "owner-1", pngData(200), map[string]string{"ad_id": "ad-9"}))
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "ad_media_limit")
}

func TestUpload_TooLarge_BodyLimit(t *testing.T) {
	// Крошечный лимит тела: MaxBytesReader обрывает загрузку → 422 file_too_large,
	// use-case при этом не вызывается.
	svc := &fakeService{}
	h := &Handler{svc: svc, maxUpload: 64, log: discardLogger()}
	router := NewRouter(Deps{Log: discardLogger(), Handler: h})
	rec := serve(router, uploadReq(t, "owner-1", pngData(4096), nil))
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "file_too_large")
	assert.Empty(t, svc.gotInput.OwnerUserID, "тело оборвано до вызова use-case")
}

func TestGet_OK(t *testing.T) {
	svc := &fakeService{getResult: app.Uploaded{
		Media: domain.Media{
			ID: "m1", OwnerUserID: "o", Bucket: "bozor-media", ObjectKey: "originals/m1.png",
			MimeType: "image/png", SizeBytes: 200, Status: domain.StatusUploaded, CreatedAt: time.Unix(0, 0).UTC(),
		},
		PublicURL: "http://cdn/bozor-media/originals/m1.png",
	}}
	rec := serve(newRouter(svc), httptest.NewRequest(http.MethodGet, "/api/v1/media/m1", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp mediaResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "m1", resp.ID)
	assert.Equal(t, "http://cdn/bozor-media/originals/m1.png", resp.URL)
	assert.Equal(t, "image/png", resp.MimeType)
}

func TestGet_NotFound(t *testing.T) {
	svc := &fakeService{getErr: domain.ErrMediaNotFound}
	rec := serve(newRouter(svc), httptest.NewRequest(http.MethodGet, "/api/v1/media/nope", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "media_not_found")
}

func TestGet_PreviewsAndOwnerURL(t *testing.T) {
	svc := &fakeService{getResult: app.Uploaded{
		Media: domain.Media{
			ID: "m1", OwnerUserID: "owner-1", ObjectKey: "originals/m1.jpg",
			MimeType: "image/jpeg", SizeBytes: 200, Status: domain.StatusReady, CreatedAt: time.Unix(0, 0).UTC(),
		},
		PublicURL:   "http://cdn/bozor-media/originals/m1.jpg",
		OriginalURL: "http://cdn/bozor-media/originals/m1.jpg?signed=1",
		Previews: []app.PreviewView{
			{Size: 120, Width: 120, Height: 90, URL: "http://cdn/bozor-media/previews/m1/120.jpg"},
			{Size: 480, Width: 480, Height: 360, URL: "http://cdn/bozor-media/previews/m1/480.jpg"},
		},
	}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/m1", nil)
	req.Header.Set("X-User-Id", "owner-1")
	rec := serve(newRouter(svc), req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "owner-1", svc.gotRequester, "идентичность проброшена в use-case")

	var resp mediaResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ready", resp.Status)
	assert.Equal(t, "http://cdn/bozor-media/originals/m1.jpg?signed=1", resp.OriginalURL)
	require.Len(t, resp.Previews, 2)
	assert.Equal(t, 480, resp.Previews[1].Size)
	assert.Contains(t, resp.Previews[0].URL, "previews/m1/120.jpg")
}
