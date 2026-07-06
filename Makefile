# Bozor — корневой Makefile монорепо.
# Windows без make: используй эквивалентные команды из README («Разработка»).

SHELL := /bin/sh
GOBIN ?= $(shell go env GOPATH)/bin

# Все модули workspace (go.work)
MODULES = $(shell go list -m -f '{{.Dir}}')

.PHONY: all lint test build tidy fmt gen up down ps logs migrate seed help

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

## gen: генерация кода из proto (buf)
gen:
	cd api/proto && buf generate

## up: поднять всю платформу локально
up:
	docker compose -f deploy/compose/docker-compose.yml --env-file .env up -d

## down: остановить платформу
down:
	docker compose -f deploy/compose/docker-compose.yml --env-file .env down

## ps: статус контейнеров
ps:
	docker compose -f deploy/compose/docker-compose.yml --env-file .env ps

## logs: логи платформы (все сервисы)
logs:
	docker compose -f deploy/compose/docker-compose.yml --env-file .env logs -f --tail=100

## help: список целей
help:
	@grep -E '^## ' Makefile | sed 's/## //'
