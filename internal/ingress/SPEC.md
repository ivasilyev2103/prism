# Ingress — Module Spec

> Загружать при работе над `internal/ingress/`.
> Дополнительно: `docs/INTERFACES.md`.
> Security-критичный: также загрузить `docs/SECURITY.md`.

## Назначение

Входная точка Prism: mTLS-аутентификация, валидация local token, rate limiting, определение `ServiceType`, извлечение `TextParts` для PII-сканирования, сохранение оригинального тела как `RawBody` (pass-through).

Prism — универсальный AI-шлюз. Ingress определяет тип AI-сервиса из URL path запроса и извлекает только текстовые фрагменты, оставляя оригинальное тело нетронутым для прозрачного проксирования к провайдеру.

## Реализуемые интерфейсы

- `ingress.Handler`
- `ingress.RateLimiter`

## Зависимости

| Направление | Модуль | Что используется |
|-------------|--------|------------------|
| Использует | `internal/types` | ParsedRequest, TextPart, ServiceType |
| Использует | `vault.Vault` | ValidateToken |
| Используется в | `internal/policy` | Handle |

## Структура файлов

```
internal/ingress/
├── ingress.go        # интерфейсы (Handler, RateLimiter)
├── handler.go        # реализация Handler
├── auth.go           # ValidateToken через Vault (HMAC lookup + constant-time compare)
├── ratelimit.go      # token bucket per projectID
├── tls.go            # генерация self-signed CA
├── service.go        # определение ServiceType из URL path
├── extractor.go      # извлечение TextParts из body по ServiceType
├── doc.go
├── handler_test.go
└── SPEC.md
```

## Определение ServiceType из URL path

Ingress определяет `ServiceType` по URL path входящего запроса:

```
/v1/chat/completions      → ServiceChat
/v1/images/generations    → ServiceImage
/v1/embeddings            → ServiceEmbedding
/v1/audio/speech          → ServiceTTS
/v1/audio/transcriptions  → ServiceSTT
/v1/moderations           → ServiceModeration
X-Prism-Service: 3d_model → Service3DModel (custom header для нестандартных API)
```

Если path не совпадает ни с одним из известных — проверяется заголовок `X-Prism-Service`. Если и он отсутствует — ошибка 400 Bad Request.

**Тест**: `TestIngress_ServiceTypeDetection` проверяет все URL paths и fallback на header.

## Извлечение TextParts из body

После определения `ServiceType` Ingress извлекает текстовые фрагменты (`TextParts`) для PII-сканирования. Стратегия извлечения зависит от типа сервиса:

| ServiceType | Что извлекается | Role |
|-------------|----------------|------|
| `chat` | `messages[].content` (строковые) | `system` / `user` / `assistant` |
| `image` | `prompt` | `prompt` |
| `embedding` | `input` (строка или массив) | `input` |
| `tts` | `input` | `input` |
| `stt` | Ничего (audio input) | — |
| `3d_model` | `prompt` | `prompt` |
| `moderation` | `input` | `input` |

Оригинальное тело запроса сохраняется как `RawBody` (`json.RawMessage`) для pass-through к провайдеру. Prism не парсит тело полностью — извлекает только то, что нужно для privacy pipeline.

**Тест**: `TestIngress_TextPartsExtraction_Chat`, `TestIngress_TextPartsExtraction_Image` и аналогичные для каждого ServiceType.

## Аутентификация токена

Валидация local token использует **HMAC(token)** для поиска в БД:

1. Приложение передаёт токен в заголовке `X-Prism-Token`
2. Ingress вычисляет `HMAC-SHA256(token, hmac_key)`
3. Поиск по HMAC-хешу в БД (не по plaintext токену)
4. Финальное сравнение — **constant-time** (`crypto/subtle.ConstantTimeCompare`)

Plaintext токены не хранятся в БД. Даже при компрометации БД токены не раскрываются.

**Тест**: `TestIngress_TokenValidation_ConstantTime`.

## Ключевые ограничения

### Биндинг только на loopback
**Ограничение**: `net.Listen("tcp", "127.0.0.1:8080")` — жёстко, без конфигурации.
**Тест**: `TestIngress_BindsOnlyLoopback` проверяет это явно.

### mTLS при первом запуске требует CA
**Ограничение**: CA генерируется при `prism init`. Без init — старт невозможен.
**Воркэраунд**: `prism init` проверяет существование CA и не перезаписывает его.

### Rate limiting per project
**Ограничение**: token bucket per `projectID`. Конфигурируется через `RateLimit` (requests per minute).
**Поведение**: при превышении лимита — 429 Too Many Requests.

## Пример использования

```go
// В main.go
vaultImpl := vault.New(vaultConfig)
handler := ingress.NewHandler(ingress.Config{
    Vault:      vaultImpl,
    RateLimit:  100, // requests per minute per project
    TLSCert:    filepath.Join(home, ".prism", "ca.crt"),
    TLSKey:     filepath.Join(home, ".prism", "ca.key"),
})

// handler реализует http.Handler
// Определяет ServiceType, извлекает TextParts, сохраняет RawBody
http.Handle("/v1/", handler)

// Внутри handler.Handle():
// 1. mTLS → ValidateToken (HMAC lookup) → rate limit
// 2. ServiceType из URL path (/v1/chat/completions → ServiceChat)
// 3. TextParts из body (messages[].content для chat)
// 4. RawBody = оригинальное тело (json.RawMessage)
// 5. Возвращает ParsedRequest
```
