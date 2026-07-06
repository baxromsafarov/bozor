# Инфраструктура развёртывания

- `compose/` — docker-compose.yml (+ override/prod) для всей платформы (Stage 0.3).
- `nginx/` — конфигурация edge-балансировщика (TLS, LB, апстримы).
- `migrations/` — goose-миграции, по каталогу на сервис: `migrations/<service>/`.

Путь масштабирования (Compose → Swarm → K8s) — в `docs/ARCHITECTURE.md`.
