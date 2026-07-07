# syntax=docker/dockerfile:1
# Сборка API Gateway. Контекст сборки — корень монорепо (нужны go.work + pkg/shared).

FROM golang:1.26-alpine AS build
WORKDIR /src

# Кешируемый слой зависимостей: сначала манифесты модулей workspace.
COPY go.work go.work.sum ./
COPY pkg/shared/go.mod pkg/shared/go.sum ./pkg/shared/
COPY services/gateway/go.mod services/gateway/go.sum ./services/gateway/
RUN go mod download

# Исходники и сборка статического бинаря.
COPY pkg/shared ./pkg/shared
COPY services/gateway ./services/gateway
WORKDIR /src/services/gateway
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/server

# Рантайм: distroless static, без shell, непривилегированный пользователь.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gateway /gateway
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/gateway"]
