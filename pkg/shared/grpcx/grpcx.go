// Package grpcx содержит вспомогательные функции для создания gRPC-серверов
// и клиентских соединений с телеметрией и восстановлением после паник.
package grpcx

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// NewServer создаёт gRPC-сервер с OpenTelemetry-инструментацией и
// recovery-интерсепторами: паника в обработчике логируется и превращается
// в ошибку codes.Internal вместо падения всего процесса.
func NewServer(log *slog.Logger, opts ...grpc.ServerOption) *grpc.Server {
	all := make([]grpc.ServerOption, 0, 3+len(opts))
	all = append(all,
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(unaryRecovery(log)),
		grpc.ChainStreamInterceptor(streamRecovery(log)),
	)
	all = append(all, opts...)
	return grpc.NewServer(all...)
}

// Dial создаёт клиентское gRPC-соединение без TLS (для внутренней сети)
// с OpenTelemetry-инструментацией исходящих вызовов.
func Dial(target string) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, fmt.Errorf("grpcx: подключение к %q: %w", target, err)
	}
	return conn, nil
}

// unaryRecovery возвращает unary-интерсептор, перехватывающий паники.
func unaryRecovery(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logPanic(ctx, log, info.FullMethod, r)
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}

// streamRecovery возвращает stream-интерсептор, перехватывающий паники.
func streamRecovery(log *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				logPanic(ss.Context(), log, info.FullMethod, r)
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(srv, ss)
	}
}

// logPanic пишет сообщение о панике в лог, если логгер задан.
func logPanic(ctx context.Context, log *slog.Logger, method string, r any) {
	if log == nil {
		return
	}
	log.ErrorContext(ctx, "паника в gRPC-обработчике",
		slog.String("method", method),
		slog.Any("panic", r),
	)
}
