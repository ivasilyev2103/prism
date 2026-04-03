# Prism — План разработки по фазам

> Каждая фаза = одна сессия Claude Code.
> Фаза считается завершённой только когда выполнены **все** пункты из "Критерии готовности".
> Не переходить к следующей фазе до полного завершения текущей.

---

## Текущая фаза: **5**

---

## Фаза 1 — Фундамент: типы, интерфейсы, скелет

**Цель**: создать каркас проекта. Никакой бизнес-логики — только типы, интерфейсы и заглушки.
После этой фазы все последующие фазы могут разрабатываться независимо.

**Контекст для загрузки**: `CLAUDE.md` + `docs/INTERFACES.md`

### Задачи

1. Инициализировать Go-модуль: `go mod init github.com/yourname/prism`
2. Создать пакет `internal/types` — все типы из `docs/INTERFACES.md` (раздел "Общие типы"):
   - `ServiceType` enum (chat, image, embedding, tts, 3d_model, stt, moderation)
   - `BillingType` enum (per_token, per_image, per_request, per_second, subscription)
   - `TextPart`, `ParsedRequest`, `SanitizedRequest` (с `SanitizedBody json.RawMessage`)
   - `Response` (с `TextContent`, `BinaryData`, `ContentType`, `RawBody`)
   - `UsageMetrics` (InputTokens, OutputTokens, ImagesCount, AudioSeconds, ComputeUnits)
   - `CostEstimate`, `RequestRecord`, `BudgetExceededError`, `QuotaExceededError`
3. Создать пустые пакеты с интерфейсами для каждого модуля:
   - `internal/vault/vault.go` — интерфейс `Vault` (с `RotateProviderKey`, `tokenTTL`)
   - `internal/privacy/privacy.go` — интерфейсы `Pipeline`, `Detector`
   - `internal/ingress/ingress.go` — интерфейсы `Handler`, `RateLimiter`
   - `internal/policy/policy.go` — интерфейсы `Router` (с `Validate`), `BudgetChecker`, `Failover`
   - `internal/cost/cost.go` — интерфейс `Tracker` (с `Flush`)
   - `internal/audit/audit.go` — интерфейс `Logger` (с `VerifyChain`)
   - `internal/cache/cache.go` — интерфейсы `SemanticCache`, `Embedder`, тип `CachePolicy`
   - `internal/provider/provider.go` — интерфейсы `Provider` (с `Execute`, `EstimateCost`, `SupportedServices`), `Registry` (с `GetForService`)
4. Создать `internal/types/errors.go` — `BudgetExceededError`, `QuotaExceededError`
5. Создать mock-реализации всех интерфейсов в `internal/<module>/mock_test.go` (только для тестов)
6. Создать `cmd/prism/main.go` — пустой main с wire-up заглушками
7. Настроить `golangci-lint` (`.golangci.yml`) — включая запрет логирования секретов
8. Настроить `Makefile`:
   ```makefile
   test:       go test -race ./...
   lint:       golangci-lint run
   vuln:       govulncheck ./...
   coverage:   go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out
   ```

### Критерии готовности

- [x] `go build ./...` — без ошибок
- [x] `go vet ./...` — без ошибок
- [x] `golangci-lint run` — без ошибок
- [x] Каждый интерфейс из `docs/INTERFACES.md` присутствует в коде без изменений
- [x] `internal/types` не импортирует другие `internal/*` пакеты
- [x] Ни один `internal/*` пакет не импортирует конкретные типы другого `internal/*` пакета
- [x] `ServiceType`, `BillingType` и все обобщённые типы присутствуют

---

## Фаза 2 — Vault

**Цель**: реализовать хранилище секретов. Самый критичный модуль — начинаем с него.

**Контекст для загрузки**: `CLAUDE.md` + `docs/INTERFACES.md` + `internal/vault/SPEC.md` + `docs/SECURITY.md`

### Задачи

