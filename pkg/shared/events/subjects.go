package events

// StreamName — имя JetStream-стрима, в который пишутся все события Bozor.
const StreamName = "BOZOR"

// SubjectsWildcard — wildcard, покрывающий все subjects событий Bozor.
const SubjectsWildcard = "bozor.>"

// Subjects событий. Значение константы — NATS subject;
// поле Type у Envelope совпадает с subject.
const (
	// Объявления.
	SubjectAdCreated  = "bozor.ad.created"
	SubjectAdUpdated  = "bozor.ad.updated"
	SubjectAdDeleted  = "bozor.ad.deleted"
	SubjectAdApproved = "bozor.ad.approved"
	SubjectAdRejected = "bozor.ad.rejected"
	SubjectAdSold     = "bozor.ad.sold"
	SubjectAdExpired  = "bozor.ad.expired"
	SubjectAdBumped   = "bozor.ad.bumped"

	// Пользователи.
	SubjectUserCreated = "bozor.user.created"
	SubjectUserUpdated = "bozor.user.updated"
	SubjectUserBanned  = "bozor.user.banned"

	// Сохранённые поиски.
	SubjectSavedSearchMatched = "bozor.saved_search.matched" // новое объявление совпало с сохранённым поиском (→ Notification)

	// Категории каталога.
	SubjectCategoryCreated = "bozor.category.created"
	SubjectCategoryUpdated = "bozor.category.updated"
	SubjectCategoryDeleted = "bozor.category.deleted"

	// Медиа.
	SubjectMediaUploaded  = "bozor.media.uploaded"  // оригинал загружен (→ воркер обработки)
	SubjectMediaProcessed = "bozor.media.processed" // превью готовы (воркер, 3.2)
	SubjectMediaDeleted   = "bozor.media.deleted"   // объект удалён/осиротел

	// Чат.
	SubjectChatMessageSent = "bozor.chat.message_sent"

	// Продвижение и платежи.
	SubjectPromotionActivated = "bozor.promotion.activated"
	SubjectPaymentSucceeded   = "bozor.payment.succeeded"
	SubjectPaymentFailed      = "bozor.payment.failed"
	SubjectWalletRefunded     = "bozor.wallet.refunded"

	// Отзывы и модерация.
	SubjectReviewCreated           = "bozor.review.created"
	SubjectModerationReportCreated = "bozor.moderation.report_created"
)

// DLQSubject возвращает subject dead-letter-очереди для указанного durable-консьюмера.
func DLQSubject(durable string) string {
	return "bozor.dlq." + durable
}
