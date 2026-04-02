# Prism — Go Interface Contracts

> Это единственный источник истины для контрактов между модулями.
> Реализации живут в `internal/<module>/`. Зависеть можно только от интерфейсов отсюда, не от конкретных типов.
>
> Изменение интерфейса = breaking change. Требует обновления всех реализаций и тестов.

---

## Общие типы (пакет `internal/types`)

```go
package types

import (
    "context"
    "encoding/json"
    "time"
)

// ProviderID идентифицирует AI-провайдера.
type ProviderID string

const (
    ProviderClaude  ProviderID = "claude"
    ProviderGemini  ProviderID = "gemini"
    ProviderOpenAI  ProviderID = "openai"
    ProviderOllama  ProviderID = "ollama"
)

// ServiceType определяет тип AI-сервиса.
// Prism — универсальный AI-шлюз: не только LLM, но и image generation, TTS, 3D и др.
type ServiceType string

const (
    ServiceChat       ServiceType = "chat"        // LLM chat completions
    ServiceImage      ServiceType = "image"       // text-to-image (DALL-E, Stable Diffusion)
    ServiceEmbedding  ServiceType = "embedding"   // text embeddings
    ServiceTTS        ServiceType = "tts"         // text-to-speech
    Service3DModel    ServiceType = "3d_model"    // text-to-3D model
    ServiceModeration ServiceType = "moderation"  // content moderation
    ServiceSTT        ServiceType = "stt"         // speech-to-text
)

// BillingType описывает модель оплаты.
type BillingType string

const (
    BillingPerToken     BillingType = "per_token"     // LLM: цена за токен
    BillingPerImage     BillingType = "per_image"     // image gen: цена за изображение
    BillingPerRequest   BillingType = "per_request"   // фиксированная цена за запрос
    BillingPerSecond    BillingType = "per_second"    // compute-time (3D, длинный TTS)
    BillingSubscription BillingType = "subscription"  // подписка с квотами
)

// TextPart — текстовая часть запроса, извлечённая для PII-сканирования.
// Prism использует pass-through архитектуру: не парсит полностью тело запроса,
// а извлекает только текстовые фрагменты для privacy pipeline.
type TextPart struct {
    Role    string // для chat: "system" | "user" | "assistant"; для других: "prompt" | "input"
    Content string
    Index   int    // позиция в оригинальном массиве (для восстановления)
}

// ParsedRequest — запрос после Ingress, до Privacy Pipeline.
// RawBody сохраняется для pass-through к провайдеру (Prism не навязывает свой формат).
type ParsedRequest struct {
    ID          string
    ProjectID   string
    ServiceType ServiceType
    Model       string          // запрошенная модель (может быть переопределена роутером)
    TextParts   []TextPart      // текстовые части для PII-сканирования
    Tags        []string
    RawBody     json.RawMessage // оригинальное тело запроса (pass-through)
    MaxTokens   int             // для chat: ограничение ответа; 0 = не задано
}

// SanitizedRequest — запрос после Privacy Pipeline, готов к отправке провайдеру.
type SanitizedRequest struct {
    ParsedRequest
    SanitizedBody    json.RawMessage // RawBody с подменёнными PII в текстовых полях
    PrivacyScore     float64
    PIIEntitiesFound int
    // MapTable умышленно отсутствует — он не передаётся между модулями.
}

// Response — ответ от провайдера. Обобщённый тип для всех AI-сервисов.
type Response struct {
    ID          string
    ServiceType ServiceType
    TextContent string          // для text-based ответов (chat, moderation)
    BinaryData  []byte          // для image/audio/3D ответов
    ContentType string          // MIME: "text/plain", "image/png", "model/gltf+json", "audio/mp3"
    RawBody     json.RawMessage // оригинальный ответ провайдера (pass-through)
    Usage       UsageMetrics
    Model       string
    ProviderID  ProviderID
    LatencyMS   int64
}

// UsageMetrics — метрики потребления ресурсов (зависят от типа сервиса).
type UsageMetrics struct {
    InputTokens  int     // chat, embedding
    OutputTokens int     // chat
    ImagesCount  int     // image generation
    AudioSeconds float64 // TTS, STT
    ComputeUnits float64 // 3D, generic compute
}

// CostEstimate — предварительная оценка стоимости запроса.
type CostEstimate struct {
    EstimatedUSD float64
    BillingType  BillingType
    Breakdown    string // человекочитаемое описание: "~500 tokens × $0.003/1K = $0.0015"
}

// RequestRecord — запись для Cost Tracker и Audit Log.
type RequestRecord struct {
    ID               string
    Timestamp        int64
    ProjectID        string
    ProviderID       ProviderID
    ServiceType      ServiceType
    Model            string
    Usage            UsageMetrics
    CostUSD          float64
    BillingType      BillingType
    LatencyMS        int64
    PrivacyScore     float64
    PIIEntitiesFound int
    CacheHit         bool
    RouteMatched     string
    Status           string // "ok" | "error" | "budget_blocked" | "quota_exceeded" | "cache_hit"
}

// BudgetExceededError возвращается Budget Guard при превышении лимита.
type BudgetExceededError struct {
    Level      string  // "global" | "project" | "provider" | "pair"
    ProjectID  string
    ProviderID ProviderID
    LimitUSD   float64
    CurrentUSD float64
    Action     string  // "block" | "downgrade_model" | "alert"
}

func (e *BudgetExceededError) Error() string { ... }

// QuotaExceededError возвращается при исчерпании квоты подписки.
type QuotaExceededError struct {
    ProviderID ProviderID
    QuotaType  string // "requests" | "input_tokens" | "output_tokens" | "images" | "compute_seconds"
    Used       int64
    Limit      int64
}

func (e *QuotaExceededError) Error() string { ... }
```

