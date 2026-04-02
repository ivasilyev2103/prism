# Privacy Pipeline — Module Spec

> Загружать при работе над `internal/privacy/`.
> Дополнительно: `docs/INTERFACES.md` + `docs/SECURITY.md`.

## Назначение

Обнаруживает PII в текстовых частях запросов и заменяет на placeholder'ы перед отправкой в облако.
Реализует **обратимую** обфускацию: модель видит `[PERSON_1]`, пользователь видит "Иван Петров".

Работает **только с текстовыми данными** (`TextParts`). Бинарные данные (image, audio, 3D) не сканируются — PII в сгенерированных изображениях/аудио требует Computer Vision/NLP, что out of scope.

## Реализуемые интерфейсы

- `privacy.Pipeline`
- `privacy.Detector`

## Зависимости

| Направление | Модуль | Зачем |
|-------------|--------|-------|
| Использует | `internal/types` | типы `TextPart`, `ParsedRequest`, `SanitizedRequest` |
| Используется в | `internal/policy` | `Pipeline.Sanitize` |

## Структура файлов

```
internal/privacy/
├── privacy.go          # интерфейсы + экспортируемые типы
├── pipeline.go         # реализация pipelineImpl
├── regex_detector.go   # RegexDetector — встроенный Go-детектор (Tier 1)
├── ollama_detector.go  # OllamaDetector — NER через локальный LLM (Tier 2)
├── presidio_detector.go # PresidioDetector — Unix socket клиент (Tier 3)
├── composite_detector.go # CompositeDetector — объединяет результаты нескольких детекторов
├── substitution.go     # Map Table, замена PII на placeholder'ы
├── restore.go          # RestoreFunc — замыкание над Map Table
├── score.go            # risk scoring: max(scores) * coverage_factor
├── doc.go
├── pipeline_test.go
├── substitution_test.go
├── detector_test.go
└── SPEC.md
```

## Ключевые решения

### Pipeline.Sanitize: TextParts + RawBody

`Sanitize` принимает `[]TextPart` и `json.RawMessage` (RawBody) вместо `[]Message`. Prism использует pass-through архитектуру: не парсит полностью тело запроса, а извлекает только текстовые фрагменты для PII-сканирования.

```go
// pipeline.go
func (p *pipelineImpl) Sanitize(
    ctx context.Context,
    parts []types.TextPart,
    rawBody json.RawMessage,
    profile Profile,
    customPatterns []Pattern,
) (*SanitizeResult, error) {
    // 1. Если profile == ProfileOff → pass-through (no-op)
    // 2. Детектировать PII в каждом TextPart через CompositeDetector
    // 3. Подменить PII в parts → sanitizedParts
    // 4. Собрать SanitizedBody: заменить текстовые поля в RawBody
    // 5. Создать RestoreFunc (замыкание над Map Table)
    // 6. Вернуть SanitizeResult
}
```

Возвращает `SanitizeResult`:
- `SanitizedParts` — TextParts с подменёнными PII
- `SanitizedBody` — RawBody с подменёнными текстовыми полями (json.RawMessage)
- `RestoreFunc` — замыкание для восстановления PII в текстовых ответах
- Для бинарных ответов (image, audio, 3D) RestoreFunc **не применяется**

### PII Detector: tiered architecture

Prism поддерживает три уровня PII detection. `CompositeDetector` объединяет результаты нескольких детекторов (union, max score per entity).

| Tier | Детектор | Покрытие | Зависимости |
|------|---------|---------|-------------|
| 1 (встроенный) | **RegexDetector** (Go) | email, phone, CC, SSN, IBAN, IP (~70%) | Нет |
| 2 (рекомендуемый) | RegexDetector + **OllamaDetector** | + имена, организации, адреса (~95%) | Ollama |
| 3 (максимальный) | RegexDetector + OllamaDetector + **PresidioDetector** | Максимальное качество NER | Ollama + Python/Presidio |

