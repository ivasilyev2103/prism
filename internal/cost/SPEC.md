# Cost — Module Spec

> Загружать при работе над `internal/cost/`.
> Дополнительно: `docs/INTERFACES.md`.


## Назначение

Учёт расходов и потребления квот. Поддерживает many-to-many между проектами и провайдерами, множественные модели биллинга, иерархию бюджетов из 4 уровней. Использует in-memory write buffer с периодическим flush для снижения write contention при concurrent нагрузке.

## Реализуемые интерфейсы

- `cost.Tracker`
- `policy.BudgetChecker` (делегирует к Tracker)

## Зависимости

| Использует | `internal/types` | RequestRecord, ProviderID, UsageMetrics, BillingType |
| Используется в | `internal/policy` | BudgetChecker, Tracker.Record |

## Модели биллинга (BillingType)

| BillingType | Как считается стоимость | Примеры |
|-------------|------------------------|---------|
| `per_token` | tokens × price_per_1M | Claude API, GPT API, Gemini |
| `per_image` | images × price_per_image | DALL-E, Midjourney |
| `per_request` | фиксированная цена за вызов | Moderation API |
| `per_second` | compute_time × price_per_second | 3D generation, длинный TTS |
| `subscription` | квоты (requests, tokens, images, compute) | Claude Pro, GPT Plus |

Один провайдер может использовать **разные BillingType** для разных ServiceType. Например, OpenAI: `per_token` для chat, `per_image` для DALL-E, `per_request` для moderation. Tracker корректно обрабатывает все типы в рамках одного провайдера.

## UsageMetrics

Метрики потребления ресурсов заполняются в зависимости от типа сервиса:

```go
type UsageMetrics struct {
    InputTokens  int     // chat, embedding
    OutputTokens int     // chat
    ImagesCount  int     // image generation
    AudioSeconds float64 // TTS, STT
    ComputeUnits float64 // 3D, generic compute
}
```

| ServiceType | Заполняемые поля | BillingType |
|-------------|-----------------|-------------|
| chat | InputTokens, OutputTokens | per_token / subscription |
| embedding | InputTokens | per_token |
| image | ImagesCount | per_image |
| tts | AudioSeconds | per_second |
| stt | AudioSeconds | per_second |
| 3d_model | ComputeUnits | per_second |
| moderation | (нет метрик потребления) | per_request |

## In-memory write buffer

Cost Tracker использует **in-memory write buffer** для снижения write contention:

1. `Record()` добавляет `RequestRecord` в ring buffer (lock-free или mutex-protected)
2. Фоновая горутина flush в SQLite пакетами:
   - Каждую **1 секунду** (по таймеру)
   - Или при **100 записях** в буфере (что наступит раньше)
3. Flush использует `BEGIN IMMEDIATE` + batch INSERT для атомарности
4. `Flush(ctx)` — принудительный flush, вызывается при **graceful shutdown**

```
Record() → [ring buffer] → flush goroutine → BEGIN IMMEDIATE → batch INSERT → COMMIT
                                ↑ timer (1s)
                                ↑ buffer full (100)
                                ↑ Flush() (shutdown)
```

При ошибке flush: retry с exponential backoff (3 попытки), затем потеря записей с логированием в stderr. Данные в буфере не дублируются на диск — crash = потеря до 1s/100 записей.

## SQL schema

```sql
CREATE TABLE providers (
    id              TEXT PRIMARY KEY,
    display_name    TEXT NOT NULL,
    billing_type    TEXT NOT NULL,     -- per_token | per_image | per_request | per_second | subscription
    -- per_token pricing
    price_input_per_1m  REAL,
    price_output_per_1m REAL,
    -- per_image / per_request pricing
    price_per_unit      REAL,
    -- per_second pricing
    price_per_second    REAL,
    prices_updated_at   INTEGER,
    -- subscription
    sub_plan_name        TEXT,
    sub_period           TEXT,
    sub_cost_usd         REAL,
    sub_reset_day        INTEGER,
    sub_quota_requests   INTEGER,
    sub_quota_input_tokens  INTEGER,
    sub_quota_output_tokens INTEGER,
    sub_quota_images     INTEGER
);

CREATE TABLE requests (
    id            TEXT PRIMARY KEY,
    ts            INTEGER NOT NULL,
    project_id    TEXT NOT NULL,
    provider_id   TEXT NOT NULL REFERENCES providers(id),
    service_type  TEXT NOT NULL,
    model         TEXT NOT NULL,
    -- usage metrics (заполняются в зависимости от service_type)
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    images_count  INTEGER NOT NULL DEFAULT 0,
    audio_seconds REAL NOT NULL DEFAULT 0,
    compute_units REAL NOT NULL DEFAULT 0,
    -- cost
    cost_usd      REAL NOT NULL DEFAULT 0,
    billing_type  TEXT NOT NULL,
    -- metadata
    latency_ms    INTEGER NOT NULL,
    privacy_score REAL,
    cache_hit     BOOLEAN NOT NULL DEFAULT 0,
    route_matched TEXT,
    status        TEXT NOT NULL
);

CREATE INDEX idx_requests_project  ON requests(project_id, ts);
CREATE INDEX idx_requests_provider ON requests(provider_id, ts);
CREATE INDEX idx_requests_pair     ON requests(project_id, provider_id, ts);
CREATE INDEX idx_requests_service  ON requests(service_type, ts);
```

