package natsx

// Юнит-тесты чистых хелперов natsx без реального NATS-сервера.
// TODO: интеграционные тесты Connect/EnsureStream/Publish/Consume
// против реального NATS JetStream (testcontainers или embedded server).

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"
)

func TestMarshalEnvelope(t *testing.T) {
	t.Parallel()

	env, err := events.New(events.SubjectAdCreated, "ad-service", map[string]string{"ad_id": "42"})
	require.NoError(t, err)

	payload, err := marshalEnvelope(env)
	require.NoError(t, err)

	var got events.Envelope
	require.NoError(t, json.Unmarshal(payload, &got))

	assert.Equal(t, env.ID, got.ID)
	assert.Equal(t, events.SubjectAdCreated, got.Type)
	assert.Equal(t, "ad-service", got.Source)
	assert.Equal(t, "1.0", got.SpecVersion)
	assert.JSONEq(t, `{"ad_id":"42"}`, string(got.Data))
}

func TestDecideAck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		numDelivered uint64
		maxDeliver   int
		want         bool
	}{
		{name: "первая доставка — Nak", numDelivered: 1, maxDeliver: 5, want: false},
		{name: "предпоследняя доставка — Nak", numDelivered: 4, maxDeliver: 5, want: false},
		{name: "последняя доставка — DLQ", numDelivered: 5, maxDeliver: 5, want: true},
		{name: "превышение лимита — DLQ", numDelivered: 6, maxDeliver: 5, want: true},
		{name: "maxDeliver=1 — сразу DLQ", numDelivered: 1, maxDeliver: 1, want: true},
		{name: "maxDeliver=0 — всегда Nak", numDelivered: 100, maxDeliver: 0, want: false},
		{name: "maxDeliver отрицательный — всегда Nak", numDelivered: 100, maxDeliver: -1, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, decideAck(tt.numDelivered, tt.maxDeliver))
		})
	}
}

func TestBackoffSchedule(t *testing.T) {
	t.Parallel()

	full := []time.Duration{time.Second, 5 * time.Second, 15 * time.Second}

	tests := []struct {
		name       string
		maxDeliver int
		want       []time.Duration
	}{
		{name: "maxDeliver=1 — одна пауза", maxDeliver: 1, want: full[:1]},
		{name: "maxDeliver=2 — две паузы", maxDeliver: 2, want: full[:2]},
		{name: "maxDeliver=3 — полное расписание", maxDeliver: 3, want: full},
		{name: "maxDeliver=5 — полное расписание", maxDeliver: 5, want: full},
		{name: "maxDeliver=0 — полное расписание (без лимита)", maxDeliver: 0, want: full},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := backoffSchedule(tt.maxDeliver)
			assert.Equal(t, tt.want, got)
			assert.LessOrEqual(t, len(got), 3)
			if tt.maxDeliver > 0 {
				assert.LessOrEqual(t, len(got), tt.maxDeliver,
					"len(BackOff) должна быть <= MaxDeliver")
			}
		})
	}
}

func TestDLQSubjectForConsumer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		durable string
		want    string
	}{
		{name: "поисковый консьюмер", durable: "search-indexer", want: "bozor.dlq.search-indexer"},
		{name: "нотификации", durable: "notifications", want: "bozor.dlq.notifications"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, events.DLQSubject(tt.durable))
		})
	}
}
