// Package natsx — обвязка над NATS JetStream для шины событий Bozor:
// подключение, создание стримов и консьюмеров, публикация и потребление
// событий в конвертах events.Envelope с поддержкой дедупликации и DLQ.
package natsx

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"bozor/pkg/shared/events"
)

// Connect подключается к NATS и создаёт контекст JetStream.
// Клиент переподключается бесконечно (MaxReconnects(-1)) с паузой 2 секунды.
func Connect(url, name string) (*nats.Conn, jetstream.JetStream, error) {
	nc, err := nats.Connect(url,
		nats.Name(name),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("natsx: подключение к NATS %q: %w", url, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("natsx: создание контекста JetStream: %w", err)
	}

	return nc, js, nil
}

// EnsureStream создаёт или обновляет JetStream-стрим с файловым хранилищем,
// политикой лимитов и окном дедупликации 2 минуты.
func EnsureStream(ctx context.Context, js jetstream.JetStream, name string, subjects []string) (jetstream.Stream, error) {
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       name,
		Subjects:   subjects,
		Storage:    jetstream.FileStorage,
		Retention:  jetstream.LimitsPolicy,
		Duplicates: 2 * time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("natsx: создание стрима %q: %w", name, err)
	}
	return stream, nil
}

// Publish публикует конверт события в JetStream.
// Subject берётся из env.Type, а env.ID передаётся в заголовке Nats-Msg-Id
// для дедупликации на стороне сервера.
func Publish(ctx context.Context, js jetstream.JetStream, env events.Envelope) error {
	payload, err := marshalEnvelope(env)
	if err != nil {
		return err
	}

	msg := &nats.Msg{
		Subject: env.Type,
		Header:  nats.Header{},
		Data:    payload,
	}
	msg.Header.Set("Nats-Msg-Id", env.ID)

	if _, err := js.PublishMsg(ctx, msg); err != nil {
		return fmt.Errorf("natsx: публикация события %q (id=%s): %w", env.Type, env.ID, err)
	}
	return nil
}

// Handler — обработчик события. Ненулевая ошибка приводит к повторной
// доставке (Nak) либо к отправке сообщения в DLQ после maxDeliver попыток.
type Handler func(ctx context.Context, env events.Envelope) error

// Consume создаёт (или обновляет) durable-консьюмер и запускает потребление
// сообщений. «Ядовитые» сообщения (не парсятся в Envelope) и сообщения,
// исчерпавшие maxDeliver попыток, публикуются в DLQ-subject консьюмера
// (events.DLQSubject) и подтверждаются.
func Consume(ctx context.Context, js jetstream.JetStream, stream, durable string, subjects []string, maxDeliver int, h Handler) (jetstream.ConsumeContext, error) {
	cons, err := js.CreateOrUpdateConsumer(ctx, stream, jetstream.ConsumerConfig{
		Durable:        durable,
		FilterSubjects: subjects,
		AckPolicy:      jetstream.AckExplicitPolicy,
		MaxDeliver:     maxDeliver,
		AckWait:        30 * time.Second,
		BackOff:        backoffSchedule(maxDeliver),
	})
	if err != nil {
		return nil, fmt.Errorf("natsx: создание консьюмера %q в стриме %q: %w", durable, stream, err)
	}

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		handleMsg(ctx, js, durable, maxDeliver, h, msg)
	})
	if err != nil {
		return nil, fmt.Errorf("natsx: запуск потребления консьюмером %q: %w", durable, err)
	}
	return cc, nil
}

// handleMsg обрабатывает одно сообщение: парсит конверт, вызывает обработчик
// и решает судьбу сообщения (Ack / Nak / DLQ).
func handleMsg(ctx context.Context, js jetstream.JetStream, durable string, maxDeliver int, h Handler, msg jetstream.Msg) {
	var env events.Envelope
	if err := json.Unmarshal(msg.Data(), &env); err != nil {
		// «Ядовитое» сообщение: в обработчик не попадёт никогда —
		// отправляем сырые данные в DLQ и подтверждаем.
		publishDLQ(ctx, js, durable, msg.Data())
		_ = msg.Ack()
		return
	}

	if err := h(ctx, env); err != nil {
		numDelivered := uint64(1)
		if meta, metaErr := msg.Metadata(); metaErr == nil {
			numDelivered = meta.NumDelivered
		}
		if decideAck(numDelivered, maxDeliver) {
			// Попытки исчерпаны: исходный payload — в DLQ, сообщение подтверждаем.
			publishDLQ(ctx, js, durable, msg.Data())
			_ = msg.Ack()
			return
		}
		_ = msg.Nak()
		return
	}

	_ = msg.Ack()
}

// publishDLQ публикует сырые данные сообщения в DLQ-subject консьюмера.
// Ошибка публикации сознательно игнорируется: сообщение всё равно будет
// подтверждено, чтобы не блокировать консьюмер (TODO: добавить логирование).
func publishDLQ(ctx context.Context, js jetstream.JetStream, durable string, data []byte) {
	_, _ = js.Publish(ctx, events.DLQSubject(durable), data)
}

// decideAck решает, нужно ли подтвердить сообщение с отправкой в DLQ (true)
// или запросить повторную доставку через Nak (false).
func decideAck(numDelivered uint64, maxDeliver int) bool {
	return maxDeliver > 0 && numDelivered >= uint64(maxDeliver)
}

// backoffSchedule возвращает расписание пауз между повторными доставками
// {1s, 5s, 15s}, обрезанное так, чтобы длина не превышала MaxDeliver
// (требование JetStream: len(BackOff) <= MaxDeliver).
func backoffSchedule(maxDeliver int) []time.Duration {
	full := []time.Duration{time.Second, 5 * time.Second, 15 * time.Second}
	if maxDeliver > 0 && maxDeliver < len(full) {
		return full[:maxDeliver]
	}
	return full
}

// marshalEnvelope сериализует конверт события в JSON.
func marshalEnvelope(env events.Envelope) ([]byte, error) {
	payload, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("natsx: сериализация конверта события (id=%s): %w", env.ID, err)
	}
	return payload, nil
}
