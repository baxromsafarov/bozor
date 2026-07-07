package domain

// События аудита авторизации (значения колонки auth_audit_log.event).
const (
	AuditLogin          = "login"
	AuditTokenRefreshed = "token_refreshed"
	AuditLogout         = "logout"
	AuditReuseDetected  = "refresh_reuse_detected"
)

// AuditEntry — запись журнала чувствительных действий авторизации.
type AuditEntry struct {
	UserID string         // пустая строка → NULL (действие без известного пользователя)
	Event  string         // одно из Audit*
	Detail map[string]any // произвольные детали (device_id, family_id и т.п.)
	IP     string         // клиентский IP; пустой/невалидный → NULL
}
