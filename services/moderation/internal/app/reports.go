package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"bozor/pkg/shared/events"

	"bozor/services/moderation/internal/domain"
)

// Доменные ошибки жалоб/банов.
var (
	// ErrReportNotFound — жалоба не найдена.
	ErrReportNotFound = errors.New("жалоба не найдена")
	// ErrReportClosed — жалоба уже разобрана.
	ErrReportClosed = errors.New("жалоба уже закрыта")
)

// ReportStore — доступ к жалобам и банам (реализуется repo.Repo).
type ReportStore interface {
	CreateReportWithEvent(ctx context.Context, rep domain.Report, ev events.Envelope) error
	GetReport(ctx context.Context, id string) (domain.Report, bool, error)
	ResolveReportWithEvent(ctx context.Context, reportID, status, resolution, moderatorID string, ev *events.Envelope) (bool, error)
	ListReports(ctx context.Context, status string, limit int) ([]domain.Report, error)
	CreateBanWithEvent(ctx context.Context, ban domain.Ban, ev events.Envelope) error
}

// AdSource — чтение объявления для обогащения события снятия (реализуется listingclient).
type AdSource interface {
	GetAd(ctx context.Context, id string) (domain.AdView, bool, error)
}

// OpsService — use-cases жалоб и банов.
type OpsService struct {
	store  ReportStore
	source AdSource
	now    func() time.Time
	newID  func() (string, error)
	log    *slog.Logger
}

// NewOpsService создаёт сервис жалоб/банов.
func NewOpsService(store ReportStore, source AdSource, log *slog.Logger) *OpsService {
	return &OpsService{
		store:  store,
		source: source,
		now:    func() time.Time { return time.Now().UTC() },
		newID:  func() (string, error) { id, err := uuid.NewV7(); return id.String(), err },
		log:    log,
	}
}

