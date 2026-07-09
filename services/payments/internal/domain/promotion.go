// Package domain — доменные типы каталога платных услуг продвижения (Stage 8.1):
// отдельные услуги, наборы и правила ценообразования (регион × категория ×
// длительность). Слой не зависит от БД и транспорта.
package domain

// Типы продуктов в таблице pricing.
const (
	ProductService = "service"
	ProductBundle  = "bundle"
	CurrencyUZS    = "UZS"
)

// Service — отдельная платная услуга (TOP/VIP/BUMP/LOGO).
type Service struct {
	Code          string
	NameUZ        string
	NameRU        string
	DescriptionUZ string
	DescriptionRU string
	// Durations — допустимые длительности покупки в днях; 0 у BUMP (разовое поднятие).
	Durations []int
	SortOrder int
}

// BundleItem — состав набора: услуга, её длительность и расписание авто-BUMP.
type BundleItem struct {
	ServiceCode string
	Duration    int
	// BumpSchedule — дни от старта для авто-поднятий (только для BUMP; иначе пуст).
	BumpSchedule []int
	SortOrder    int
}

// Bundle — предустановленный набор услуг (EASY_START/FAST_SALE/TURBO_SALE).
type Bundle struct {
	Code          string
	NameUZ        string
	NameRU        string
	DescriptionUZ string
	DescriptionRU string
	SortOrder     int
	Items         []BundleItem
}

// PriceRule — строка таблицы pricing. RegionID/CategoryID = nil означает «любой»
// (базовая цена). Чем больше конкретики, тем выше приоритет при разрешении.
type PriceRule struct {
	ProductType string
	ProductCode string
	RegionID    *int
	CategoryID  *string
	Duration    int
	AmountUZS   int64
}

// Specificity — приоритет правила: регион (+2) конкретнее категории (+1), их
// сочетание — самое конкретное. Базовое правило (оба nil) имеет приоритет 0.
func (r PriceRule) Specificity() int {
	s := 0
	if r.RegionID != nil {
		s += 2
	}
	if r.CategoryID != nil {
		s++
	}
	return s
}

// PriceKey адресует цену по продукту и длительности.
type PriceKey struct {
	productType string
	productCode string
	duration    int
}

// ResolvePrices сводит набор правил (уже отфильтрованных под конкретные регион и
// категорию: подходящие или базовые) к одной цене на каждый продукт×длительность,
// выбирая самое конкретное правило. Возвращает карту для быстрого поиска.
func ResolvePrices(rules []PriceRule) map[PriceKey]int64 {
	best := make(map[PriceKey]PriceRule, len(rules))
	for _, r := range rules {
		k := PriceKey{r.ProductType, r.ProductCode, r.Duration}
		if cur, ok := best[k]; !ok || r.Specificity() > cur.Specificity() {
			best[k] = r
		}
	}
	out := make(map[PriceKey]int64, len(best))
	for k, r := range best {
		out[k] = r.AmountUZS
	}
	return out
}

// ServicePrice возвращает разрешённую цену услуги на указанную длительность.
func ServicePrice(prices map[PriceKey]int64, code string, duration int) (int64, bool) {
	amount, ok := prices[PriceKey{ProductService, code, duration}]
	return amount, ok
}

// BundlePrice возвращает разрешённую цену набора (длительность набора — 0).
func BundlePrice(prices map[PriceKey]int64, code string) (int64, bool) {
	amount, ok := prices[PriceKey{ProductBundle, code, 0}]
	return amount, ok
}