---

## Provider (пакет `internal/provider`)

```go
package provider

import (
    "context"
    "prism/internal/types"
)

// Provider — адаптер к конкретному AI-провайдеру.
// Все реализации взаимозаменяемы (Liskov Substitution).
// Provider работает как прокси: получает SanitizedBody и проксирует к API провайдера.
type Provider interface {
    // ID возвращает идентификатор провайдера.
    ID() types.ProviderID

    // SupportedServices возвращает список поддерживаемых типов сервисов.
    // Используется Router для проверки совместимости.
    SupportedServices() []types.ServiceType

    // Execute отправляет запрос провайдеру и возвращает ответ.
    // Использует SanitizedBody (pass-through) для формирования запроса к API.
    // Контекст должен поддерживать отмену (timeout).
    Execute(ctx context.Context, req *types.SanitizedRequest) (*types.Response, error)

    // EstimateCost возвращает предварительную оценку стоимости запроса.
    // Используется Budget Guard перед отправкой.
    EstimateCost(req *types.ParsedRequest) (*types.CostEstimate, error)

    // HealthCheck проверяет доступность провайдера.
    HealthCheck(ctx context.Context) error
}

// Registry хранит и предоставляет доступ к зарегистрированным провайдерам.
type Registry interface {
    // Get возвращает провайдер по ID. Ошибка если не зарегистрирован.
    Get(id types.ProviderID) (Provider, error)

    // GetForService возвращает провайдеры, поддерживающие указанный тип сервиса.
    GetForService(serviceType types.ServiceType) []Provider

    // Register добавляет провайдер. Паникует при дублировании ID.
    Register(p Provider)

    // All возвращает все зарегистрированные провайдеры.
    All() []Provider
}
```

---

## Vault (пакет `internal/vault`)

