# Policy — Module Spec

> Загружать при работе над `internal/policy/`.
> Дополнительно: `docs/INTERFACES.md`.


## Назначение

Оркестратор обработки запроса: routing (с учётом `ServiceType` и совместимости провайдеров), budget guard (на основе `CostEstimate`), failover. Собирает все остальные модули через интерфейсы — единственный модуль с зависимостями от всех.

## Реализуемые интерфейсы

- `policy.Router`
- `policy.BudgetChecker`
- `policy.Failover`

## Зависимости

| Направление | Модуль | Что используется |
|-------------|--------|------------------|
| Использует | `internal/types` | все типы (ParsedRequest, SanitizedRequest, Response, CostEstimate, ServiceType и др.) |
| Использует | `vault.Vault` | SignRequest |
| Использует | `provider.Registry` | Get, GetForService, All |
| Использует | `provider.Provider` | Execute, EstimateCost, SupportedServices |
| Использует | `privacy.Pipeline` | Sanitize |
| Использует | `cost.Tracker` | Record |
| Использует | `audit.Logger` | Log |
| Использует | `cache.SemanticCache` | Get, Set |

Engine принимает все зависимости через интерфейсы в конструкторе. Никаких глобальных переменных.

## Структура файлов

```
internal/policy/
├── policy.go         # интерфейсы (Router, BudgetChecker, Failover)
├── engine.go         # оркестратор полного flow
├── router.go         # загрузка routes.yaml, match правил, service_type routing
├── validate.go       # Router.Validate() — проверка конфигурации при загрузке
├── budget.go         # BudgetChecker с CostEstimate
├── failover.go       # retry + fallback
├── doc.go
├── engine_test.go
├── router_test.go
├── validate_test.go
└── SPEC.md
```

## Router: service_type как условие маршрутизации

Правила проверяются сверху вниз, применяется первое совпавшее. `service_type` — полноценное условие маршрутизации наряду с `privacy_score`, `tags`, `project_id` и `time_of_day`.

Router проверяет совместимость: выбранный провайдер должен поддерживать `ServiceType` запроса (`provider.SupportedServices()`). Нельзя маршрутизировать image generation на chat-only провайдера.

```yaml
routes:
  - name: "sensitive_data"
    if:
      privacy_score: ">0.7"
    then:
      provider: ollama
      fallback: claude-haiku

  - name: "image_generation"
    if:
      service_type: image
    then:
      provider: openai          # DALL-E
      fallback: ollama           # Stable Diffusion

  - name: "embeddings_local"
    if:
      service_type: embedding
    then:
      provider: ollama           # nomic-embed-text
      fallback: openai

  - name: "cheap_tasks"
    if:
      service_type: chat
      tags: ["classify", "autocomplete"]
    then:
      provider: gemini-flash

  - name: "code_tasks"
    if:
      tags: ["code", "sql", "debug"]
    then:
      provider: claude-sonnet

  - name: "default"
    then:
      provider: claude-haiku
```

**Условия** (`if`):
- `service_type` — тип AI-сервиса (chat, image, embedding, tts, 3d_model, stt, moderation)
- `privacy_score` — из Privacy Pipeline
- `tags` — теги из заголовка `X-Prism-Tags` или auto-detected
- `project_id` — explicit match по проекту
- `time_of_day` — например, дешёвые модели ночью

**Действия** (`then`):
- `provider` — целевой провайдер (Router проверяет `provider.SupportedServices()`)
- `model` — override модели (если нужна конкретная)
- `fallback` — провайдер при недоступности основного
- `max_tokens` — ограничение ответа (для chat)

## Router.Validate() — проверка конфигурации при загрузке

`Router.Validate()` вызывается при загрузке `routes.yaml` и возвращает список ошибок. Это fail-fast: проблемы конфигурации обнаруживаются при старте, а не при первом запросе.

Проверки:

1. **Опечатки в именах провайдеров** — все `provider` и `fallback` должны быть зарегистрированы в `Registry`
2. **Несовместимость provider + service_type** — если правило фильтрует по `service_type: image`, а указанный провайдер не поддерживает `ServiceImage` (не входит в `provider.SupportedServices()`), это ошибка
3. **Unreachable rules** — правила после catch-all (правило без `if`) никогда не сработают
4. **Неизвестные service_type** — опечатки в значениях `service_type`
5. **Пустой конфиг** — отсутствие правил или отсутствие default route

