package domain

import (
	"fmt"
	"strconv"

	"bozor/pkg/shared/events"
)

// renderer строит текст уведомления для конкретного языка ("uz"/"ru").
type renderer func(lang string, p EventPayload) string

// templates — локализованные шаблоны уведомлений по типам событий (uz/ru).
// Тексты используют только поля, гарантированно присутствующие в событии;
// отсутствующие подставляются пустыми и опускаются форматированием.
var templates = map[string]renderer{
	events.SubjectAdApproved: func(lang string, p EventPayload) string {
		return pick(lang,
			"✅ Ваше объявление «"+p.Title+"» одобрено и опубликовано.",
			"✅ «"+p.Title+"» e'loningiz tasdiqlanib, chop etildi.")
	},
	events.SubjectAdRejected: func(lang string, p EventPayload) string {
		ru := "❌ Ваше объявление «" + p.Title + "» отклонено."
		uz := "❌ «" + p.Title + "» e'loningiz rad etildi."
		if p.Reason != "" {
			ru += " Причина: " + p.Reason + "."
			uz += " Sababi: " + p.Reason + "."
		}
		return pick(lang, ru, uz)
	},
	events.SubjectAdBlocked: func(lang string, p EventPayload) string {
		ru := "🚫 Ваше объявление «" + p.Title + "» снято с публикации."
		uz := "🚫 «" + p.Title + "» e'loningiz chop etishdan olib tashlandi."
		if p.Reason != "" {
			ru += " Причина: " + p.Reason + "."
			uz += " Sababi: " + p.Reason + "."
		}
		return pick(lang, ru, uz)
	},
	events.SubjectAdExpired: func(lang string, p EventPayload) string {
		return pick(lang,
			"⌛ Срок действия объявления «"+p.Title+"» истёк. Продлите его в приложении.",
			"⌛ «"+p.Title+"» e'loni muddati tugadi. Ilovada uni uzaytiring.")
	},
	events.SubjectSavedSearchMatched: func(lang string, p EventPayload) string {
		return pick(lang,
			"🔔 По вашему сохранённому поиску «"+p.Name+"» найдено новое объявление.",
			"🔔 «"+p.Name+"» saqlangan qidiruvingiz bo‘yicha yangi e'lon topildi.")
	},
	events.SubjectChatMessageSent: func(lang string, p EventPayload) string {
		from := p.SenderName
		ru := "💬 Новое сообщение"
		uz := "💬 Yangi xabar"
		if from != "" {
			ru += " от " + from
			uz += " " + from + "dan"
		}
		return pick(lang, ru+".", uz+".")
	},
	events.SubjectReviewCreated: func(lang string, _ EventPayload) string {
		return pick(lang,
			"⭐ Вам оставили новый отзыв.",
			"⭐ Sizga yangi sharh qoldirildi.")
	},
	events.SubjectPromotionActivated: func(lang string, p EventPayload) string {
		return pick(lang,
			"🚀 Продвижение для объявления «"+p.Title+"» активировано.",
			"🚀 «"+p.Title+"» e'loni uchun reklama faollashtirildi.")
	},
	events.SubjectPaymentSucceeded: func(lang string, p EventPayload) string {
		return pick(lang,
			"💳 Оплата на "+money(p)+" прошла успешно.",
			"💳 "+money(p)+" to‘lov muvaffaqiyatli amalga oshirildi.")
	},
	events.SubjectPaymentFailed: func(lang string, _ EventPayload) string {
		return pick(lang,
			"⚠️ Оплата не прошла. Попробуйте ещё раз.",
			"⚠️ To‘lov amalga oshmadi. Qayta urinib ko‘ring.")
	},
	events.SubjectWalletRefunded: func(lang string, p EventPayload) string {
		return pick(lang,
			"↩️ Возврат "+money(p)+" зачислен на ваш баланс.",
			"↩️ "+money(p)+" mablag‘ hisobingizga qaytarildi.")
	},
	events.SubjectUserBanned: func(lang string, p EventPayload) string {
		ru := "🚫 Ваш аккаунт заблокирован."
		uz := "🚫 Hisobingiz bloklandi."
		if p.Reason != "" {
			ru += " Причина: " + p.Reason + "."
			uz += " Sababi: " + p.Reason + "."
		}
		if p.Until != "" {
			ru += " До: " + p.Until + "."
			uz += " Muddat: " + p.Until + "."
		}
		return pick(lang, ru, uz)
	},
}

// Render строит локализованный текст уведомления по subject и языку.
// ok=false, если subject не имеет шаблона (в шину такое не попадёт — консьюмер
// фильтрует subjects, но защищаемся на всякий случай).
func Render(subject, lang string, p EventPayload) (string, bool) {
	r, ok := templates[subject]
	if !ok {
		return "", false
	}
	return r(NormalizeLang(lang), p), true
}

// pick выбирает строку по языку ("uz" → uz, иначе ru).
func pick(lang, ru, uz string) string {
	if lang == "uz" {
		return uz
	}
	return ru
}

// money форматирует сумму с валютой ("50000 UZS"); при отсутствии суммы —
// нейтральная формулировка без числа.
func money(p EventPayload) string {
	if p.Amount <= 0 {
		return ""
	}
	amount := strconv.FormatInt(p.Amount, 10)
	if p.Currency == "" {
		return amount
	}
	return fmt.Sprintf("%s %s", amount, p.Currency)
}