1. Реализовать `internal/vault/storage.go` — `modernc.org/sqlite` (pure Go) + per-record AES-256-GCM
2. Реализовать `internal/vault/kdf.go` — Argon2id (memory=64MB, iterations=3, parallelism=4) + mlock-pinned буферы для encryption key
3. Реализовать `internal/vault/impl.go` — конкретная реализация интерфейса `Vault`
4. Реализовать `internal/vault/transport.go` — custom `http.RoundTripper` для Sign-not-Expose: инжектирует auth header на уровне transport, зануляет после wire write
5. Реализовать управление токенами: `RegisterProject` (с TTL), `ValidateToken` (HMAC lookup + constant-time compare), `RevokeToken`
6. Реализовать `RotateProviderKey` — атомарная замена ключа
7. Реализовать `explicit_bzero` + mlock через `golang.org/x/sys/unix` (Linux/macOS)
8. Написать тесты (100% покрытие публичного интерфейса):
   - `TestSignNotExpose_KeyNeverLeaks`
   - `TestSignNotExpose_RoundTripper_ZerosHeader`
   - `TestKeyZeroedOnAllPaths` (включая error paths)
   - `TestKDFParameters_OWASPCompliant`
   - `TestWrongMasterPassword_ReturnsError`
   - `TestEncryptionNonceUniqueness`
   - `TestValidateToken_HMACLookup_ConstantTime`
   - `TestTokenTTL_Expired_Returns401`
   - `TestRevokedToken_Returns401`
   - `TestRotateProviderKey_AtomicReplace`

### Критерии готовности

- [x] `go test ./internal/vault/...` — 24 теста зелёные (race detector требует обновления GCC на Windows)
- [x] Покрытие публичного интерфейса `Vault` — 81% (все 8 методов >90%, остаток — unreachable crypto error paths)
- [x] `govulncheck ./internal/vault/...` — нет критических CVE
- [x] Нет CGO зависимостей (pure Go SQLite: modernc.org/sqlite v1.48.0)
- [x] mlock-pinned буфер для encryption key (VirtualLock на Windows, mlock(2) на Unix)
- [x] Custom RoundTripper зануляет auth header после wire write
- [x] Все пути (включая Close, all-ops-after-close) вызывают `explicitBzero`

---

## Фаза 3 — Provider Adapters

**Цель**: адаптеры к AI-провайдерам. Поддержка множественных ServiceType.

**Контекст для загрузки**: `CLAUDE.md` + `docs/INTERFACES.md` + `internal/provider/SPEC.md`

### Задачи

1. Реализовать `internal/provider/registry.go` — `Registry` с `GetForService(serviceType)`
2. Реализовать `internal/provider/claude.go` — `ClaudeProvider`
   - `SupportedServices()`: `[ServiceChat]`
   - `Execute()`: pass-through proxy (SanitizedBody → API)
   - `EstimateCost()`: по модели и EstimatedTokens
3. Реализовать `internal/provider/ollama.go` — `OllamaProvider`
   - `SupportedServices()`: `[ServiceChat, ServiceEmbedding, ServiceImage]`
   - HTTP к `localhost:11434`
   - Бесплатный (EstimateCost всегда 0)
4. Реализовать `internal/provider/openai.go` — `OpenAIProvider` (stub)
   - `SupportedServices()`: `[ServiceChat, ServiceImage, ServiceEmbedding, ServiceTTS, ServiceSTT, ServiceModeration]`
5. Реализовать `internal/provider/gemini.go` — `GeminiProvider` (stub)
   - `SupportedServices()`: `[ServiceChat, ServiceEmbedding]`
6. Написать тесты с HTTP mock-сервером:
   - `TestClaudeProvider_Execute_Success`
   - `TestClaudeProvider_Execute_Timeout`
   - `TestClaudeProvider_Execute_5xx`
   - `TestClaudeProvider_EstimateCost`
   - `TestOllamaProvider_SupportedServices`
   - `TestOllamaProvider_HealthCheck_Unavailable`
   - `TestRegistry_GetForService`
   - `TestRegistry_GetUnknownProvider_ReturnsError`

