// Package ws реализует WebSocket-транспорт чата: аутентификация соединения по
// JWT, локальный реестр соединений (Hub) и backplane между репликами через NATS
// core pub/sub — сообщение адресату публикуется в chat.rt.<userID>, и реплика, к
// которой подключён получатель, доставляет его в свои сокеты. Так доставка не
// зависит от того, на одной ли реплике отправитель и получатель.
package ws

import "github.com/nats-io/nats.go"

// subjectPrefix — префикс realtime-subject получателя (chat.rt.<userID>).
const subjectPrefix = "chat.rt."

// presencePrefix — префикс subject запроса присутствия (chat.presence.<userID>).
// Реплика с подключением пользователя отвечает на запрос → пользователь онлайн.
const presencePrefix = "chat.presence."

// Subscription — подписка backplane, которую Hub отменяет при отключении
// последнего соединения пользователя.
type Subscription interface {
	Unsubscribe() error
}

// Backplane — межрепличная шина realtime-доставки.
type Backplane interface {
	// Publish отправляет payload адресату userID (получат все реплики с его подпиской).
	Publish(userID string, payload []byte) error
	// Subscribe подписывает реплику на сообщения для userID; handler вызывается
	// на каждое доставленное сообщение.
	Subscribe(userID string, handler func([]byte)) (Subscription, error)
	// RespondPresence подписывает реплику отвечать на запросы присутствия userID
	// (пока он подключён здесь), чтобы отправитель узнал онлайн-статус получателя.
	RespondPresence(userID string) (Subscription, error)
}

// NATSBackplane — реализация backplane поверх NATS core pub/sub (эфемерная
// доставка, не JetStream: realtime-сообщения не персистятся — история в БД).
type NATSBackplane struct {
	nc *nats.Conn
}

// NewNATSBackplane создаёт backplane поверх соединения NATS.
func NewNATSBackplane(nc *nats.Conn) *NATSBackplane {
	return &NATSBackplane{nc: nc}
}

// Publish публикует payload в chat.rt.<userID>.
func (b *NATSBackplane) Publish(userID string, payload []byte) error {
	return b.nc.Publish(subjectPrefix+userID, payload)
}

// Subscribe подписывается на chat.rt.<userID>; *nats.Subscription реализует Subscription.
func (b *NATSBackplane) Subscribe(userID string, handler func([]byte)) (Subscription, error) {
	return b.nc.Subscribe(subjectPrefix+userID, func(m *nats.Msg) { handler(m.Data) })
}

// RespondPresence отвечает пустым сообщением на запросы chat.presence.<userID>.
func (b *NATSBackplane) RespondPresence(userID string) (Subscription, error) {
	return b.nc.Subscribe(presencePrefix+userID, func(m *nats.Msg) { _ = m.Respond(nil) })
}