```go
// composite_detector.go
// CompositeDetector вызывает детекторы по цепочке и объединяет результаты.
// При совпадении позиций — берёт entity с максимальным score.
type compositeDetector struct {
    detectors []Detector // [RegexDetector, OllamaDetector, PresidioDetector]
}

func (cd *compositeDetector) Detect(ctx context.Context, text string, profile Profile) ([]Entity, error) {
    var allEntities []Entity
    for _, d := range cd.detectors {
        entities, err := d.Detect(ctx, text, profile)
        if err != nil {
            // HealthCheck определяет, critical ли этот детектор
            // RegexDetector никогда не падает (встроенный)
            return nil, err
        }
        allEntities = append(allEntities, entities...)
    }
    return mergeEntities(allEntities), nil // union, max score per overlap
}
```

#### RegexDetector (Tier 1, встроенный)

```go
// regex_detector.go
// Встроенный Go-детектор. Нулевые зависимости. Всегда доступен.
// Покрывает: EMAIL, PHONE, CREDIT_CARD, SSN, IBAN, IP_ADDRESS.
// Паттерны компилируются один раз при инициализации.
type regexDetector struct {
    patterns map[string]*regexp.Regexp
}
```

#### OllamaDetector (Tier 2)

```go
// ollama_detector.go
// NER через локальный LLM (Ollama). Определяет: PERSON, ORGANIZATION, ADDRESS.
// Требует запущенный Ollama с поддержкой NER.
type ollamaDetector struct {
    client *http.Client
    model  string // например, "llama3" или специализированная NER-модель
}
```

#### PresidioDetector (Tier 3, опциональный)

```go
// presidio_detector.go
// Полнофункциональный Python sidecar. Максимальное качество NER.
// Коммуникация через Unix domain socket (chmod 600), НЕ HTTP.
type presidioDetector struct {
    conn      net.Conn        // Unix domain socket
    semaphore chan struct{}    // rate limiting: max concurrent requests
    mu        sync.Mutex      // защита conn
}
```

### Presidio sidecar: Unix domain socket

Коммуникация с Presidio через **Unix domain socket** с file permissions, не через HTTP.

**Обоснование**: PII отправляется на Presidio **до обфускации** — это самые чувствительные данные. Plaintext HTTP на localhost подвержен тем же атакам (sniффинг через `/proc`, ptrace), от которых mTLS на порту 8080 защищает. Unix socket с `chmod 600` гарантирует, что только процесс Prism имеет доступ.

```go
// presidio_detector.go
const presidioSocketPath = "/tmp/prism-presidio.sock" // chmod 600

func NewPresidioDetector(maxConcurrent int) (*presidioDetector, error) {
    conn, err := net.Dial("unix", presidioSocketPath)
    if err != nil { return nil, fmt.Errorf("presidio socket unavailable: %w", err) }

    return &presidioDetector{
        conn:      conn,
        semaphore: make(chan struct{}, maxConcurrent), // rate limiting
    }, nil
}

func (pd *presidioDetector) Detect(ctx context.Context, text string, profile Profile) ([]Entity, error) {
    // Acquire semaphore — ограничение concurrent нагрузки на Presidio
    select {
    case pd.semaphore <- struct{}{}:
        defer func() { <-pd.semaphore }()
    case <-ctx.Done():
        return nil, ctx.Err()
    }

    // Отправить запрос через Unix socket
    // Протокол: length-prefixed JSON (не HTTP)
    // ...
}
```

**Windows**: Unix domain sockets недоступны. Используется loopback (`127.0.0.1`) + TLS.

```go
// presidio_detector.go
func newPresidioConn() (net.Conn, error) {
    if runtime.GOOS == "windows" {
        // Windows: loopback + TLS
        tlsConfig := &tls.Config{ /* mutual TLS */ }
        return tls.Dial("tcp", "127.0.0.1:5001", tlsConfig)
    }
    // Unix: domain socket (chmod 600)
    return net.Dial("unix", presidioSocketPath)
}
```