### Критерии готовности

- [x] `go test ./internal/provider/...` — 22 теста зелёные
- [x] Тесты используют HTTP mock-сервер (`httptest`), не реальные API
- [x] Все провайдеры реализуют `SupportedServices()` корректно
- [x] `Execute()` работает как pass-through: SanitizedBody → API → Response (Claude, Ollama)
- [x] `EstimateCost()` учитывает BillingType провайдера (per_token, per_image, per_request, per_second)

---

## Фаза 4 — Ingress

**Цель**: входная точка: mTLS, аутентификация, rate limiting, определение ServiceType, извлечение TextParts.

**Контекст для загрузки**: `CLAUDE.md` + `docs/INTERFACES.md` + `internal/ingress/SPEC.md` + `docs/SECURITY.md`

### Задачи

1. Реализовать `internal/ingress/tls.go` — генерация self-signed CA при первом запуске
2. Реализовать `internal/ingress/auth.go` — валидация `X-Prism-Token` через `Vault.ValidateToken` (HMAC lookup)
3. Реализовать `internal/ingress/ratelimit.go` — token bucket per projectID
4. Реализовать `internal/ingress/service.go` — определение `ServiceType` из URL path + `X-Prism-Service` header
5. Реализовать `internal/ingress/extractor.go` — извлечение `TextParts` из тела запроса (per ServiceType)
6. Реализовать `internal/ingress/handler.go` — композиция: tls → auth → ratelimit → service type → extract → ParsedRequest
7. Настроить биндинг только на `127.0.0.1` (проверяется тестом)
8. Написать тесты:
   - `TestIngress_NoToken_Returns401`
   - `TestIngress_InvalidToken_Returns401`
   - `TestIngress_RateLimit_Returns429`
   - `TestIngress_BindsOnlyLoopback`
   - `TestIngress_ValidRequest_ReturnsParsedRequest`
   - `TestIngress_ServiceType_FromURLPath`
   - `TestIngress_ServiceType_FromHeader`
   - `TestIngress_TextParts_ExtractedFromChat`
   - `TestIngress_TextParts_ExtractedFromImagePrompt`
   - `TestIngress_RawBody_Preserved`

### Критерии готовности

- [x] `go test ./internal/ingress/...` — 13 тестов зелёные
- [x] Биндинг только на 127.0.0.1 (тест TestIngress_BindsOnlyLoopback)
- [x] ServiceType корректно определяется для всех 6 URL paths + X-Prism-Service header
- [x] TextParts извлекаются из тела (chat messages, image prompt, embedding input, массивы), RawBody сохраняется

---

## Фаза 5 — Privacy Pipeline

**Цель**: обфускация PII. Tiered detection: Regex (Go) → Ollama NER → Presidio (опционально).

**Контекст для загрузки**: `CLAUDE.md` + `docs/INTERFACES.md` + `internal/privacy/SPEC.md` + `docs/SECURITY.md`

### Задачи

1. Реализовать `internal/privacy/regex_detector.go` — встроенный Go детектор (email, phone, CC, SSN, IBAN, IP)
2. Реализовать `internal/privacy/ollama_detector.go` — NER через Ollama (имена, организации)
3. Реализовать `internal/privacy/presidio_detector.go` — клиент к Presidio (Unix socket, chmod 600)
4. Реализовать `internal/privacy/composite_detector.go` — объединение результатов (union, max score)
5. Реализовать `internal/privacy/substitution.go` — создание Map Table, замена PII в TextParts + RawBody → SanitizedBody
6. Реализовать `internal/privacy/restore.go` — `RestoreFunc` как замыкание (только для текстовых ответов)
7. Реализовать `internal/privacy/pipeline.go` — композиция: detect → score → substitute
8. Реализовать `destroyMapTable` — explicit_bzero (best-effort)
9. Реализовать защиту от prompt injection: whitelist только своих placeholder'ов
10. Реализовать rate limiting / semaphore для Presidio клиента
11. Написать golden tests (загружать из `testdata/privacy/*.json`):
    - `person_ru.json`, `person_en.json`, `credit_card.json`, `phone_formats.json`
    - `email_edge_cases.json`, `no_pii.json`, `mixed_languages.json`, `prompt_injection.json`
