# Audit — Module Spec

> Загружать при работе над `internal/audit/`.
> Дополнительно: `docs/INTERFACES.md`, `docs/SECURITY.md`.


## Назначение

Append-only лог метаданных запросов. Тела запросов/ответов не хранятся никогда. WORM-семантика через SQLite триггеры + **обязательная HMAC chain** для tamper detection. Каждая запись содержит `ServiceType` для фильтрации по типу AI-сервиса.

## Реализуемые интерфейсы

- `audit.Logger`

## Зависимости

| Использует | `internal/types` | RequestRecord |
| Используется в | `internal/policy` | Logger.Log |

## HMAC chain (обязательная)

HMAC chain — **основной** механизм защиты целостности audit log. Каждая запись содержит HMAC, вычисленный как цепочка:

```
record[0].hmac = HMAC-SHA256(key, serialize(record[0]) || "genesis")
record[n].hmac = HMAC-SHA256(key, serialize(record[n]) || record[n-1].hmac)
```

Любая модификация, удаление или вставка записи разрывает цепочку и обнаруживается через `VerifyChain()`.

### Производство HMAC ключа

HMAC ключ **отделён** от encryption key и производится из master password через HKDF:

```
Master Password
    │
    ▼ Argon2id → master_key
    │
    ├── HKDF(master_key, salt, info="prism-vault-encryption") → encryption_key (для Vault)
    └── HKDF(master_key, salt, info="prism-audit-hmac")       → hmac_key (для Audit)
```

Разделение ключей гарантирует: компрометация encryption key не компрометирует audit HMAC, и наоборот.

### VerifyChain

```go
// VerifyChain проверяет целостность HMAC-цепочки за указанный период.
// Возвращает nil если цепочка не нарушена.
// При нарушении возвращает ошибку с указанием первой повреждённой записи.
VerifyChain(ctx context.Context, from, to time.Time) error
```

Верификация:
1. Загружает все записи за период `[from, to]` упорядоченные по `rowid`
2. Для первой записи периода: загружает предыдущую запись (или "genesis" если это начало лога)
3. Последовательно вычисляет `expected_hmac` для каждой записи и сравнивает с сохранённым
4. При расхождении: возвращает ошибку с ID записи, где цепочка разорвана

Рекомендуется вызывать `VerifyChain` при каждом запуске Prism и периодически (cron).

## ServiceType в audit записях

Каждая запись содержит `ServiceType` из `types.RequestRecord`. Это позволяет:
- Фильтрацию аудит-лога по типу сервиса (`Filter.ServiceType`)
- Анализ: сколько запросов по каждому типу (chat, image, embedding и др.)
- Корреляцию с Cost Tracker по service_type

## Что логируется

```json
{
  "id": "req_01jq...",
  "ts": 1742811600,
  "project_id": "game-engine",
  "provider": "claude",
  "service_type": "chat",
  "model": "claude-haiku-4-5",
  "input_tokens": 312,
  "output_tokens": 89,
  "images_count": 0,
  "audio_seconds": 0,
  "compute_units": 0,
  "cost_usd": 0.000201,
  "billing_type": "per_token",
  "latency_ms": 847,
  "privacy_score": 0.12,
  "pii_entities_found": 0,
  "cache_hit": false,
  "route_matched": "code_tasks",
  "status": "ok",
  "hmac": "a3f9b2c1..."
}
```

Поля `images_count`, `audio_seconds`, `compute_units` заполняются в зависимости от `service_type`. Тела запросов и ответов **не логируются никогда**.

## WORM-семантика через SQLite триггеры

SQLite триггеры запрещают UPDATE и DELETE на таблице audit:

```sql
CREATE TRIGGER audit_no_update BEFORE UPDATE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit log is immutable: UPDATE not allowed');
END;

CREATE TRIGGER audit_no_delete BEFORE DELETE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit log is immutable: DELETE not allowed');
END;
```

**Важно**: WORM-триггеры — **недостаточная** защита сами по себе. Прямое открытие `audit.db` сторонним инструментом (sqlite3 CLI, DB Browser) обходит триггеры. HMAC chain — основная защита от tampering. Триггеры — дополнительный барьер от случайных модификаций через Prism код.

## Структура файлов

```
internal/audit/
├── audit.go          # интерфейс Logger, Filter
├── logger.go         # реализация Logger с HMAC chain
├── hmac.go           # HMAC вычисление и верификация
├── schema.go         # DDL с WORM-триггерами
├── doc.go
├── logger_test.go
├── hmac_test.go
└── SPEC.md
```

## Ключевые ограничения

### Тела запросов не хранятся никогда
**Это инвариант**, не ограничение. Logger принимает только `*types.RequestRecord` — структуру без тел.
**Проверяется тестом**: `TestAuditLog_NoPIIInEntries`.

### HMAC chain — последовательная запись
**Ограничение**: HMAC chain требует последовательной записи (каждый HMAC зависит от предыдущего). Concurrent writes сериализуются через mutex.
**Воркэраунд**: audit log записывается асинхронно после ответа клиенту. Сериализация не влияет на latency запросов.

### VerifyChain — линейная сложность
**Ограничение**: `VerifyChain(from, to)` загружает все записи за период и проверяет последовательно. Для больших периодов (>1M записей) может быть медленным.
**Воркэраунд**: периодическая верификация за последние N дней (не за весь лог). Полная верификация — при необходимости, фоновой задачей.

### WORM-триггеры обходятся вне Prism
**Ограничение**: SQLite триггеры — программная защита. Прямое открытие `audit.db` сторонним инструментом обходит их.
**Защита**: HMAC chain обнаруживает любую модификацию, даже выполненную вне Prism. `VerifyChain()` при старте Prism выявит tampering.

## Пример использования

```go
// Logger не принимает тела запросов — только метаданные
// HMAC вычисляется автоматически: hmac = sha256(content || prev_hmac)
err := logger.Log(ctx, &types.RequestRecord{
    ID:          requestID,
    ProjectID:   "game-engine",
    ProviderID:  types.ProviderClaude,
    ServiceType: types.ServiceChat,
    Model:       "claude-haiku-4-5",
    Usage:       types.UsageMetrics{InputTokens: 312, OutputTokens: 89},
    CostUSD:     0.000201,
    BillingType: types.BillingPerToken,
    PrivacyScore: 0.12,
    Status:      "ok",
    // Content запроса/ответа — НЕ передаётся
})

// Image generation запрос в audit log
err := logger.Log(ctx, &types.RequestRecord{
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

// Верификация целостности HMAC chain
err := logger.VerifyChain(ctx, from, to)
if err != nil {
    // Цепочка нарушена — tampering detected
    log.Fatalf("audit chain integrity violation: %v", err)
}

// Фильтрация по ServiceType
entries, err := logger.Query(ctx, &audit.Filter{
    ServiceType: types.ServiceImage,
    From:        from,
    To:          to,
})
```