```go
errs := router.Validate()
if len(errs) > 0 {
    for _, err := range errs {
        log.Printf("routing config error: %v", err)
    }
    os.Exit(1)
}
```

**Тест**: `TestRouter_Validate_DetectsTypos`, `TestRouter_Validate_IncompatibleProviderService`, `TestRouter_Validate_UnreachableRules`.

## BudgetChecker с CostEstimate

BudgetChecker использует `CostEstimate` (полученный от `provider.EstimateCost(req)`) вместо фиксированной суммы в USD. Это позволяет корректно оценивать стоимость для разных моделей биллинга (per-token, per-image, per-request, per-second).

Порядок проверки — от конкретного к общему, срабатывает самый строгий:

```
Запрос (project=X, provider=Y)
    │
    │  estimate := provider.EstimateCost(req)   // CostEstimate с BillingType и Breakdown
    │
    ├─ проверить бюджет пары  X × Y
    ├─ проверить бюджет проекта X (все провайдеры)
    ├─ проверить бюджет провайдера Y (все проекты)
    └─ проверить глобальный бюджет
    │
    ├─ все в рамках → пропустить
    ├─ превышение, on_exceed=alert → пропустить + уведомление
    ├─ превышение, on_exceed=downgrade → переключить на дешёвую модель
    └─ превышение, on_exceed=block → 429 Too Many Requests

Для subscription-провайдеров дополнительно:
    └─ проверить квоты периода → если исчерпаны → QuotaExceededError
```

**Тест**: `TestBudgetChecker_UsesEstimateCost`, `TestBudgetChecker_PerImageBilling`.

## Engine — оркестратор с инъекцией зависимостей

Engine — единственный модуль, зависящий от всех остальных. Все зависимости инжектируются через конструктор как интерфейсы:

```go
engine := policy.NewEngine(policy.Deps{
    Vault:        vaultImpl,        // vault.Vault
    Providers:    providerRegistry, // provider.Registry
    Privacy:      privacyPipeline,  // privacy.Pipeline
    BudgetCheck:  budgetChecker,    // policy.BudgetChecker
    AuditLog:     auditLogger,      // audit.Logger
    Cache:        semanticCache,    // cache.SemanticCache
    CostTracker:  costTracker,      // cost.Tracker
    RouterConfig: routesYAML,       // []byte (routes.yaml)
})
```

**Тест**: `TestEngine_FullFlow_WithMocks` использует mock-реализации всех интерфейсов.

## Ключевые ограничения

### Engine — единственный модуль с зависимостями от всех остальных
**Ограничение**: Engine принимает все зависимости через интерфейсы в конструкторе. Никаких глобальных переменных.

### Router проверяет SupportedServices
**Ограничение**: при маршрутизации Router вызывает `provider.SupportedServices()` и отклоняет несовместимые пары. Ошибка маршрутизации — не fallback, а явная ошибка конфигурации (ловится в `Validate()`).

### Failover: не более одного retry на провайдера
**Ограничение**: primary — fail — один retry — fallback. Не exponential backoff на этой фазе.
**Планируется**: configurable retry policy (Фаза 9+).

## Пример использования

```go
// Конструктор принимает все зависимости через интерфейсы
engine := policy.NewEngine(policy.Deps{
    Vault:        vaultImpl,
    Providers:    providerRegistry,
    Privacy:      privacyPipeline,
    BudgetCheck:  budgetChecker,
    AuditLog:     auditLogger,
    Cache:        semanticCache,
    CostTracker:  costTracker,
    RouterConfig: routesYAML,
})

// Валидация конфигурации при старте
if errs := engine.Router().Validate(); len(errs) > 0 {
    log.Fatalf("invalid routing config: %v", errs)
}

// Обработка запроса (полный flow):
// 1. Privacy Pipeline → sanitize TextParts
// 2. Budget Guard → EstimateCost → проверить бюджеты
// 3. Router → выбрать провайдера (с учётом ServiceType + SupportedServices)
// 4. Cache → проверить semantic cache (если enabled для данного ServiceType)
// 5. Provider → Execute (если cache miss)
// 6. Restoration → вернуть PII (для текстовых ответов)
// 7. Cost Tracker → записать метрики
// 8. Audit Log → записать метаданные
response, err := engine.Process(ctx, parsedRequest)
```