```go
package vault

import (
    "context"
    "net/http"
    "prism/internal/types"
    "time"
)

// Vault управляет секретами и подписывает исходящие запросы.
// ВАЖНО: plaintext ключи не покидают Vault ни через один метод.
//
// Ограничение Go memory model: explicit_bzero — best-effort.
// GC может скопировать данные до зануления. Реальная граница безопасности —
// process isolation (mTLS + loopback binding), а не memory zeroing.
// См. docs/SECURITY.md → "Ограничения Go Memory Model".
type Vault interface {
    // SignRequest добавляет Authorization header к исходящему HTTP-запросу.
    // Ключ не возвращается вызывающему коду — только используется внутри.
    // projectID и providerID используются для проверки прав доступа.
    //
    // Реализация: custom http.RoundTripper инжектирует header на уровне transport
    // и зануляет после записи в wire. См. internal/vault/SPEC.md.
    SignRequest(ctx context.Context, projectID string, providerID types.ProviderID, req *http.Request) error

    // RegisterProject регистрирует приложение и возвращает local token.
    // allowedProviders — список провайдеров, доступных этому проекту.
    // tokenTTL — время жизни токена; 0 = бессрочный.
    RegisterProject(projectID string, allowedProviders []types.ProviderID, tokenTTL time.Duration) (token string, err error)

    // ValidateToken проверяет local token и возвращает projectID.
    // Использует HMAC(token) для lookup в БД + constant-time compare.
    ValidateToken(token string) (projectID string, err error)

    // RevokeToken инвалидирует token немедленно.
    RevokeToken(token string) error

    // AddProvider сохраняет API-ключ провайдера (зашифрованно).
    AddProvider(providerID types.ProviderID, apiKey string, allowedProjects []string) error

    // RotateProviderKey заменяет API-ключ провайдера.
    // Старый ключ немедленно удаляется, новый сохраняется.
    RotateProviderKey(providerID types.ProviderID, newAPIKey string) error

    // RemoveProvider удаляет API-ключ провайдера.
    RemoveProvider(providerID types.ProviderID) error

    // Close корректно завершает Vault, зануляя ключи шифрования из RAM.
    Close() error
}
```

---

## Privacy (пакет `internal/privacy`)

```go
package privacy

import (
    "context"
    "prism/internal/types"
)

// Profile определяет уровень обфускации.
type Profile string

const (
    ProfileStrict   Profile = "strict"
    ProfileModerate Profile = "moderate"
    ProfileOff      Profile = "off"
)

// Entity — обнаруженная PII-сущность.
type Entity struct {
    Type  string  // "PERSON" | "EMAIL" | "PHONE" | "CREDIT_CARD" | ...
    Value string
    Score float64 // уверенность детектора: 0.0–1.0
    Start int     // позиция в тексте
    End   int
}

// SanitizeResult — результат обфускации одного запроса.
type SanitizeResult struct {
    SanitizedParts []types.TextPart
    SanitizedBody  json.RawMessage // RawBody с подменёнными текстовыми полями
    PrivacyScore   float64
    EntitiesFound  []Entity
    // RestoreFunc восстанавливает оригинальные данные в текстовом ответе.
    // ВАЖНО: RestoreFunc хранит Map Table в замыкании.
    // Вызвать ровно один раз. После вызова Map Table занулен.
    // Для бинарных ответов (image, audio, 3D) RestoreFunc не применяется.
    RestoreFunc func(response string) string
}

// Pipeline выполняет обфускацию и восстановление PII.
// Работает только с текстовыми частями запроса (TextParts).
// Бинарные данные (images, audio, 3D) не сканируются.
type Pipeline interface {
    // Sanitize обнаруживает PII в текстовых частях и заменяет на placeholder'ы.
    // profile и customPatterns берутся из конфигурации проекта.
    // Возвращает SanitizeResult с RestoreFunc для обратного преобразования.
    // Для ServiceType без текстового ввода (STT с audio) возвращает pass-through.
    Sanitize(ctx context.Context, parts []types.TextPart, rawBody json.RawMessage, profile Profile, customPatterns []Pattern) (*SanitizeResult, error)
}

// Detector обнаруживает PII-сущности в тексте.
// Разделён от Pipeline для тестируемости и поддержки нескольких реализаций:
// - PresidioDetector: полнофункциональный (Python sidecar)
// - RegexDetector: встроенный Go-детектор (email, phone, CC, SSN, IBAN, IP)
// - OllamaDetector: NER через локальный LLM
// - CompositeDetector: объединяет результаты нескольких детекторов
type Detector interface {
    // Detect возвращает список найденных сущностей.
    Detect(ctx context.Context, text string, profile Profile) ([]Entity, error)

    // HealthCheck проверяет доступность детектора.
    HealthCheck(ctx context.Context) error
}

// Pattern — пользовательский паттерн для обнаружения чувствительных данных.
type Pattern struct {
    Name    string // например, "INTERNAL_USER_ID"
    Regex   string // например, "USR-[0-9]{8}"
    Score   float64
}
```