**Rate limiting / semaphore**: Presidio — Python sidecar с ограниченным throughput (количество spaCy workers). Семафор предотвращает DoS через перегрузку sidecar. `maxConcurrent` = количество spaCy workers (по умолчанию: количество CPU ядер).

### Placeholder с request ID — защита от prompt injection

```
Формат: [TYPE_<reqID8>_N]
Пример: [PERSON_a3f9b2c1_1]

При восстановлении принимаем ТОЛЬКО placeholder'ы с нашим reqID8.
Чужой [PERSON_deadbeef_1] в ответе модели — остаётся без изменений.
```

### Map Table — только в замыкании RestoreFunc

```go
// Map Table живёт только внутри замыкания RestoreFunc.
// Не передаётся между горутинами. Не сериализуется.
// destroyMapTable вызывается в defer внутри RestoreFunc — ровно один раз.

func buildSubstitution(entities []Entity, parts []types.TextPart, rawBody json.RawMessage, reqID string) (
    sanitizedParts []types.TextPart,
    sanitizedBody json.RawMessage,
    restoreFunc func(string) string,
) {
    mt := make(map[string]string) // placeholder → original
    // ... заполнение mt, подмена в parts и rawBody ...

    return sanitizedParts, sanitizedBody, func(response string) string {
        defer destroyMapTable(mt) // зануление при любом выходе
        // whitelist: заменяем только ключи из mt
        return applyRestore(response, mt, reqID)
    }
}
```

### RestoreFunc: текстовые vs бинарные ответы

RestoreFunc применяется **только** к текстовым ответам (`Response.TextContent`).

Для бинарных ответов (`Response.BinaryData` — image, audio, 3D):
- PII detection не применялась к бинарному выводу
- Placeholder'ов в бинарных данных нет
- RestoreFunc **не вызывается**, Map Table уничтожается при timeout/defer

```go
// policy.go (вызывающий код)
if resp.ServiceType == types.ServiceChat || resp.ContentType == "text/plain" {
    // Текстовый ответ — применить restoration
    resp.TextContent = result.RestoreFunc(resp.TextContent)
    // Map Table занулён. RestoreFunc больше не вызывать.
} else {
    // Бинарный ответ (image, audio, 3D) — restoration не нужна
    // Map Table будет занулён при GC замыкания RestoreFunc
    // или через explicit defer в pipeline
}
```

### Fail-closed vs. pass-through по типу провайдера

| Провайдер | При отказе PII detector |
|-----------|----------------------|
| Облачный (Claude, OpenAI, Gemini) | **Fail-closed**: запрос отклоняется, данные не покидают машину |
| Локальный (Ollama) | **Pass-through**: данные не покидают машину, PII detection — QoS, не security |

```go
// pipeline.go
func (p *pipelineImpl) handleDetectorError(err error, providerID types.ProviderID) error {
    if isLocalProvider(providerID) {
        // Ollama — данные локальны, pass-through допустим
        log.Warn("PII detector unavailable, pass-through for local provider",
            "provider", providerID, "error", err)
        return nil
    }
    // Облачный провайдер — fail-closed, блокируем запрос
    return fmt.Errorf("PII detector unavailable, blocking cloud request: %w", err)
}
```

## Ограничения

### RestoreFunc — строго один вызов
**Ограничение**: повторный вызов `RestoreFunc` вернёт исходный (не восстановленный) текст — Map Table уже занулён.
**Это инвариант безопасности**, не баг. Задокументировать в godoc интерфейса.

### Presidio latency при холодном старте
**Ограничение**: ~500-2000ms на первый запрос (загрузка spaCy моделей).
**Воркэраунд**: warm-up `HealthCheck()` при старте Prism — до приёма первого запроса.

### Русские имена: качество зависит от модели spaCy
**Конфигурация**: `SPACY_MODELS=en_core_web_lg,ru_core_news_lg`.
Имена/организации — хорошо. Адреса — хуже.