12. Написать тесты инвариантов:
    - `TestMapTableDestroyedOnPanic`
    - `TestMapTableDestroyedOnError`
    - `TestPlaceholderIsolationBetweenRequests`
    - `TestRestoreFuncCalledOnce`
    - `TestBinaryResponse_NoRestoration`
    - `TestRegexDetector_StructuredPII`
    - `TestCompositeDetector_MergesResults`
    - `TestFailClosed_CloudProvider`
    - `TestPassThrough_LocalProvider`

### Критерии готовности

- [ ] `go test -race ./internal/privacy/...` — все тесты зелёные (unit, с mock детекторами)
- [ ] `go test -tags=integration -race ./internal/privacy/...` — зелёные (с Ollama и/или Presidio)
- [ ] Покрытие публичного интерфейса `Pipeline` — 100%
- [ ] RegexDetector работает без внешних зависимостей
- [ ] Presidio использует Unix socket (не HTTP)
- [ ] SanitizedBody корректно собирается из RawBody + подменённых TextParts
- [ ] RestoreFunc не применяется к бинарным ответам

---

## Фаза 6 — Cost Tracker + Audit Log

**Цель**: учёт расходов (множественные BillingType) и append-only audit log с HMAC chain.

**Контекст для загрузки**: `CLAUDE.md` + `docs/INTERFACES.md` + `internal/cost/SPEC.md` + `internal/audit/SPEC.md`

### Задачи

1. Реализовать `internal/cost/schema.go` — DDL для таблиц (с `service_type`, всеми usage metrics, множественными billing types)
2. Реализовать `internal/cost/buffer.go` — in-memory write buffer (ring buffer, flush каждые 1с или 100 записей)
3. Реализовать `internal/cost/tracker.go` — `Tracker` с `Record`, `Summary`, `QuotaUsage`, `Flush`
4. Реализовать `internal/cost/budget.go` — `BudgetChecker`, принимает `CostEstimate`, проверка иерархии
5. Реализовать `internal/cost/subscription.go` — учёт квот subscription-провайдеров
6. Реализовать `internal/audit/schema.go` — DDL с WORM-триггерами
7. Реализовать `internal/audit/hmac.go` — HMAC chain (sha256, ключ через HKDF от master password)
8. Реализовать `internal/audit/logger.go` — `Logger` с `Log`, `Query`, `VerifyChain`
9. Написать тесты для Cost:
   - `TestBudgetGuard_AllFourLevels`
   - `TestBudgetGuard_MostRestrictiveWins`
   - `TestBudgetGuard_SubscriptionQuotaExceeded`
   - `TestBudgetGuard_CostEstimate_PerImage`
   - `TestSummary_ByPair_ManyToMany`
   - `TestWriteBuffer_FlushOnTimer`
   - `TestWriteBuffer_FlushOnCapacity`
   - `TestFlush_GracefulShutdown`
10. Написать тесты для Audit:
    - `TestAuditLog_UpdateForbidden`
    - `TestAuditLog_DeleteForbidden`
    - `TestAuditLog_NoPIIInEntries`
    - `TestHMACChain_TamperDetected`
    - `TestHMACChain_VerifyChain_ValidRange`
    - `TestHMACChain_SeparateKey_FromEncryption`

### Критерии готовности

- [ ] `go test -race ./internal/cost/... ./internal/audit/...` — все тесты зелёные
- [ ] WORM-триггеры работают (UPDATE/DELETE вызывают ошибку)
- [ ] HMAC chain обязательна для каждой записи; `VerifyChain` обнаруживает подделку
- [ ] In-memory buffer снижает write contention
- [ ] Множественные BillingType корректно обрабатываются в бюджетах

---

## Фаза 7 — Policy Engine