// reportCreatedPayload — payload bozor.moderation.report_created.
type reportCreatedPayload struct {
	ReportID   string `json:"report_id"`
	ReporterID string `json:"reporter_id"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
}

// blockedPayload — payload bozor.ad.blocked (снятие объявления по жалобе).
type blockedPayload struct {
	AdID   string `json:"ad_id"`
	UserID string `json:"user_id"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// messageBlockedPayload — payload bozor.chat.message_blocked (снятие сообщения).
type messageBlockedPayload struct {
	MessageID string `json:"message_id"`
	Reason    string `json:"reason"`
}

// userBannedPayload — payload bozor.user.banned.
type userBannedPayload struct {
	UserID string     `json:"user_id"`
	Type   string     `json:"type"`
	Reason string     `json:"reason"`
	Until  *time.Time `json:"until,omitempty"` // срок временного бана
}

// CreateReport подаёт жалобу и публикует bozor.moderation.report_created.
func (s *OpsService) CreateReport(ctx context.Context, reporterID, targetType, targetID, reason string) (domain.Report, error) {
	if err := domain.ValidateReportInput(targetType, targetID, reason); err != nil {
		return domain.Report{}, err
	}
	id, err := s.newID()
	if err != nil {
		return domain.Report{}, fmt.Errorf("app: генерация id жалобы: %w", err)
	}
	rep := domain.Report{
		ID: id, ReporterID: reporterID, TargetType: targetType, TargetID: targetID,
		Reason: strings.TrimSpace(reason), Status: domain.ReportOpen, CreatedAt: s.now(),
	}
	ev, err := events.New(events.SubjectModerationReportCreated, source, reportCreatedPayload{
		ReportID: rep.ID, ReporterID: rep.ReporterID, TargetType: rep.TargetType, TargetID: rep.TargetID,
	})
	if err != nil {
		return domain.Report{}, fmt.Errorf("app: сборка bozor.moderation.report_created: %w", err)
	}
	if err := s.store.CreateReportWithEvent(ctx, rep, ev); err != nil {
		return domain.Report{}, err
	}
	return rep, nil
}

// ListReports возвращает жалобы по статусу (очередь).
func (s *OpsService) ListReports(ctx context.Context, status string, limit int) ([]domain.Report, error) {
	return s.store.ListReports(ctx, status, limit)
}

// ResolveReport разбирает жалобу действием модератора (dismiss/warn/takedown).
// takedown публикует bozor.ad.blocked (Listing снимает объявление из любого статуса).
func (s *OpsService) ResolveReport(ctx context.Context, reportID, moderatorID, action, note string) error {
	rep, found, err := s.store.GetReport(ctx, reportID)
	if err != nil {
		return err
	}
	if !found {
		return ErrReportNotFound
	}
	if rep.Status != domain.ReportOpen {
		return ErrReportClosed
	}
	if err := domain.ValidateAction(action, rep.TargetType); err != nil {
		return err
	}

	ev, err := s.takedownEvent(ctx, action, rep, note)
	if err != nil {
		return err
	}

	resolution := action
	if strings.TrimSpace(note) != "" {
		resolution = action + ": " + strings.TrimSpace(note)
	}
	applied, err := s.store.ResolveReportWithEvent(ctx, reportID, domain.ResolvedStatus(action), resolution, moderatorID, ev)
	if err != nil {
		return err
	}
	if !applied {
		return ErrReportClosed
	}
	s.log.InfoContext(ctx, "жалоба разобрана",
		slog.String("report_id", reportID), slog.String("action", action))
	return nil
}

// takedownEvent строит событие снятия для takedown (для прочих действий — nil):
// объявление → bozor.ad.blocked (обогащая владельцем/заголовком из Listing, снятие
// из любого статуса), сообщение → bozor.chat.message_blocked (Chat скрывает тело).
func (s *OpsService) takedownEvent(ctx context.Context, action string, rep domain.Report, note string) (*events.Envelope, error) {
	if action != domain.ActionTakedown {
		return nil, nil
	}
	reason := strings.TrimSpace(note)
	if reason == "" {
		reason = "снято по жалобе"
	}

	if rep.TargetType == domain.TargetMessage {
		ev, err := events.New(events.SubjectChatMessageBlocked, source, messageBlockedPayload{
			MessageID: rep.TargetID, Reason: reason,
		})
		if err != nil {
			return nil, fmt.Errorf("app: сборка bozor.chat.message_blocked (takedown): %w", err)
		}
		return &ev, nil
	}

	ad, found, err := s.source.GetAd(ctx, rep.TargetID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil // объявление уже удалено — снимать нечего
	}
	ev, err := events.New(events.SubjectAdBlocked, source, blockedPayload{
		AdID: ad.ID, UserID: ad.UserID, Title: ad.Title, Reason: reason,
	})
	if err != nil {
		return nil, fmt.Errorf("app: сборка bozor.ad.blocked (takedown): %w", err)
	}
	return &ev, nil
}

// BanUser банит пользователя и публикует bozor.user.banned (→ Auth отзывает сессии,
// Notification уведомляет).
func (s *OpsService) BanUser(ctx context.Context, moderatorID, userID, banType, reason string, duration time.Duration) (domain.Ban, error) {
	if err := domain.ValidateBanInput(banType, duration); err != nil {
		return domain.Ban{}, err
	}
	id, err := s.newID()
	if err != nil {
		return domain.Ban{}, fmt.Errorf("app: генерация id бана: %w", err)
	}
	now := s.now()
	ban := domain.Ban{
		ID: id, UserID: userID, Type: banType, Reason: strings.TrimSpace(reason),
		ExpiresAt: domain.BanExpiry(banType, now, duration), CreatedBy: moderatorID, CreatedAt: now,
	}
	ev, err := events.New(events.SubjectUserBanned, source, userBannedPayload{
		UserID: ban.UserID, Type: ban.Type, Reason: ban.Reason, Until: ban.ExpiresAt,
	})
	if err != nil {
		return domain.Ban{}, fmt.Errorf("app: сборка bozor.user.banned: %w", err)
	}
	if err := s.store.CreateBanWithEvent(ctx, ban, ev); err != nil {
		return domain.Ban{}, err
	}
	s.log.InfoContext(ctx, "пользователь забанен",
		slog.String("user_id", userID), slog.String("type", banType))
	return ban, nil
}
