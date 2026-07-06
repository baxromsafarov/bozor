package grpcx

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNewServer(t *testing.T) {
	srv := NewServer(slog.Default())
	require.NotNil(t, srv)
	srv.Stop()
}

func TestUnaryRecovery(t *testing.T) {
	tests := []struct {
		name     string
		handler  grpc.UnaryHandler
		wantCode codes.Code
		wantResp any
		wantErr  error
	}{
		{
			name:     "паника превращается в codes.Internal",
			handler:  func(ctx context.Context, req any) (any, error) { panic("boom") },
			wantCode: codes.Internal,
		},
		{
			name:     "успешный вызов проходит без изменений",
			handler:  func(ctx context.Context, req any) (any, error) { return "ok", nil },
			wantResp: "ok",
		},
		{
			name:    "ошибка обработчика проходит без изменений",
			handler: func(ctx context.Context, req any) (any, error) { return nil, errors.New("fail") },
			wantErr: errors.New("fail"),
		},
	}

	interceptor := unaryRecovery(slog.Default())
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp any
			var err error
			assert.NotPanics(t, func() {
				resp, err = interceptor(context.Background(), struct{}{}, info, tt.handler)
			})

			switch {
			case tt.wantCode != codes.OK:
				require.Error(t, err)
				st, ok := status.FromError(err)
				require.True(t, ok)
				assert.Equal(t, tt.wantCode, st.Code())
				assert.Equal(t, "internal error", st.Message())
			case tt.wantErr != nil:
				require.Error(t, err)
				assert.Equal(t, tt.wantErr.Error(), err.Error())
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.wantResp, resp)
			}
		})
	}
}

func TestDial(t *testing.T) {
	conn, err := Dial("localhost:65000")
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.NoError(t, conn.Close())
}
