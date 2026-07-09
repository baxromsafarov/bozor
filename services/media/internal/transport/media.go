package transport

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/authx"
	"bozor/pkg/shared/httpx"

	"bozor/services/media/internal/app"
	"bozor/services/media/internal/domain"
)

// sniffLen — сколько байт читаем для определения типа содержимого.
const sniffLen = 512

// Service — use-cases медиа (реализуется app.Service).
type Service interface {
	Upload(ctx context.Context, in app.UploadInput) (app.Uploaded, error)
	Get(ctx context.Context, id, requesterID string) (app.Uploaded, error)
}

// Handler обслуживает эндпоинты медиа.
type Handler struct {
	svc       Service
	maxUpload int64 // предел тела запроса загрузки (файл + overhead multipart)
	log       *slog.Logger
}

// NewHandler создаёт обработчик медиа.
func NewHandler(svc Service, maxFileBytes int64, log *slog.Logger) *Handler {
	return &Handler{svc: svc, maxUpload: maxFileBytes + (1 << 20), log: log}
}

type mediaResponse struct {
	ID          string            `json:"id"`
	URL         string            `json:"url"`
	OriginalURL string            `json:"original_url,omitempty"` // presigned, только владельцу
	AdID        *string           `json:"ad_id,omitempty"`
	MimeType    string            `json:"mime_type"`
	SizeBytes   int64             `json:"size_bytes"`
	Status      string            `json:"status"`
	Width       *int              `json:"width,omitempty"`
	Height      *int              `json:"height,omitempty"`
	Previews    []previewResponse `json:"previews,omitempty"`
	CreatedAt   string            `json:"created_at"`
}

type previewResponse struct {
	Size   int    `json:"size"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	URL    string `json:"url"`
}

// Upload принимает multipart-файл, сохраняет оригинал и создаёт запись.
// Требует аутентификации (владелец берётся из проброшенной идентичности).
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	owner := authx.UserID(r.Context())
	if owner == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "unauthorized",
			"Требуется авторизация", "Avtorizatsiya talab qilinadi"))
		return
	}

	// Ограничиваем тело запроса, чтобы огромная загрузка не съела память.
	r.Body = http.MaxBytesReader(w, r.Body, h.maxUpload)

	file, _, err := r.FormFile("file")
	if err != nil {
		if errors.As(err, new(*http.MaxBytesError)) {
			h.writeMediaError(w, r, domain.ErrFileTooLarge)
			return
		}
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "missing_file",
			"Ожидается файл в поле file", "file maydonida fayl kutilmoqda"))
		return
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		if errors.As(err, new(*http.MaxBytesError)) {
			h.writeMediaError(w, r, domain.ErrFileTooLarge)
			return
		}
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "read_failed",
			"Не удалось прочитать файл", "Faylni o'qib bo'lmadi"))
		return
	}

	var adID *string
	if v := r.FormValue("ad_id"); v != "" {
		adID = &v
	}

	up, err := h.svc.Upload(r.Context(), app.UploadInput{
		OwnerUserID: owner, AdID: adID,
		MimeType: sniffType(data), Data: data,
	})
	if err != nil {
		h.writeMediaError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusCreated, toResponse(up))
}

// Get отдаёт метаданные медиа по id (публично; владельцу — presigned-оригинал).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	up, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"), authx.UserID(r.Context()))
	if err != nil {
		h.writeMediaError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toResponse(up))
}

// sniffType определяет MIME-тип по содержимому (не доверяя заголовку клиента).
func sniffType(data []byte) string {
	if len(data) > sniffLen {
		data = data[:sniffLen]
	}
	return http.DetectContentType(data)
}

// writeMediaError переводит доменные ошибки в ответы RFC 7807.
func (h *Handler) writeMediaError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrMediaNotFound):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindNotFound, "media_not_found",
			"Медиа не найдено", "Media topilmadi"))
	case errors.Is(err, domain.ErrEmptyFile):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "empty_file",
			"Пустой файл", "Bo'sh fayl"))
	case errors.Is(err, domain.ErrUnsupportedType):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "unsupported_type",
			"Поддерживаются только JPEG, PNG, WebP", "Faqat JPEG, PNG, WebP qo'llab-quvvatlanadi"))
	case errors.Is(err, domain.ErrFileTooLarge):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "file_too_large",
			"Файл превышает допустимый размер", "Fayl hajmi ruxsat etilgandan katta"))
	case errors.Is(err, domain.ErrAdMediaLimit):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindConflict, "ad_media_limit",
			"Превышен лимит изображений на объявление", "E'lon uchun rasm limiti oshib ketdi"))
	default:
		h.log.ErrorContext(r.Context(), "ошибка медиа", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "media_failed",
			"Внутренняя ошибка", "Ichki xatolik"))
	}
}

func toResponse(up app.Uploaded) mediaResponse {
	m := up.Media
	resp := mediaResponse{
		ID: m.ID, URL: up.PublicURL, OriginalURL: up.OriginalURL, AdID: m.AdID, MimeType: m.MimeType,
		SizeBytes: m.SizeBytes, Status: string(m.Status), Width: m.Width, Height: m.Height,
		CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339),
	}
	for _, p := range up.Previews {
		resp.Previews = append(resp.Previews, previewResponse{
			Size: p.Size, Width: p.Width, Height: p.Height, URL: p.URL,
		})
	}
	return resp
}