### Бинарные ответы не сканируются
**Ограничение**: PII может содержаться в сгенерированных изображениях (текст на вывеске), аудио (имя в речи), 3D-моделях. Детекция требует Computer Vision/NLP, что out of scope.
**Митигация**: PII detection применяется к текстовому промпту — если промпт обфусцирован, вероятность PII в выводе снижается.

### Map Table и Go GC
**Ограничение**: `map[string]string` (Map Table) — ключи и значения как Go-строки. `destroyMapTable` зануляет map, но копии от GC или конкатенации — вне контроля.
**Митигация**: mlock-pinned буферы для Map Table (Phase 2), process isolation как основной барьер. См. `docs/SECURITY.md` → "Ограничения Go Memory Model".

## Пример использования

```go
// Создание tiered детектора
regexDet := privacy.NewRegexDetector()
ollamaDet := privacy.NewOllamaDetector("http://localhost:11434", "llama3")
presidioDet, err := privacy.NewPresidioDetector(4) // maxConcurrent = 4 workers

detector := privacy.NewCompositeDetector(regexDet, ollamaDet, presidioDet)
pipeline := privacy.NewPipeline(detector)

// Sanitize принимает TextParts и RawBody (pass-through архитектура)
result, err := pipeline.Sanitize(ctx, req.TextParts, req.RawBody, privacy.ProfileModerate, customPatterns)
if err != nil { return err }

// Отправить result.SanitizedBody провайдеру
resp, err := provider.Execute(ctx, &types.SanitizedRequest{
    ParsedRequest:    *req,
    SanitizedBody:    result.SanitizedBody,
    PrivacyScore:     result.PrivacyScore,
    PIIEntitiesFound: len(result.EntitiesFound),
})

// Восстановить оригинальные данные в текстовом ответе
if resp.TextContent != "" {
    cleanContent := result.RestoreFunc(resp.TextContent)
    // Map Table занулён. result.RestoreFunc больше не вызывать.
} else {
    // Бинарный ответ (image, audio, 3D) — RestoreFunc не применяется.
    // Map Table уничтожается автоматически.
}
```

## Тесты

Golden tests загружаются из `testdata/privacy/*.json`. Формат:

```json
{
  "description": "Имя и email в русском тексте",
  "profile": "moderate",
  "input_parts": [
    {"role": "user", "content": "Клиент Иван Петров, ivan@corp.ru", "index": 0}
  ],
  "expected_original_not_in_sanitized": ["Иван Петров", "ivan@corp.ru"],
  "expected_entities": [
    {"type": "PERSON", "score_min": 0.85},
    {"type": "EMAIL",  "score_min": 0.99}
  ],
  "restore_check": true
}
```

Обязательные тест-кейсы:
- `TestMapTableDestroyedOnPanic`
- `TestMapTableDestroyedOnError`
- `TestPlaceholderIsolationBetweenRequests`
- `TestRestoreFuncCalledOnce` — второй вызов не восстанавливает данные
- `TestSanitize_TextParts_NotMessages` — Sanitize принимает TextParts и RawBody
- `TestSanitize_SanitizedBody_RawBodyWithReplacedText` — SanitizedBody содержит RawBody с подменёнными текстовыми полями
- `TestCompositeDetector_MergesEntities` — union результатов, max score per overlap
- `TestCompositeDetector_RegexOnly_NoDependencies` — Tier 1 работает без внешних зависимостей
- `TestPresidioDetector_UnixSocket` — коммуникация через Unix domain socket
- `TestPresidioDetector_WindowsFallback_TLS` — на Windows: loopback + TLS
- `TestPresidioDetector_Semaphore_RateLimiting` — concurrent запросы ограничены семафором
- `TestBinaryResponse_NotScanned` — бинарные ответы (image/audio/3D) не сканируются
- `TestRestoreFunc_TextOnly` — RestoreFunc применяется только к текстовым ответам
- `TestFailClosed_CloudProvider` — отказ детектора при облачном провайдере блокирует запрос
- `TestPassThrough_LocalProvider` — отказ детектора при Ollama допускает pass-through
- Golden tests для всех файлов в `testdata/privacy/`
