package ws

import (
	"context"
	"errors"
	"time"

	"github.com/nats-io/nats.go"

	"bozor/services/chat/internal/domain"
)

// presenceTimeout — сколько ждём ответ о присутствии. NATS сразу возвращает
// ErrNoResponders, если подписчиков нет (получатель оффлайн), так что таймаут
// расходуется лишь в редких гонках.
const presenceTimeout = 300 * time.Millisecond

// Presence проверяет онлайн-статус получателя запросом chat.presence.<userID>:
// ответ хотя бы одной реплики → пользователь подключён. Реализует app.Presence.
type Presence struct {
	nc *nats.Conn
}

// NewPresence создаёт проверку присутствия поверх NATS request-reply.
func NewPresence(nc *nats.Conn) *Presence { return &Presence{nc: nc} }

// Online сообщает, подключён ли пользователь к какой-либо реплике chat.
func (p *Presence) Online(ctx context.Context, userID string) (bool, error) {
	reqCtx, cancel := context.WithTimeout(ctx, presenceTimeout)
	defer cancel()
	_, err := p.nc.RequestWithContext(reqCtx, presencePrefix+userID, nil)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, nats.ErrNoResponders), errors.Is(err, context.DeadlineExceeded):
		return false, nil // никто не подключён / никто не ответил вовремя → оффлайн
	default:
		return false, err
	}
}

// Delivery доставляет кадры подключённым клиентам через Hub/backplane.
// Реализует app.Realtime.
type Delivery struct {
	hub *Hub
}

// NewDelivery создаёт realtime-доставку поверх Hub.
func NewDelivery(hub *Hub) *Delivery { return &Delivery{hub: hub} }

// DeliverMessage публикует кадр сообщения адресату (его реплика запишет в сокеты).
func (d *Delivery) DeliverMessage(recipientID string, msg domain.Message) {
	_ = d.hub.Publish(recipientID, messageFrame(msg))
}

// NotifyRead публикует кадр прочтения автору прочитанных сообщений.
func (d *Delivery) NotifyRead(recipientID string, r domain.ReadReceipt) {
	_ = d.hub.Publish(recipientID, readFrame(r))
}
