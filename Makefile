# Bozor — корневой Makefile монорепо.
# Windows без make: используй эквивалентные команды из README («Разработка»).

SHELL := /bin/sh
GOBIN ?= $(shell go env GOPATH)/bin

# Все модули workspace (go.work)
MODULES = $(shell go list -m -f '{{.Dir}}')

# Compose-обёртки: dev тянет override (проброс портов), prod — prod-профиль.
COMPOSE_BASE = docker compose -f deploy/compose/docker-compose.yml
COMPOSE_DEV  = $(COMPOSE_BASE) -f deploy/compose/docker-compose.override.yml --env-file .env
COMPOSE_PROD = $(COMPOSE_BASE) -f deploy/compose/docker-compose.prod.yml --env-file .env

.PHONY: all lint test test-integration build tidy fmt proto gen up up-prod down down-prod ps logs migrate seed help

all: lint test build

## lint: golangci-lint по всем модулям workspace
lint:
	@for dir in $(MODULES); do \
		echo "== lint $$dir"; \
		(cd $$dir && golangci-lint run ./...) || exit 1; \
	done

## test: unit-тесты по всем модулям workspace (с гонками)
test:
	@for dir in $(MODULES); do \
		echo "== test $$dir"; \
		(cd $$dir && go test -race -count=1 ./...) || exit 1; \
	done

## test-integration: интеграционные тесты (testcontainers, требуется Docker)
test-integration:
	@for dir in $(MODULES); do \
		echo "== test-integration $$dir"; \
		(cd $$dir && go test -tags=integration -count=1 ./...) || exit 1; \
	done

## build: сборка всех модулей workspace
build:
	@for dir in $(MODULES); do \
		echo "== build $$dir"; \
		(cd $$dir && go build ./...) || exit 1; \
	done

## tidy: go mod tidy по всем модулям
tidy:
	@for dir in $(MODULES); do \
		echo "== tidy $$dir"; \
		(cd $$dir && go mod tidy) || exit 1; \
	done

## fmt: gofmt по всему репозиторию
fmt:
	gofmt -w services pkg

## proto: генерация Go из proto (buf + protoc-gen-go/-go-grpc) в pkg/shared/pb
proto:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	PATH="$(GOBIN):$$PATH" buf lint
	PATH="$(GOBIN):$$PATH" buf generate

## gen: алиас proto (обратная совместимость)
gen: proto

## up: поднять всю платформу локально (dev: порты проброшены на localhost)
up:
	$(COMPOSE_DEV) up -d

## up-prod: поднять платформу в прод-профиле (наружу только nginx)
up-prod:
	$(COMPOSE_PROD) up -d

## down: остановить платформу (dev)
down:
	$(COMPOSE_DEV) down

## down-prod: остановить платформу (prod)
down-prod:
	$(COMPOSE_PROD) down

## ps: статус контейнеров
ps:
	$(COMPOSE_DEV) ps

## logs: логи платформы (все сервисы)
logs:
	$(COMPOSE_DEV) logs -f --tail=100

## help: список целей
help:
	@grep -E '^## ' Makefile | sed 's/## //'
