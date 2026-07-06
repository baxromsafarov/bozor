package events

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testPayload struct {
	AdID  string `json:"ad_id"`
	Price int64  `json:"price"`
}

func TestNew(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		source    string
		data      any
		wantErr   bool
	}{
		{
			name:      "структура",
			eventType: SubjectAdCreated,
			source:    "ads-service",
			data:      testPayload{AdID: "42", Price: 1000},
		},
		{
			name:      "nil data",
			eventType: SubjectUserCreated,
			source:    "users-service",
			data:      nil,
		},
		{
			name:      "несериализуемые данные",
			eventType: SubjectAdCreated,
			source:    "ads-service",
			data:      make(chan int),
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, err := New(tt.eventType, tt.source, tt.data)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			// ID — валидный UUID версии 7.
			id, err := uuid.Parse(env.ID)
			require.NoError(t, err)
			assert.Equal(t, uuid.Version(7), id.Version())

			assert.Equal(t, tt.eventType, env.Type)
			assert.Equal(t, tt.source, env.Source)
			assert.Equal(t, "1.0", env.SpecVersion)

			// Время — в UTC и близко к текущему.
			assert.Equal(t, time.UTC, env.Time.Location())
			assert.WithinDuration(t, time.Now().UTC(), env.Time, 5*time.Second)
		})
	}
}

func TestEnvelope_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   testPayload
	}{
		{name: "обычные значения", in: testPayload{AdID: "abc", Price: 99900}},
		{name: "нулевые значения", in: testPayload{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, err := New(SubjectAdSold, "ads-service", tt.in)
			require.NoError(t, err)

			var out testPayload
			require.NoError(t, env.Decode(&out))
			assert.Equal(t, tt.in, out)
		})
	}
}

func TestEnvelope_Decode_Error(t *testing.T) {
	env := Envelope{Data: []byte("{invalid json")}
	var out testPayload
	assert.Error(t, env.Decode(&out))
}

func TestDLQSubject(t *testing.T) {
	tests := []struct {
		name    string
		durable string
		want    string
	}{
		{name: "обычный durable", durable: "notifications", want: "bozor.dlq.notifications"},
		{name: "пустой durable", durable: "", want: "bozor.dlq."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, DLQSubject(tt.durable))
		})
	}
}
