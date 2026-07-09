// Package domain содержит сущности и правила Moderation-сервиса: задачи
// модерации, авто-проверки (стоп-слова, запрещённые категории, дубли) и
// нормализацию текста для сопоставления.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Статусы задачи модерации.
const (
	StatusApproved      = "approved"       // прошло авто-проверки / одобрено вручную → bozor.ad.approved
	StatusManual        = "manual"         // не прошло авто-проверки → ручная очередь (6.3)
	StatusRejected      = "rejected"       // отклонено вручную (6.3) → bozor.ad.rejected
	StatusEditRequested = "edit_requested" // возвращено на доработку (6.3) → bozor.ad.rejected (editable)
)

// ReasonMaxLen — максимальная длина причины/комментария модератора (руны).
const ReasonMaxLen = 500

// Ошибки валидации ручного решения.
var (
	ErrReasonRequired = errors.New("причина обязательна")
	ErrReasonTooLong  = errors.New("причина слишком длинная")
)

// ValidateReason проверяет обязательную причину ручного решения (reject/request-edit).
func ValidateReason(reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ErrReasonRequired
	}
	if utf8.RuneCountInString(reason) > ReasonMaxLen {
		return ErrReasonTooLong
	}
	return nil
}

// Результаты авто-проверки.
const (
	AutoPassed  = "passed"
	AutoFlagged = "flagged"
)

// Причины авто-флага (префиксы; стоп-слово дополняется словом).
const (
	ReasonStopwordPrefix = "stopword:"          // сработало стоп-слово
	ReasonForbiddenCat   = "forbidden_category" // запрещённая категория
	ReasonDuplicate      = "duplicate"          // дубль объявления пользователя
)

// AdView — проекция объявления из Listing для авто-проверок.
type AdView struct {
	ID          string
	UserID      string
	CategoryID  string
	Title       string
	Description string
	Status      string // модерируем только объявления в статусе pending
}

// Task — задача модерации объявления.
type Task struct {
	ID          string
	AdID        string
	UserID      string
	Title       string
	ContentHash string
	Status      string
	AutoResult  string
	Reasons     []string
	DecidedBy   string     // модератор ручного решения (пусто до решения)
	Comment     string     // причина reject / пояснение request-edit
	DecidedAt   *time.Time // время ручного решения
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// wsRe схлопывает последовательности пробельных символов в один пробел.
var wsRe = regexp.MustCompile(`\s+`)

// Normalize приводит текст к форме для сопоставления: нижний регистр, ё→е,
// схлопнутые пробелы, без крайних пробелов.
func Normalize(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "ё", "е")
	s = wsRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// ContentHash — хэш нормализованного содержимого (title+description) для
// детекции дублей объявлений одного пользователя.
func ContentHash(title, description string) string {
	sum := sha256.Sum256([]byte(Normalize(title) + "\x00" + Normalize(description)))
	return hex.EncodeToString(sum[:])
}

// MatchedStopwords возвращает стоп-слова, встретившиеся в тексте (подстрока без
// регистра по нормализованному title+description). words — активные стоп-слова.
func MatchedStopwords(title, description string, words []string) []string {
	haystack := Normalize(title + " " + description)
	var matched []string
	seen := make(map[string]bool)
	for _, w := range words {
		nw := Normalize(w)
		if nw == "" || seen[nw] {
			continue
		}
		if strings.Contains(haystack, nw) {
			matched = append(matched, w)
			seen[nw] = true
		}
	}
	return matched
}

// Evaluate собирает причины авто-флага из результатов проверок. Пустой срез —
// объявление проходит авто-модерацию (будет одобрено).
func Evaluate(matchedStopwords []string, forbiddenCategory, duplicate bool) []string {
	var reasons []string
	for _, w := range matchedStopwords {
		reasons = append(reasons, ReasonStopwordPrefix+w)
	}
	if forbiddenCategory {
		reasons = append(reasons, ReasonForbiddenCat)
	}
	if duplicate {
		reasons = append(reasons, ReasonDuplicate)
	}
	return reasons
}
