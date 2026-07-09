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
	ActionTakedown = "takedown" // снять объявление (→ bozor.ad.rejected)
)

// validTargets / validActions — допустимые значения.
var (
	validTargets = map[string]bool{TargetAd: true, TargetUser: true, TargetMessage: true}
	validActions = map[string]bool{ActionDismiss: true, ActionWarn: true, ActionTakedown: true}
)

// Ошибки валидации жалоб.
var (
	ErrInvalidTarget  = errors.New("недопустимый тип объекта жалобы")
	ErrInvalidAction  = errors.New("недопустимое действие модератора")
	ErrTakedownTarget = errors.New("снятие применимо только к объявлению")
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

// ValidateAction проверяет действие модератора; для takedown цель обязана быть объявлением.
func ValidateAction(action, targetType string) error {
	if !validActions[action] {
		return ErrInvalidAction
	}
	if action == ActionTakedown && targetType != TargetAd {
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
