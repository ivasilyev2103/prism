# Provider — Module Spec

> Загружать при работе над `internal/provider/`.
> Дополнительно: `docs/INTERFACES.md`.


## Назначение

Адаптеры к AI-провайдерам. Нормализует разные API к единому интерфейсу Provider. Нет зависимостей от других internal модулей.

Prism — универсальный AI-шлюз: не только LLM, но и image generation, TTS, STT, embeddings, 3D, moderation. Каждый провайдер объявляет `SupportedServices()` — список типов сервисов, которые он поддерживает. Router использует эту информацию для проверки совместимости при маршрутизации.

## Реализуемые интерфейсы

- `provider.Provider` (ClaudeProvider, OllamaProvider, GeminiProvider, OpenAIProvider)
- `provider.Registry`

## Зависимости

| Использует | `internal/types` | все типы |
| Используется в | `internal/policy` | через Registry |

## Интерфейс Provider

```go
type Provider interface {
    ID() types.ProviderID
    SupportedServices() []types.ServiceType
    Execute(ctx context.Context, req *types.SanitizedRequest) (*types.Response, error)
    EstimateCost(req *types.ParsedRequest) (*types.CostEstimate, error)
    HealthCheck(ctx context.Context) error
}
```

### Execute (замена Complete)

Провайдер работает как **pass-through прокси**: получает `SanitizedBody` из `SanitizedRequest` и проксирует к API провайдера с минимальными изменениями:

1. Формирует URL эндпоинта провайдера (зависит от `ServiceType` запроса)
2. Устанавливает auth header (через Vault `SignRequest`)
3. Проксирует `SanitizedBody` как тело HTTP-запроса
4. Парсит ответ провайдера в `types.Response` (заполняет `Usage`, `TextContent`/`BinaryData`, `RawBody`)

Prism не навязывает свой формат — оригинальное тело запроса сохраняется и передаётся провайдеру как есть (с подменёнными PII в текстовых полях).

### EstimateCost (замена CountTokens/PricePerMTokens)

Возвращает `types.CostEstimate` с предварительной оценкой стоимости запроса. Используется Budget Guard перед отправкой.

Оценка зависит от `BillingType` провайдера для данного `ServiceType`:
- `per_token`: приблизительный подсчёт токенов × цена за 1M
- `per_image`: количество запрошенных изображений × цена за штуку
- `per_request`: фиксированная цена за вызов
- `per_second`: оценка compute time × цена за секунду
- `subscription`: проверка квот (не стоимость в USD)

### SupportedServices

Возвращает список `types.ServiceType`, которые провайдер поддерживает. Router проверяет совместимость `req.ServiceType` с провайдером перед маршрутизацией.

## Провайдеры и поддерживаемые сервисы

| Провайдер | ServiceTypes | BillingTypes |
|-----------|-------------|-------------|
| Claude | chat | per_token, subscription |
| OpenAI | chat, image, embedding, tts, stt, moderation | per_token (chat, embedding), per_image (DALL-E), per_request (moderation), per_second (tts, stt) |
| Gemini | chat, embedding | per_token, subscription |
| Ollama | chat, embedding, image (Stable Diffusion) | бесплатно (локальный) |

Один провайдер может иметь **несколько BillingType** в зависимости от ServiceType. Например, OpenAI использует `per_token` для chat, но `per_image` для DALL-E. `EstimateCost` выбирает правильный BillingType на основе `req.ServiceType`.

## Структура файлов

```
internal/provider/
├── provider.go       # интерфейсы Provider, Registry
├── registry.go       # реализация Registry (Get, GetForService, Register, All)
├── claude.go         # ClaudeProvider (chat)
├── ollama.go         # OllamaProvider (chat, embedding, image)
├── gemini.go         # GeminiProvider (stub: chat, embedding)
├── openai.go         # OpenAIProvider (stub: chat, image, embedding, tts, stt, moderation)
├── doc.go
├── claude_test.go
├── ollama_test.go
├── gemini_test.go
├── openai_test.go
├── registry_test.go
└── SPEC.md
```

## Ключевые ограничения

### Gemini и OpenAI — stubs в Фазе 3
**Ограничение**: реализованы как заглушки с `TODO`. Полная реализация в Фазе 9+.
Stubs корректно возвращают `SupportedServices()` и `EstimateCost()` с приблизительными ценами. `Execute()` возвращает `ErrNotImplemented`.
**Тест**: `TestRegistry_GetUnknownProvider` — возвращает ошибку, не паникует.
**Тест**: `TestProvider_SupportedServices` — каждый провайдер возвращает непустой список ServiceType.

### Цены провайдеров — конфиг, не API
**Ограничение**: цены не получаются автоматически от провайдеров — они меняют их редко, но без уведомлений.
**Воркэраунд**: `prism update-prices` обновляет вручную. Планируется автообновление (Фаза 9+).

### Множественные BillingType на провайдера
**Ограничение**: конфигурация цен должна указываться per-ServiceType. Один провайдер (например, OpenAI) может иметь разные цены и BillingType для разных типов сервисов.
**Решение**: `EstimateCost` принимает `ParsedRequest` (содержит `ServiceType`) и выбирает соответствующие тарифы.

## Пример использования

```go
// Регистрация провайдеров
reg := provider.NewRegistry()
reg.Register(provider.NewClaudeProvider(claudeConfig))
reg.Register(provider.NewOllamaProvider(ollamaConfig))
reg.Register(provider.NewOpenAIProvider(openaiConfig))

// Получение провайдера для запроса
p, err := reg.Get(types.ProviderClaude)
if err != nil {
    return nil, fmt.Errorf("provider not registered: %w", err)
}

// Проверка совместимости с ServiceType
services := p.SupportedServices()
// Router проверяет: содержит ли services нужный req.ServiceType

// Предварительная оценка стоимости (для Budget Guard)
estimate, err := p.EstimateCost(parsedReq)
// estimate.BillingType == "per_token", estimate.EstimatedUSD == 0.0015

// Отправка запроса (pass-through: SanitizedBody → API провайдера)
resp, err := p.Execute(ctx, sanitizedReq)
// resp.Usage.InputTokens, resp.Usage.OutputTokens — фактическое потребление

// Поиск провайдеров по типу сервиса
imageProviders := reg.GetForService(types.ServiceImage)
// [OpenAIProvider, OllamaProvider]
```