БД: `modernc.org/sqlite` (pure Go, без CGO).

## Иерархия бюджетов (четыре уровня)

```
                    ┌─────────────────────┐
                    │   GLOBAL budget     │
                    └──────────┬──────────┘
               ┌───────────────┼───────────────┐
               ▼               ▼               ▼
    ┌──────────────────┐      ...    ┌──────────────────┐
    │ PROJECT budget   │             │ PROVIDER budget  │
    └────────┬─────────┘             └────────┬─────────┘
             └──────────────┬─────────────────┘
                            ▼
               ┌─────────────────────────┐
               │  PROJECT×PROVIDER budget│
               └─────────────────────────┘
```

Проверяются **все применимые бюджеты** от конкретного к общему, срабатывает самый строгий:
- Пара project×provider
- Бюджет проекта (все провайдеры)
- Бюджет провайдера (все проекты)
- Глобальный бюджет

Действия при превышении: `block` (429), `downgrade_model`, `alert`.

## Подписки (subscription tracking)

Для subscription-провайдеров Cost Tracker отслеживает потребление квот за текущий период:

```go
type QuotaUsage struct {
    ProviderID       types.ProviderID
    PeriodStart      time.Time
    PeriodEnd        time.Time
    RequestsUsed     int64
    RequestsLimit    *int64  // nil = безлимитно
    InputTokensUsed  int64
    InputTokensLimit *int64
    OutputTokensUsed  int64
    OutputTokensLimit *int64
    ImagesUsed       int64
    ImagesLimit      *int64
}
```

Квоты сбрасываются в `sub_reset_day` каждого месяца. При исчерпании квоты возвращается `QuotaExceededError`.

## Структура файлов

```
internal/cost/
├── cost.go           # интерфейсы
├── tracker.go        # реализация Tracker с write buffer
├── buffer.go         # ring buffer + flush goroutine
├── budget.go         # BudgetChecker, 4-уровневая иерархия
├── subscription.go   # учёт квот подписок
├── schema.go         # DDL (CREATE TABLE, индексы)
├── doc.go
├── tracker_test.go
├── buffer_test.go
├── budget_test.go
└── SPEC.md
```

## Ключевые ограничения

### Агрегация по паре project×provider в реальном времени
**Ограничение**: `Summary` выполняет GROUP BY в SQLite — при большом объёме (>100K записей) может быть медленным.
**Воркэраунд**: индексы на `(project_id, ts)`, `(provider_id, ts)`, `(project_id, provider_id, ts)`, `(service_type, ts)`.
**Планируется**: materialized view или агрегация по расписанию (Фаза 9+).

### Budget Guard: оценка стоимости — приблизительная
**Ограничение**: `estimated_cost` вычисляется до ответа провайдера. Для `per_token` — output tokens неизвестны (используется `max_tokens` как верхняя граница). Для `per_image`, `per_request` — оценка точная. Для `per_second` — приблизительная. Фактическая стоимость пишется после ответа.

### Потеря данных при crash
**Ограничение**: in-memory buffer не дублируется на диск. При аварийном завершении теряются записи из буфера (до 1 секунды или 100 записей).
**Воркэраунд**: `Flush()` вызывается при graceful shutdown (SIGTERM/SIGINT). Для аварий — потеря допустима (метаданные, не финансовые транзакции).

## Пример использования

```go
// Запись после каждого запроса (async, через write buffer)
tracker.Record(ctx, &types.RequestRecord{
    ID:          requestID,
    ProjectID:   "game-engine",
    ProviderID:  types.ProviderClaude,
    ServiceType: types.ServiceChat,
    Model:       "claude-haiku-4-5",
    Usage:       types.UsageMetrics{InputTokens: 312, OutputTokens: 89},
    CostUSD:     0.000201,
    BillingType: types.BillingPerToken,
    Status:      "ok",
})

// Запись image generation запроса
tracker.Record(ctx, &types.RequestRecord{
    ID:          requestID,
    ProjectID:   "ai-influencer",
    ProviderID:  types.ProviderOpenAI,
    ServiceType: types.ServiceImage,
    Model:       "dall-e-3",
    Usage:       types.UsageMetrics{ImagesCount: 2},
    CostUSD:     0.080,
    BillingType: types.BillingPerImage,
    Status:      "ok",
})

// Агрегация по паре проект×провайдер
summary, err := tracker.Summary(ctx, "game-engine", types.ProviderClaude, from, to)

// Graceful shutdown: принудительный flush буфера
if err := tracker.Flush(ctx); err != nil {
    log.Printf("cost flush error: %v", err)
}
```
