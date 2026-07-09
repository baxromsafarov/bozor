package gateway

// Route сопоставляет префикс внешнего REST-пути с внутренним сервисом.
type Route struct {
	Prefix  string // префикс пути, например "/api/v1/ads"
	Service string // имя внутреннего сервиса (совпадает с DNS в сети compose)
}

// Routes — таблица маршрутизации внешнего API `/api/v1/*` на внутренние
// сервисы. Полный путь (с префиксом /api/v1) проксируется как есть —
// версионирование обрабатывают сами сервисы.
//
// Детализация вложенных путей разводится более специфичными префиксами: chi
// (radix-trie) отдаёт приоритет статическому сегменту независимо от порядка, так
// /me/ads уходит в listing-ads, а /me и /me/notification-prefs — в user-profile.
var Routes = []Route{
	{Prefix: "/api/v1/auth", Service: "auth"},
	{Prefix: "/api/v1/users", Service: "user-profile"},
	// Мои объявления/избранное — более специфичны, чем /me → user-profile.
	{Prefix: "/api/v1/me/ads", Service: "listing-ads"},
	{Prefix: "/api/v1/me/favorites", Service: "favorites-savedsearch"},
	{Prefix: "/api/v1/me", Service: "user-profile"},
	{Prefix: "/api/v1/categories", Service: "catalog"},
	{Prefix: "/api/v1/attributes", Service: "catalog"},
	{Prefix: "/api/v1/locations", Service: "location"},
	// Поиск — более специфичный префикс, чем /api/v1/ads (Listing): chi отдаёт
	// приоритет статическому сегменту, поэтому /ads/search уходит в search, а
	// /ads и /ads/{id} — в listing-ads.
	{Prefix: "/api/v1/ads/search", Service: "search"},
	{Prefix: "/api/v1/ads", Service: "listing-ads"},
	{Prefix: "/api/v1/media", Service: "media"},
	{Prefix: "/api/v1/favorites", Service: "favorites-savedsearch"},
	{Prefix: "/api/v1/saved-searches", Service: "favorites-savedsearch"},
	{Prefix: "/api/v1/chat", Service: "chat"},
	{Prefix: "/api/v1/notifications", Service: "notification"},
	{Prefix: "/api/v1/moderation", Service: "moderation"},
	{Prefix: "/api/v1/payments", Service: "payments-promotions"},
	{Prefix: "/api/v1/promotions", Service: "payments-promotions"},
	{Prefix: "/api/v1/reviews", Service: "reviews"},
}
