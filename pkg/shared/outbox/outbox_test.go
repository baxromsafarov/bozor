package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"
)

// TODO(outbox): интеграционный тест Relay.Run/RunOnce с реальным PostgreSQL
// через testcontainers (FOR UPDATE SKIP LOCKED, конкуренция нескольких relay).

// fakeExecer — фейковый Execer, захватывающий SQL и аргументы.
type fakeExecer struct {
	sql  string
	args []any
	err  error
}

func (f *fakeExecer) Exec(_ context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	f.sql = sql
	f.args = arguments
	if f.err != nil {
		return pgconn.CommandTag{}, f.err
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

// fakeRow — фейковый pgx.Row, отдающий заранее заданное булево значение.
type fakeRow struct {
	exists bool
	err    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) == 1 {
		if p, ok := dest[0].(*bool); ok {
			*p = r.exists
		}
	}
	return nil
}

// fakeQuerier — фейковый Querier, захватывающий SQL и аргументы.
type fakeQuerier struct {
	sql  string
	args []any
	row  fakeRow
}

func (f *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.sql = sql
	f.args = args
	return f.row
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestEnqueue(t *testing.T) {
	env, err := events.New("orders.created", "orders", map[string]string{"order_id": "42"})
	require.NoError(t, err)

	tests := []struct {
		name    string
		execErr error
		wantErr bool
	}{
		{name: "успешная вставка"},
		{name: "ошибка БД оборачивается", execErr: errors.New("connection refused"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &fakeExecer{err: tt.execErr}
			err := Enqueue(context.Background(), db, env)

			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.execErr, "исходная ошибка должна быть обёрнута через %%w")
				return
			}
			require.NoError(t, err)

			assert.Contains(t, db.sql, "INSERT INTO outbox")
			require.Len(t, db.args, 3)
			assert.Equal(t, env.ID, db.args[0], "id должен браться из env.ID")
			assert.Equal(t, env.Type, db.args[1], "subject должен браться из env.Type")

			payload, ok := db.args[2].([]byte)
			require.True(t, ok, "payload должен передаваться как JSON-байты")
			var decoded events.Envelope
			require.NoError(t, json.Unmarshal(payload, &decoded))
			assert.Equal(t, env.ID, decoded.ID)
			assert.Equal(t, env.Type, decoded.Type)
			assert.Equal(t, env.Source, decoded.Source)
			assert.JSONEq(t, string(env.Data), string(decoded.Data))
		})
	}
}

func TestMarkProcessed(t *testing.T) {
	tests := []struct {
		name     string
		consumer string
		eventID  string
		execErr  error
		wantErr  bool
	}{
		{name: "успешная отметка", consumer: "orders-worker", eventID: "0197a6de-0000-7000-8000-000000000001"},
		{name: "ошибка БД оборачивается", consumer: "c", eventID: "e", execErr: errors.New("boom"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &fakeExecer{err: tt.execErr}
			err := MarkProcessed(context.Background(), db, tt.consumer, tt.eventID)

			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.execErr)
				return
			}
			require.NoError(t, err)

			assert.Contains(t, db.sql, "INSERT INTO processed_events")
			assert.Contains(t, db.sql, "ON CONFLICT DO NOTHING", "повторная отметка должна быть идемпотентной")
			assert.Equal(t, []any{tt.consumer, tt.eventID}, db.args)
		})
	}
}

func TestAlreadyProcessed(t *testing.T) {
	tests := []struct {
		name    string
		row     fakeRow
		want    bool
		wantErr bool
	}{
		{name: "событие уже обработано", row: fakeRow{exists: true}, want: true},
		{name: "событие ещё не обработано", row: fakeRow{exists: false}, want: false},
		{name: "ошибка запроса оборачивается", row: fakeRow{err: errors.New("scan failed")}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &fakeQuerier{row: tt.row}
			got, err := AlreadyProcessed(context.Background(), db, "orders-worker", "0197a6de-0000-7000-8000-000000000002")

			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.row.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)

			assert.Contains(t, db.sql, "SELECT EXISTS")
			assert.Contains(t, db.sql, "processed_events")
			assert.Equal(t, []any{"orders-worker", "0197a6de-0000-7000-8000-000000000002"}, db.args)
		})
	}
}

func TestPublishBatch(t *testing.T) {
	mustPayload := func(t *testing.T, id, subject string) []byte {
		t.Helper()
		raw, err := json.Marshal(events.Envelope{ID: id, Type: subject, SpecVersion: "1.0", Data: json.RawMessage(`{}`)})
		require.NoError(t, err)
		return raw
	}

	tests := []struct {
		name          string
		msgs          func(t *testing.T) []message
		failSubjects  map[string]bool
		wantPublished []string
	}{
		{
			name:          "пустая партия",
			msgs:          func(_ *testing.T) []message { return nil },
			wantPublished: []string{},
		},
		{
			name: "все сообщения публикуются",
			msgs: func(t *testing.T) []message {
				return []message{
					{ID: "id-1", Payload: mustPayload(t, "id-1", "a.b")},
					{ID: "id-2", Payload: mustPayload(t, "id-2", "a.c")},
				}
			},
			wantPublished: []string{"id-1", "id-2"},
		},
		{
			name: "битый payload пропускается, остальные публикуются",
			msgs: func(t *testing.T) []message {
				return []message{
					{ID: "id-1", Payload: []byte(`{не json`)},
					{ID: "id-2", Payload: mustPayload(t, "id-2", "a.c")},
				}
			},
			wantPublished: []string{"id-2"},
		},
		{
			name: "ошибка публикации одного не валит остальные",
			msgs: func(t *testing.T) []message {
				return []message{
					{ID: "id-1", Payload: mustPayload(t, "id-1", "fail.subject")},
					{ID: "id-2", Payload: mustPayload(t, "id-2", "ok.subject")},
					{ID: "id-3", Payload: mustPayload(t, "id-3", "ok.subject")},
				}
			},
			failSubjects:  map[string]bool{"fail.subject": true},
			wantPublished: []string{"id-2", "id-3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var publishedEnvs []events.Envelope
			publish := func(_ context.Context, env events.Envelope) error {
				if tt.failSubjects[env.Type] {
					return errors.New("publish failed")
				}
				publishedEnvs = append(publishedEnvs, env)
				return nil
			}

			got := publishBatch(context.Background(), publish, discardLogger(), tt.msgs(t))
			assert.Equal(t, tt.wantPublished, got)
			assert.Len(t, publishedEnvs, len(tt.wantPublished))
		})
	}
}

// TestRelayDefaults проверяет значения по умолчанию для BatchSize и Interval.
func TestRelayDefaults(t *testing.T) {
	tests := []struct {
		name         string
		relay        Relay
		wantBatch    int
		wantInterval string
	}{
		{name: "нулевые значения заменяются дефолтами", relay: Relay{}, wantBatch: 100, wantInterval: "500ms"},
		{name: "явные значения сохраняются", relay: Relay{BatchSize: 7, Interval: 42_000_000}, wantBatch: 7, wantInterval: "42ms"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantBatch, tt.relay.batchSize())
			assert.Equal(t, tt.wantInterval, tt.relay.interval().String())
		})
	}
}
