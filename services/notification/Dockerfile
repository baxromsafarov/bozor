# syntax=docker/dockerfile:1
# Сборка Notification-сервиса. Контекст сборки — корень монорепо (нужен pkg/shared).

FROM golang:1.26-alpine AS build
WORKDIR /src

# Копируем только нужные модули и собираем локальный workspace (без go.work
# репозитория, который ссылается на прочие сервисы, отсутствующие в контексте).
COPY pkg/shared ./pkg/shared
COPY services/notification ./services/notification
RUN go work init ./pkg/shared ./services/notification

WORKDIR /src/services/notification
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/notification ./cmd/server

# Рантайм: distroless static, без shell, непривилегированный пользователь.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/notification /notification
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/notification"]