---

## Policy (пакет `internal/policy`)

```go
package policy

import (
    "context"
    "prism/internal/types"
)

// RoutingDecision — результат работы роутера.
type RoutingDecision struct {
    ProviderID   types.ProviderID
    ModelOverride string          // "" = использовать из запроса
    RuleName     string           // имя сработавшего правила
    FallbackID   types.ProviderID
}

// Router определяет, какой провайдер обработает запрос.
type Router interface {
    // Route возвращает решение о маршрутизации.
    // Правила проверяются сверху вниз, применяется первое совпавшее.
    // Проверяет совместимость provider.SupportedServices с req.ServiceType.
    Route(ctx context.Context, req *types.SanitizedRequest) (*RoutingDecision, error)

    // Validate проверяет корректность routing rules при загрузке конфигурации.
    // Возвращает список ошибок: опечатки, unreachable rules, несовместимые provider+service.
    Validate() []error
}

// BudgetChecker проверяет бюджеты перед отправкой запроса.
type BudgetChecker interface {
    // Check проверяет все применимые бюджеты (global, project, provider, pair).
    // Возвращает BudgetExceededError или QuotaExceededError при превышении.
    Check(ctx context.Context, projectID string, providerID types.ProviderID, estimate *types.CostEstimate) error
}

// Failover управляет переключением при недоступности провайдера.
type Failover interface {
    // Execute пытается выполнить fn с primary провайдером.
    // При ошибке (timeout, 5xx) — один retry, затем fallback провайдер.
    Execute(ctx context.Context, primary, fallback types.ProviderID, fn func(types.ProviderID) error) error
}
```

---

## Cost (пакет `internal/cost`)

```go
package cost

import (
    "context"
    "time"
    "prism/internal/types"
)

// Tracker записывает расходы и потребление квот.
// Использует in-memory write buffer с периодическим flush в SQLite
// для снижения write contention при concurrent нагрузке.
type Tracker interface {
    // Record сохраняет метрики завершённого запроса (async-safe).
    // Записывается в in-memory buffer, flush в SQLite пакетами.
    Record(ctx context.Context, r *types.RequestRecord) error

    // Summary возвращает агрегированные данные за период.
    // projectID и providerID опциональны (пустая строка = все).
    Summary(ctx context.Context, projectID string, providerID types.ProviderID, from, to time.Time) (*Summary, error)

    // QuotaUsage возвращает текущее потребление квот подписки.
    QuotaUsage(ctx context.Context, providerID types.ProviderID) (*QuotaUsage, error)

    // Flush принудительно записывает буфер в SQLite (вызывается при graceful shutdown).
    Flush(ctx context.Context) error
}

// Summary — агрегированная статистика.
type Summary struct {
    TotalUSD        float64
    RequestsCount   int64
    CacheSavingsUSD float64
    ByProvider      map[types.ProviderID]*ProviderSummary
    ByProject       map[string]*ProjectSummary
    ByPair          map[string]*PairSummary // ключ: "projectID×providerID"
}

type ProviderSummary struct {
    USD          float64
    Requests     int64
    Usage        types.UsageMetrics // агрегированные метрики
}

type ProjectSummary struct {
    USD      float64
    Requests int64
}

type PairSummary struct {
    USD      float64
    Requests int64
}

// QuotaUsage — потребление квот subscription-провайдера.
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

---

## Audit (пакет `internal/audit`)

```go
package audit

import (
    "context"
    "time"
    "prism/internal/types"
)

// Logger записывает метаданные запросов в append-only лог.
// ВАЖНО: тела запросов и ответов не логируются никогда.
// Каждая запись содержит HMAC-hash цепочки для tamper detection.
type Logger interface {
    // Log записывает запись. Операция append-only, изменение записей невозможно.
    // Автоматически вычисляет HMAC: hash(content || prev_hmac).
    Log(ctx context.Context, r *types.RequestRecord) error

    // Query возвращает записи с фильтрацией (без тел запросов).
    Query(ctx context.Context, filter *Filter) ([]*types.RequestRecord, error)

    // VerifyChain проверяет целостность HMAC-цепочки за указанный период.
    // Возвращает nil если цепочка не нарушена.
    VerifyChain(ctx context.Context, from, to time.Time) error
}

