package domain

import (
	"errors"
	"strings"
	"time"
)

// Типы объектов жалобы.
const (
	TargetAd      = "ad"
	TargetUser    = "user"
	TargetMessage = "message"
	TargetReview  = "review"
)

// Статусы жалобы.
const (
	ReportOpen      = "open"      // ждёт разбора
	ReportResolved  = "resolved"  // разобрана с действием
	ReportDismissed = "dismissed" // отклонена без действия
)

// Действия модератора при разборе жалобы.
const (
	ActionDismiss  = "dismiss"  // отклонить жалобу (без последствий)
	ActionWarn     = "warn"     // предупреждение (информационно)
	ActionTakedown = "takedown" // снять объект (→ bozor.ad.blocked / bozor.chat.message_blocked / bozor.review.blocked)
)

// validTargets / validActions — допустимые значения.
var (
	validTargets = map[string]bool{TargetAd: true, TargetUser: true, TargetMessage: true, TargetReview: true}
	validActions = map[string]bool{ActionDismiss: true, ActionWarn: true, ActionTakedown: true}
)

// Ошибки валидации жалоб.
var (
	ErrInvalidTarget  = errors.New("недопустимый тип объекта жалобы")
	ErrInvalidAction  = errors.New("недопустимое действие модератора")
	ErrTakedownTarget = errors.New("снятие применимо к объявлению, сообщению или отзыву")
)

// Report — жалоба на объявление/пользователя/сообщение.
type Report struct {
	ID         string
	ReporterID string
	TargetType string
	TargetID   string
	Reason     string
	Status     string
	Resolution string
	ResolvedBy string
	ResolvedAt *time.Time
	CreatedAt  time.Time
}

// ValidateReportInput проверяет входные данные новой жалобы.
func ValidateReportInput(targetType, targetID, reason string) error {
	if !validTargets[targetType] {
		return ErrInvalidTarget
	}
	if strings.TrimSpace(targetID) == "" {
		return ErrInvalidTarget
	}
	return ValidateReason(reason)
}

// ValidateAction проверяет действие модератора; takedown применим к объявлению,
// сообщению или отзыву (не к пользователю — для пользователя есть бан).
func ValidateAction(action, targetType string) error {
	if !validActions[action] {
		return ErrInvalidAction
	}
	if action == ActionTakedown &&
		targetType != TargetAd && targetType != TargetMessage && targetType != TargetReview {
		return ErrTakedownTarget
	}
	return nil
}

// ResolvedStatus возвращает статус жалобы по действию (dismiss → dismissed, иначе resolved).
func ResolvedStatus(action string) string {
	if action == ActionDismiss {
		return ReportDismissed
	}
	return ReportResolved
}
