# Bozor — Sync Protocol

## Назначение
Формальный протокол синхронизации контекста между чатами для непрерывности разработки.

## Основные принципы
1. STATUS.yaml — единственный источник истины о состоянии проекта.
2. Каждый чат начинается с загрузки контекста из файлов SPMS.
3. Каждый чат заканчивается обновлением STATUS.yaml с новой версией.
4. Никакие данные не теряются между чатами.

## Начало нового чата
Разработчик предоставляет: DESCRIPTION.md, ROADMAP.md, RULES.md, ARCHITECTURE.md, STATUS.yaml (обязательно), DECISIONS.md (желательно).
AI валидирует STATUS.yaml (наличие status_id, current.chapter/stage/status; соответствие ROADMAP; консистентность completed; отсутствие открытых блокеров) и подтверждает позицию:

    Контекст загружен:
    - Проект: Bozor
    - Глава: CHAPTER X: <название>
    - Этап: STAGE X.Y: <название>
    - Статус: <in_progress|completed|blocked>
    - Завершено этапов: N из M
    - Открытых вопросов: K
    Готов продолжить с текущего этапа?

## Работа в течение чата
При завершении Stage AI: проверяет DoD, собирает доказательства, создаёт новую версию STATUS.yaml (новый status_id, обновлённые completed/current/next_actions/metrics), пишет решения в DECISIONS.md.
При архитектурном решении: обсуждает варианты → фиксирует ADR в DECISIONS.md → при необходимости обновляет ARCHITECTURE.md.
При блокере: добавляет вопрос в open_questions, ставит current.status=blocked, НЕ продолжает до решения.

## Завершение чата
AI создаёт финальную версию STATUS.yaml и предоставляет summary: что сделано, что в процессе (%), следующие действия, новый status_id, число открытых блокеров.

## Аварийное восстановление
Если файлы недоступны — разработчик даёт EMERGENCY_SYNC.yaml; AI запрашивает недостающее (DESCRIPTION/ROADMAP), восстанавливает STATUS.yaml, просит подтверждение.

## Валидация STATUS.yaml
Обязательные поля: status_id, project, current.chapter, current.stage, current.status.
Логика: current.stage существует в ROADMAP; completed предшествуют current; metrics.completed_stages == длине completed; если status=blocked — open_questions не пуст.

Текущая версия протокола: 1.0