// Filter параметры запроса к audit log.
type Filter struct {
    ProjectID   string
    ProviderID  types.ProviderID
    ServiceType types.ServiceType
    From        time.Time
    To          time.Time
    Status      string
    Limit       int
    Offset      int
}
```

---

## Cache (пакет `internal/cache`)

```go
package cache

import (
    "context"
    "prism/internal/types"
)

// CachePolicy определяет, кэшируется ли данный тип сервиса.
// Для chat/embedding — да (детерминистично). Для image gen — нет по умолчанию (стохастично).
type CachePolicy struct {
    ServiceType types.ServiceType
    Enabled     bool
    TTL         int // секунды; 0 = default
}

// SemanticCache кэширует ответы на семантически похожие запросы.
// Хранит sanitized ответы + зашифрованные PII mappings для restoration.
// PII в кэше зашифрованы per-entry (AES-256-GCM), не в plaintext.
type SemanticCache interface {
    // Get ищет семантически похожий запрос в кэше.
    // Возвращает (nil, nil) при промахе или если кэширование отключено для данного ServiceType.
    Get(ctx context.Context, req *types.SanitizedRequest) (*types.Response, error)

    // Set сохраняет запрос и ответ в кэш.
    // Для ServiceType с отключенным кэшированием — no-op.
    Set(ctx context.Context, req *types.SanitizedRequest, resp *types.Response) error

    // Invalidate удаляет записи по projectID (например, при смене настроек privacy).
    Invalidate(ctx context.Context, projectID string) error
}

// Embedder генерирует векторные представления текста.
type Embedder interface {
    // Embed возвращает embedding для текста.
    // Реализация: nomic-embed-text через Ollama (локально).
    Embed(ctx context.Context, text string) ([]float32, error)

    // HealthCheck проверяет доступность Ollama.
    HealthCheck(ctx context.Context) error
}
```

---

## Ingress (пакет `internal/ingress`)

```go
package ingress

import (
    "context"
    "net/http"
    "prism/internal/types"
)

// Handler обрабатывает входящие HTTP-запросы.
// Извлекает metadata и текстовые части для PII-сканирования.
// Оригинальное тело запроса сохраняется в RawBody для pass-through.
type Handler interface {
    // Handle валидирует запрос, аутентифицирует токен, применяет rate limit.
    // Определяет ServiceType из URL path и/или тела запроса.
    // Извлекает TextParts для Privacy Pipeline.
    // Возвращает ParsedRequest для дальнейшей обработки.
    Handle(ctx context.Context, r *http.Request) (*types.ParsedRequest, error)
}

// RateLimiter ограничивает частоту запросов per project.
type RateLimiter interface {
    // Allow возвращает true если запрос разрешён в рамках лимита.
    Allow(projectID string) bool
}
```

---

## Зависимости между модулями

```
                    ┌─────────┐
                    │  types  │  ← общие типы, нет зависимостей
                    └────┬────┘
                         │ импортируется всеми
        ┌────────────────┼──────────────────┐
        ▼                ▼                  ▼
   ┌─────────┐     ┌──────────┐      ┌──────────┐
   │ ingress │     │ provider │      │  privacy │
   └────┬────┘     └────┬─────┘      └────┬─────┘
        │               │                 │
        └───────────────┼─────────────────┘
                        ▼
                  ┌──────────┐
                  │  policy  │ ← зависит от: vault, provider, cost, privacy (через интерфейсы)
                  └────┬─────┘
                       │
              ┌────────┼────────┐
              ▼        ▼        ▼
           ┌──────┐ ┌─────┐ ┌───────┐
           │ cost │ │audit│ │ cache │
           └──────┘ └─────┘ └───────┘

   ┌───────┐
   │ vault │ ← нет зависимостей от других internal модулей
   └───────┘
```

**Правило**: стрелка означает "зависит от интерфейса". Конкретные типы не импортируются.
`policy` зависит от `Vault`, `Provider`, `BudgetChecker`, `Pipeline` — все через интерфейсы.