**Цель**: routing (с ServiceType), budget guard, failover. Собирает все предыдущие модули.

**Контекст для загрузки**: `CLAUDE.md` + `docs/INTERFACES.md` + `internal/policy/SPEC.md`

### Задачи

1. Реализовать `internal/policy/router.go` — загрузка `routes.yaml`, match правил сверху вниз с `service_type` condition
2. Реализовать `internal/policy/validate.go` — `Router.Validate()`: проверка опечаток, unreachable rules, несовместимости provider+service_type
3. Реализовать `internal/policy/failover.go` — retry + fallback с timeout
4. Реализовать `internal/policy/engine.go` — оркестратор: privacy → budget → cache → route → vault sign → provider → restore → record
5. Написать тесты:
   - `TestRouter_FirstMatchWins`
   - `TestRouter_DefaultRule`
   - `TestRouter_ServiceTypeCondition`
   - `TestRouter_ProviderCapabilityCheck`
   - `TestRouter_Validate_UnreachableRule`
   - `TestRouter_Validate_IncompatibleProviderService`
   - `TestFailover_On5xx_SwitchesToFallback`
   - `TestFailover_BothProvidersFail_Returns503`
   - `TestEngine_FullFlow_WithMocks`

### Критерии готовности

- [ ] `go test -race ./internal/policy/...` — все тесты зелёные
- [ ] Engine использует только интерфейсы, ни одного импорта конкретных типов
- [ ] `Router.Validate()` обнаруживает ошибки в конфигурации
- [ ] Routing учитывает `ServiceType` и `provider.SupportedServices()`

---

## Фаза 8 — Semantic Cache

**Цель**: кэш похожих запросов. Per-ServiceType policy. Требует Ollama для integration тестов.

**Контекст для загрузки**: `CLAUDE.md` + `docs/INTERFACES.md` + `internal/cache/SPEC.md`

### Задачи

1. Реализовать `internal/cache/embedder.go` — HTTP-клиент к Ollama (nomic-embed-text)
2. Реализовать `internal/cache/storage.go` — SQLite для хранения, encrypted PII mappings
3. Реализовать `internal/cache/index.go` — in-memory vector index для O(log n) lookup
4. Реализовать `internal/cache/similarity.go` — cosine similarity, threshold 0.95
5. Реализовать `internal/cache/policy.go` — per-ServiceType cache policy (chat=on, image=off, etc.)
6. Реализовать `internal/cache/cache.go` — `SemanticCache` с `Get`, `Set`, `Invalidate`
7. Написать тесты:
   - `TestCache_HitAboveThreshold`
   - `TestCache_MissBelowThreshold`
   - `TestCache_Invalidate_ClearsProject`
   - `TestCache_PolicyDisabled_ForImageGen`
   - `TestCache_SanitizedResponse_EncryptedPIIMapping`
   - `TestEmbedder_HealthCheck_OllamaUnavailable`
   - `TestVectorIndex_Performance`

### Критерии готовности

- [ ] `go test -race ./internal/cache/...` — unit тесты зелёные (mock Embedder)
- [ ] `go test -tags=integration ./internal/cache/...` — зелёные (требует Ollama)
- [ ] Cache policy: chat enabled, image disabled by default
- [ ] Ответы хранятся sanitized + encrypted PII mapping
- [ ] Vector index O(log n), не brute-force O(n) scan

---

## Фаза 9 — CLI + Config + Wire-up

**Цель**: собрать всё вместе в рабочий бинарник.

**Контекст для загрузки**: `CLAUDE.md` + `docs/INTERFACES.md` + `docs/ARCHITECTURE.md`

### Задачи

