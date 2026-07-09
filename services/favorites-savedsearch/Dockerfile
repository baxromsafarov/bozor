# syntax=docker/dockerfile:1
# Сборка Favorites/SavedSearch-сервиса. Контекст сборки — корень монорепо (нужен pkg/shared).

FROM golang:1.26-alpine AS build
WORKDIR /src

# Копируем только нужные модули и собираем локальный workspace (без go.work
# репозитория, который ссылается на прочие сервисы, отсутствующие в контексте).
COPY pkg/shared ./pkg/shared
COPY services/favorites-savedsearch ./services/favorites-savedsearch
RUN go work init ./pkg/shared ./services/favorites-savedsearch

WORKDIR /src/services/favorites-savedsearch
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/favorites ./cmd/server

# Рантайм: distroless static, без shell, непривилегированный пользователь.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/favorites /favorites
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/favorites"]
