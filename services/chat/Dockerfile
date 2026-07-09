# syntax=docker/dockerfile:1
# Сборка Chat-сервиса. Контекст сборки — корень монорепо (нужен pkg/shared).

FROM golang:1.26-alpine AS build
WORKDIR /src

# Копируем только нужные модули и собираем локальный workspace (без go.work
# репозитория, который ссылается на прочие сервисы, отсутствующие в контексте).
COPY pkg/shared ./pkg/shared
COPY services/chat ./services/chat
RUN go work init ./pkg/shared ./services/chat

WORKDIR /src/services/chat
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/chat ./cmd/server

# Рантайм: distroless static, без shell, непривилегированный пользователь.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/chat /chat
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/chat"]