1. Реализовать загрузку конфигов: `config.yaml`, `routes.yaml`, `budgets.yaml`, `privacy.yaml` (с tier detection)
2. Реализовать `cmd/prism/main.go` — инициализация всех модулей, dependency injection вручную
3. Реализовать CLI-команды:
   - `prism init --tier 1|2|3` — генерация CA, мастер-пароль, создание БД, проверка зависимостей
   - `prism start` — запуск сервера
   - `prism vault add-provider`, `prism vault register --ttl 720h`
   - `prism vault revoke-token`, `prism vault rotate-key`
   - `prism routes validate` — валидация routing rules
   - `prism audit verify-chain --from "24h ago"` — проверка HMAC chain
   - `prism install-service` (systemd/launchd)
4. Реализовать `/prism/health`, `/prism/cost/summary`, `/prism/cost/quota`, `/prism/audit/log`
5. Написать integration тест полного flow:
   - `TestE2E_ChatRequest_WithPIIObfuscation`
   - `TestE2E_ImageRequest_PassThrough`
   - `TestE2E_BudgetExceeded_Returns429`
   - `TestE2E_ProviderFailover`
   - `TestE2E_CacheHit_ForChat`
   - `TestE2E_CacheMiss_ForImage`

### Критерии готовности

- [ ] `prism init --tier 1 && prism start` — сервер стартует без внешних зависимостей
- [ ] E2E тесты зелёные (с mock провайдером)
- [ ] `prism routes validate` ловит ошибки конфигурации
- [ ] `prism audit verify-chain` обнаруживает подделку

---

## Фаза 10 — HTML Документация

**Цель**: сгенерировать HTML-документацию по готовым SPEC.md файлам.

**Контекст для загрузки**: `CLAUDE.md` + все `internal/*/SPEC.md`

### Задачи

1. Создать `docs/html/_shared/style.css` — единый стиль
2. Создать `docs/html/_shared/diagram.js` — интерактивная схема (Mermaid.js)
3. Создать `docs/html/index.html` — главная со схемой всех модулей и ServiceTypes
4. Для каждого модуля создать `docs/html/ru/<module>.html` и `docs/html/en/<module>.html`
5. Переключатель языка: `ru/vault.html` ↔ `en/vault.html`

### Критерии готовности

- [ ] Все 8 модулей задокументированы на двух языках
- [ ] Интерактивная схема на `index.html` — все узлы кликабельны
- [ ] Нет broken links между страницами

---

## Фаза 11 — Web UI (дашборд)

**Цель**: локальный дашборд для мониторинга расходов и настройки.

**Контекст для загрузки**: `CLAUDE.md` + `docs/ARCHITECTURE.md` (раздел "Локальный UI")

### Задачи

1. Выбрать стек: HTMX + Go templates (рекомендуется для zero-dependency) или React
2. Страницы: Overview (с service type breakdown), Cost, Requests, Budgets, Routing, Privacy, Vault
3. Биндинг дашборда на `127.0.0.1:8081` (отдельный порт)
4. **Аутентификация**: local token или session cookie от master password
5. CSRF protection для form submissions

### Критерии готовности

- [ ] Открывается в браузере по `http://localhost:8081`
- [ ] Требует аутентификацию (без токена → redirect на login)
- [ ] Overview показывает реальные данные из Cost Tracker (с breakdown по ServiceType)
- [ ] Budgets — можно создать/изменить бюджет без перезапуска сервера

---

## Порядок зависимостей между фазами

```
Фаза 1 (типы + интерфейсы)
    ├── Фаза 2 (Vault)          — нет зависимостей от других фаз
    ├── Фаза 3 (Providers)      — нет зависимостей от других фаз
    ├── Фаза 4 (Ingress)        — требует Vault (интерфейс)
    ├── Фаза 5 (Privacy)        — нет зависимостей от других фаз
    ├── Фаза 6 (Cost + Audit)   — нет зависимостей от других фаз
    └── Фазы 2–6 завершены →
            Фаза 7 (Policy)     — требует все предыдущие (интерфейсы)
                └── Фаза 8 (Cache) — требует Policy (интерфейс)
                    └── Фаза 9 (CLI + Wire-up)
                        ├── Фаза 10 (Документация)
                        └── Фаза 11 (UI)
```

Фазы 2–6 можно вести **параллельно** (разные сессии, разные ветки).
