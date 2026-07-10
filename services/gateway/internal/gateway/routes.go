package gateway

// Route сопоставляет префикс внешнего REST-пути с внутренним сервисом.
type Route struct {
	Prefix  string // префикс пути, например "/api/v1/ads"
	Service string // имя внутреннего сервиса (совпадает с DNS в сети compose)
}

// svcFavorites / svcPayments — сервисы, обслуживающие несколько префиксов,
// поэтому вынесены в константы (единое имя во всех маршрутах).
const (
	svcFavorites = "favorites-savedsearch"
	svcPayments  = "payments-promotions"
	svcProfile   = "user-profile"
)

// Routes — таблица маршрутизации внешнего API `/api/v1/*` на внутренние
// сервисы. Полный путь (с префиксом /api/v1) проксируется как есть —
// версионирование обрабатывают сами сервисы.
//
// Детализация вложенных путей разводится более специфичными префиксами: chi
// (radix-trie) отдаёт приоритет статическому сегменту независимо от порядка, так
// /me/ads уходит в listing-ads, а /me и /me/notification-prefs — в user-profile.
var Routes = []Route{
	{Prefix: "/api/v1/auth", Service: "auth"},
	// Отзывы о пользователе — более специфичный путь, чем /users (chi отдаёт
	// приоритет статическому сегменту reviews над catch-all /users/*).
	{Prefix: "/api/v1/users/{userID}/reviews", Service: "reviews"},
	{Prefix: "/api/v1/users", Service: svcProfile},
	// Мои объявления/избранное/сохранённые поиски/кошелёк — более специфичны, чем /me.
	{Prefix: "/api/v1/me/ads", Service: "listing-ads"},
	{Prefix: "/api/v1/me/favorites", Service: svcFavorites},
	{Prefix: "/api/v1/me/saved-searches", Service: svcFavorites},
	{Prefix: "/api/v1/me/wallet", Service: svcPayments},
	{Prefix: "/api/v1/me", Service: svcProfile},
	{Prefix: "/api/v1/categories", Service: "catalog"},
	{Prefix: "/api/v1/attributes", Service: "catalog"},
	{Prefix: "/api/v1/locations", Service: "location"},
	// Поиск и продвижение — более специфичные пути, чем /api/v1/ads (Listing):
	// chi отдаёт приоритет статическому сегменту, поэтому /ads/search уходит в
	// search, /ads/{id}/promote — в payments-promotions, а /ads и /ads/{id} — в
	// listing-ads.
	{Prefix: "/api/v1/ads/search", Service: "search"},
	{Prefix: "/api/v1/ads/{adID}/promote", Service: svcPayments},
	{Prefix: "/api/v1/ads/{adID}/promotions", Service: svcPayments},
	{Prefix: "/api/v1/ads", Service: "listing-ads"},
	{Prefix: "/api/v1/media", Service: "media"},
	{Prefix: "/api/v1/favorites", Service: svcFavorites},
	{Prefix: "/api/v1/saved-searches", Service: svcFavorites},
	{Prefix: "/api/v1/conversations", Service: "chat"},
	{Prefix: "/api/v1/chat", Service: "chat"},
	{Prefix: "/api/v1/notifications", Service: "notification"},
	{Prefix: "/api/v1/reports", Service: "moderation"},
	{Prefix: "/api/v1/moderation", Service: "moderation"},
	{Prefix: "/api/v1/payments", Service: svcPayments},
	{Prefix: "/api/v1/promotions", Service: svcPayments},
	{Prefix: "/api/v1/wallet", Service: svcPayments},
	{Prefix: "/api/v1/reviews", Service: "reviews"},
}
