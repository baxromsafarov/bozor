package transport

import (
	"errors"
	"log/slog"
	"net/http"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"

	"bozor/services/catalog/internal/domain"
)

// writeCatalogError переводит доменные ошибки каталога в ответы RFC 7807.
func writeCatalogError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	switch {
	case errors.Is(err, domain.ErrCategoryNotFound):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindNotFound, "category_not_found",
			"Категория не найдена", "Kategoriya topilmadi"))
	case errors.Is(err, domain.ErrParentNotFound):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "parent_not_found",
			"Родительская категория не найдена", "Ota kategoriya topilmadi"))
	case errors.Is(err, domain.ErrSlugConflict):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindConflict, "slug_conflict",
			"Категория с таким slug уже существует", "Bunday slug allaqachon mavjud"))
	case errors.Is(err, domain.ErrHasChildren):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindConflict, "has_children",
			"Сначала удалите подкатегории", "Avval quyi kategoriyalarni o'chiring"))
	case errors.Is(err, domain.ErrAttributeNotFound):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindNotFound, "attribute_not_found",
			"Атрибут не найден", "Atribut topilmadi"))
	case errors.Is(err, domain.ErrAttributeSlugConflict):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindConflict, "attribute_slug_conflict",
			"Атрибут с таким slug уже существует", "Bunday slug bilan atribut mavjud"))
	case errors.Is(err, domain.ErrOptionSlugConflict):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindConflict, "option_slug_conflict",
			"Вариант с таким slug уже существует", "Bunday slug bilan variant mavjud"))
	case errors.Is(err, domain.ErrInvalidAttributeType):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_attribute_type",
			"Недопустимый тип атрибута", "Atribut turi noto'g'ri"))
	case errors.Is(err, domain.ErrEnumRequiresOptions):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "enum_requires_options",
			"Для enum нужны варианты значений", "enum uchun variantlar kerak"))
	case errors.Is(err, domain.ErrOptionsForbidden):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "options_forbidden",
			"Варианты допустимы только для enum", "Variantlar faqat enum uchun"))
	case errors.Is(err, domain.ErrLinkExists):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindConflict, "link_exists",
			"Атрибут уже привязан к категории", "Atribut kategoriyaga biriktirilgan"))
	case errors.Is(err, domain.ErrLinkNotFound):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindNotFound, "link_not_found",
			"Привязка не найдена", "Biriktirish topilmadi"))
	default:
		log.ErrorContext(r.Context(), "ошибка каталога", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "catalog_failed",
			"Внутренняя ошибка", "Ichki xatolik"))
	}
}
