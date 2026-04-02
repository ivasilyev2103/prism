# Архитектура Prism (LocalAI Gateway)

## Концепция: три зоны доверия

Вся система строится на принципе **zero-trust between zones** — данные меняют форму на каждой границе и никогда не хранятся в памяти дольше, чем нужно.

```
╔══════════════════╗     ╔════════════════════════════════════╗     ╔══════════════════╗
║   ЗОНА 1         ║     ║   ЗОНА 2: Prism CORE               ║     ║   ЗОНА 3         ║
║   Локальные      ║────▶║   localhost:8080 · mTLS            ║────▶║   Cloud          ║
║   приложения     ║     ║                                    ║     ║   AI Providers   ║
║                  ║     ║   [Ingress]                        ║     ║                  ║
║  Game Engine     ║     ║       ↓                            ║     ║  Claude API      ║
║  PG Assistant    ║     ║   [Privacy Pipeline]  ←── ГРАНИЦА  ║     ║  Gemini API      ║
║  AI Influencer   ║     ║       ↓                            ║     ║  OpenAI API      ║
║  Image Generator ║     ║   [Policy Engine]                  ║     ║  DALL-E API      ║
║  Any AI client   ║     ║       ↓              ↓             ║     ║  Ollama (local)  ║
║                  ║     ║   [Audit Log]  [Cost Tracker]      ║     ║                  ║
║                  ║     ║                                    ║     ║                  ║
║                  ║     ║   [Secrets Vault] ← изолирован     ║     ║                  ║
╚══════════════════╝     ╚════════════════════════════════════╝     ╚══════════════════╝
```

**Ключевой инвариант**: в Зону 3 попадают только sanitized данные (с обфусцированными PII в текстовых полях). Vault API-ключи не покидают Зону 2 в plaintext никогда.

### Не только LLM

Prism — универсальный AI-шлюз. Поддерживаемые типы сервисов:

| ServiceType | Примеры | PII detection | Кеширование |
|-------------|---------|--------------|-------------|
| `chat` | Claude, GPT, Ollama | Текстовые части | Да (semantic) |
| `image` | DALL-E, Stable Diffusion | Только промпт | Конфигурируемо |
| `embedding` | nomic-embed-text, text-embedding-3 | Текстовый ввод | Да |
| `tts` | OpenAI TTS, ElevenLabs | Текстовый ввод | Конфигурируемо |
| `3d_model` | Meshy, Shap-E | Только промпт | Конфигурируемо |
| `stt` | Whisper | Нет (audio input) | Нет |
| `moderation` | OpenAI moderation | Текстовый ввод | Нет |

---

## Pass-through архитектура

Prism — **прозрачный прокси**. Он не парсит полностью тело запроса в свои типы. Вместо этого:

1. **Ingress** извлекает metadata (project, model, service_type, tags) + сохраняет оригинальное тело как `json.RawMessage`
2. **Privacy Pipeline** извлекает текстовые части (`TextParts`) для PII-сканирования, подменяет их в `RawBody`, собирает `SanitizedBody`
3. **Provider** проксирует `SanitizedBody` к API провайдера с минимальными изменениями (auth header, endpoint rewrite)

Это позволяет поддерживать **любой AI API** (chat, image, audio, 3D) без изменения кода Prism для каждого нового формата.

```
Приложение → POST /v1/chat/completions { "messages": [...], "model": "claude-3-5-haiku" }
                                          ↓
Ingress: извлекает TextParts (содержимое messages) + metadata
         сохраняет оригинальный body как RawBody
                                          ↓
Privacy: сканирует TextParts на PII → подменяет в RawBody → SanitizedBody
                                          ↓
Provider: отправляет SanitizedBody на API провайдера (+ auth header от Vault)
```

---

## Модули Prism Core

### 1. Ingress

Первая линия защиты. Отвечает за:

- **mTLS** — взаимная TLS-аутентификация между приложением и Prism (self-signed CA генерируется при первом запуске)
- **Local Token Auth** — каждое зарегистрированное приложение получает токен. Запросы без токена → 401
- **Rate Limiting** — per-project лимиты запросов в секунду (защита от runaway агента)
- **Service Type Detection** — определяет `ServiceType` из URL path и/или тела запроса
- **TextParts Extraction** — извлекает текстовые части для PII-сканирования, сохраняет `RawBody`

Определение `ServiceType`:
```
/v1/chat/completions    → ServiceChat
/v1/images/generations  → ServiceImage
/v1/embeddings          → ServiceEmbedding
/v1/audio/speech        → ServiceTTS
/v1/audio/transcriptions → ServiceSTT
/v1/moderations         → ServiceModeration
X-Prism-Service: 3d_model → Service3DModel (custom header для не-стандартных API)
```

---

### 2. Privacy Pipeline

Обеспечивает обратимую обфускацию PII перед отправкой в облако.
**Работает только с текстовыми частями запроса.** Бинарные данные (images, audio, 3D) не сканируются.

#### Поток данных

```
ParsedRequest (TextParts + RawBody)
    │
    ▼
[PII Detector]  ←── Tiered: Regex (Go) → Ollama NER → Presidio (опционально)
    │               Определяет: PERSON, EMAIL, PHONE, CREDIT_CARD,
    │               IP_ADDRESS, IBAN, SSN, FINANCIAL, + custom patterns
    │               Каждой находке присваивается score 0.0–1.0
    ▼
[Risk Scorer]
    │               risk_score = max(entity_scores) * coverage_factor
    │               coverage_factor = found_entities / total_words
    ▼
[Policy Check]  ←── Сравнение с профилем проекта
    │
    ├─ risk > 0.8 + правило → route to Ollama (данные не покидают машину)
    │
    └─ proceed →
         │
         ▼
    [Substitution Engine]
         │    Подменяет PII в TextParts
         │    Собирает SanitizedBody (RawBody с подменёнными текстовыми полями)
         │    Map Table: { PERSON_a3f9_1: "Иван Петров", ... } — ТОЛЬКО В RAM
         ▼
    SanitizedRequest → отправляется провайдеру
         │
         ▼ (текстовый ответ от провайдера)
    [Restoration Engine]
         │    [PERSON_a3f9_1] → "Иван Петров"
         │    Map Table уничтожается (зануление памяти)
         ▼
    Ответ → приложению
```

#### PII Detector: tiered architecture

Prism поддерживает три уровня PII detection:

| Tier | Детектор | Покрытие | Зависимости |
|------|---------|---------|-------------|
| 1 (встроенный) | **RegexDetector** (Go) | email, phone, CC, SSN, IBAN, IP (~70%) | Нет |
| 2 (рекомендуемый) | RegexDetector + **OllamaDetector** | + имена, организации, адреса (~95%) | Ollama |
| 3 (максимальный) | RegexDetector + OllamaDetector + **PresidioDetector** | Максимальное качество NER | Ollama + Python/Presidio |

`CompositeDetector` объединяет результаты нескольких детекторов (union, max score per entity).

#### Fail-closed vs. pass-through

| Провайдер | При отказе PII detector |
|-----------|----------------------|
| Облачный (Claude, OpenAI, Gemini) | **Fail-closed**: запрос отклоняется |
| Локальный (Ollama) | **Pass-through**: данные не покидают машину |

#### Map Table: критические требования безопасности

- Существует **только в RAM** горутины/потока текущего запроса
- **Никогда не пишется на диск** ни в каком виде (не в логи, не в кэш, не в БД)
- После получения ответа — зануление через `explicit_bzero` (best-effort в Go)
- Если запрос прерван — Map Table уничтожается в defer

#### Профили обфускации

```yaml
privacy_profiles:
  strict:
    entities: [PERSON, EMAIL, PHONE, IP_ADDRESS, CREDIT_CARD, IBAN, SSN, MEDICAL, FINANCIAL]
    min_score: 0.5

  moderate:
    entities: [PERSON, EMAIL, CREDIT_CARD, SSN]
    min_score: 0.85

  off:
    enabled: false
```

#### Custom patterns (per project)

```yaml
custom_patterns:
  game-engine:
    - name: INTERNAL_USER_ID
      pattern: "USR-[0-9]{8}"
  ai-influencer:
    - name: CLIENT_CONTRACT
      pattern: "CTR-[A-Z]{3}-[0-9]+"
```

---

### 3. Policy Engine

Принимает `SanitizedRequest` и решает: куда отправить, с каким бюджетом, что делать при ошибке.

#### Routing

Правила проверяются сверху вниз, применяется первое совпавшее:

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

  - name: "cheap_tasks"
    if:
      service_type: chat
      tags: ["classify", "autocomplete", "embed"]
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

**Валидация** при загрузке: `Router.Validate()` проверяет опечатки, несовместимости provider+service_type, unreachable rules.

#### Budget Guard

Проверяется до отправки запроса. Использует `provider.EstimateCost(req)` для оценки стоимости (зависит от BillingType: per-token, per-image, per-request, per-second).

Проверяет **все применимые бюджеты** в порядке от конкретного к общему, срабатывает самый строгий:

```
Запрос (project=X, provider=Y)
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
    └─ проверить квоты периода → если исчерпаны → quota_exceeded
```

#### Failover

```
Основной провайдер → timeout/5xx → retry (1 раз) → fallback провайдер → ответ
                                                   └─ если fallback тоже недоступен → 503
```

---

### 4. Secrets Vault

Изолированный модуль. Единственный, кто знает plaintext API-ключи.

#### Архитектура хранилища

```
Master Password (пользователь вводит при запуске Prism)
    │
    ▼ Argon2id(password, salt, memory=64MB, iterations=3, parallelism=4)
    │
    ▼
Encryption Key (только в RAM, mlock-pinned, никогда на диск)
    │
    ├─── расшифровывает secrets.db (SQLite, per-record AES-256-GCM)
    │
    └─── secrets.db содержит:
         { provider: "claude", api_key: "sk-ant-...", project_ids: ["*"] }
         { provider: "openai", api_key: "sk-...", project_ids: ["image-gen", "ai-influencer"] }
```

**БД**: `modernc.org/sqlite` (pure Go, без CGO). Шифрование per-record через AES-256-GCM.

#### Sign-not-Expose pattern

```
Policy Engine: "нужен запрос к claude для project=game-engine"
    │
    ▼
Vault: проверяет, что game-engine имеет доступ к claude
    │
    ▼
Vault: через custom RoundTripper инжектирует Authorization header
       на уровне transport, зануляет после записи в wire
    │
    ▼
Исходящий запрос → Claude API (API-ключ не задержался в http.Request.Header)
```

Компрометация Policy Engine, Cache, Audit Log — **не компрометирует API-ключи**.

---

### 5. Cost Tracker

Учёт расходов и потребления квот. Поддерживает множественные модели биллинга.

#### Модели биллинга

| BillingType | Как считается стоимость | Примеры |
|-------------|----------------------|---------|
| `per_token` | tokens × price_per_1M | Claude API, GPT API |
| `per_image` | images × price_per_image | DALL-E, Midjourney |
| `per_request` | фиксированная цена за вызов | Moderation API |
| `per_second` | compute_time × price_per_second | 3D generation |
| `subscription` | квоты (requests, tokens) | Claude Pro, GPT Plus |

#### Write buffering

Cost Tracker использует **in-memory write buffer** с периодическим flush в SQLite:
- Записи накапливаются в ring buffer
- Flush каждые 1 секунду или при 100 записях (что наступит раньше)
- `Flush()` вызывается при graceful shutdown
- Снижает write contention при concurrent нагрузке

#### SQL schema

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
```

#### Иерархия бюджетов (четыре уровня)

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

---

### 6. Audit Log

Append-only лог всех запросов. Хранит **только метаданные** — никогда тело запроса.

#### Что логируется

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
  "cost_usd": 0.000201,
  "latency_ms": 847,
  "privacy_score": 0.12,
  "pii_entities_found": 0,
  "cache_hit": false,
  "route_matched": "code_tasks",
  "status": "ok",
  "hmac": "a3f9b2c1..."
}
```

#### Целостность

- WORM-семантика через SQLite triggers (UPDATE/DELETE запрещены)
- **Обязательная HMAC chain**: каждая запись содержит `hmac = sha256(content || prev_hmac)`. Позволяет обнаружить любую модификацию. Верификация через `audit.VerifyChain(from, to)`
- HMAC ключ производный от master password, но отделён от encryption key (HKDF)
- Configurable retention: `audit_log_retention_days: 90`

---

### 7. Semantic Cache

Кэширует ответы на семантически похожие запросы.

#### Алгоритм

```
Новый запрос → embed(text_content) → cosine_similarity с кэшем
    │
    ├─ similarity > threshold (0.95) → вернуть кэшированный ответ
    │
    └─ miss → отправить провайдеру → сохранить в кэш
```

Эмбеддинги генерируются локально через `nomic-embed-text` (Ollama).

#### Cache policy per ServiceType

| ServiceType | Кеширование по умолчанию | Причина |
|-------------|-------------------------|---------|
| `chat` | Да | Детерминистично при temperature=0 |
| `embedding` | Да | Полностью детерминистично |
| `image` | **Нет** | Стохастично (разный seed → разные результаты) |
| `tts` | Конфигурируемо | Детерминистично для одного voice+text |
| `3d_model` | **Нет** | Стохастично |
| `stt` | Нет | Audio input не подходит для semantic similarity |
| `moderation` | Нет | Нет смысла кешировать |

Конфигурация:
```yaml
cache:
  enabled: true
  policies:
    chat: { enabled: true, ttl: 3600 }
    embedding: { enabled: true, ttl: 86400 }
    image: { enabled: false }    # можно включить для конкретных use cases
    tts: { enabled: true, ttl: 86400 }
    3d_model: { enabled: false }
```

#### Что хранится в кэше

Кэш хранит **sanitized ответы** + **зашифрованные PII mappings**:
- Текстовый ответ с placeholder'ами (не с реальными PII)
- PII mapping зашифрован per-entry (AES-256-GCM)
- При cache hit: расшифровать mapping → применить restoration → вернуть clean ответ
- PII никогда не хранится в plaintext на диске

#### Vector index

In-memory vector index для O(log n) поиска вместо brute-force O(n) scan по SQLite:
- Embeddings загружаются в память при старте
- Обновляются при добавлении новых записей
- SQLite — persistent backend для durability

---

### 8. Provider Adapters

Адаптеры проксируют запросы к AI-провайдерам. Каждый провайдер объявляет `SupportedServices()`.

```go
type Provider interface {
    ID() ProviderID
    SupportedServices() []ServiceType
    Execute(ctx context.Context, req *SanitizedRequest) (*Response, error)
    EstimateCost(req *ParsedRequest) (*CostEstimate, error)
    HealthCheck(ctx context.Context) error
}
```

| Провайдер | ServiceTypes | BillingType |
|-----------|-------------|------------|
| Claude | chat | per_token / subscription |
| OpenAI | chat, image, embedding, tts, stt, moderation | per_token / per_image / per_request |
| Gemini | chat, embedding | per_token / subscription |
| Ollama | chat, embedding, image (SD) | бесплатно (локальный) |

Provider получает `SanitizedBody` (pass-through) и проксирует к API с auth header.

---

## Поток запроса: end-to-end

```
1. Приложение: POST http://localhost:8080/v1/chat/completions
   Headers: X-Prism-Token: prism_tok_xxx, X-Prism-Tags: code,debug

2. Ingress:
   a. Валидация mTLS → проверка токена (HMAC lookup + constant-time compare) → rate limit
   b. Определение ServiceType из URL path
   c. Извлечение TextParts из body + сохранение RawBody

3. Privacy Pipeline (только для текстовых полей):
   a. PII Detector: сканирует TextParts (Regex + Ollama NER + Presidio)
   b. Если risk > порог → route to Ollama (данные не покидают машину)
   c. Substitution: подменяет PII в RawBody → SanitizedBody, создать Map Table в RAM

4. Policy Engine:
   a. Budget Guard: проверить бюджет (EstimateCost от Provider)
   b. Router: найти первое совпавшее правило (с учётом ServiceType + SupportedServices)
   c. Semantic Cache: есть ли похожий запрос? (если cache enabled для данного ServiceType)

5. Если cache miss:
   a. Vault: custom RoundTripper подписывает исходящий HTTP-запрос
   b. Отправить SanitizedBody → провайдеру
   c. Получить ответ

6. Privacy Pipeline (обратно, только для текстовых ответов):
   a. Restoration: заменить [PLACEHOLDER_N] → оригинальные данные
   b. Map Table → explicit_bzero (best-effort)
   Для бинарных ответов (image, audio, 3D): пропустить restoration

7. Cost Tracker: записать метрики в in-memory buffer (async flush в SQLite)
8. Audit Log: записать метаданные + HMAC chain (async)

9. Вернуть ответ приложению
```

---

## Локальный UI (дашборд)

Доступен по `http://localhost:8081`. **Требует аутентификацию** (local token или session cookie).

**Страницы**:
- **Overview** — total spend today/month, активные проекты, статус провайдеров, service type breakdown
- **Cost** — графики расходов по проектам, моделям, провайдерам, service types; экономия от кэша
- **Requests** — последние N запросов (метаданные, без тел), фильтрация по service_type
- **Budgets** — настройка лимитов, алертов
- **Routing** — редактор `routes.yaml` с валидацией
- **Privacy** — настройка профилей, custom patterns, тест-режим
- **Vault** — регистрация приложений, выдача токенов, управление провайдерами

---

## Конфигурационные файлы

```
~/.prism/
├── config.yaml          # основной конфиг (порты, retention, PII detector tier)
├── routes.yaml          # правила маршрутизации
├── budgets.yaml         # бюджеты по проектам
├── privacy.yaml         # профили и custom patterns
├── secrets.db           # зашифрованный vault (SQLite + per-record AES)
├── audit.db             # audit log (SQLite, HMAC chain)
├── cost.db              # cost tracking (SQLite)
├── cache.db             # semantic cache (SQLite)
├── ca.crt               # self-signed CA сертификат
└── ca.key               # приватный ключ CA (chmod 600)
```

---

## Deployment

### Tiered deployment

| Tier | Stack | Что работает | Когда |
|------|-------|-------------|-------|
| 1 (minimal) | Go binary | Proxy, routing, cost, audit, regex PII | Быстрый старт, нет внешних зависимостей |
| 2 (recommended) | Go + Ollama | + Ollama NER, semantic cache, local inference | Production use |
| 3 (maximum) | Go + Ollama + Presidio | + максимальное качество PII detection | Enterprise, multi-language NER |

Выбор при `prism init --tier 2`.

### Первый запуск

```bash
prism init --tier 2
# → генерирует CA сертификат
# → запрашивает Master Password
# → создаёт БД
# → проверяет доступность Ollama (для tier 2+)

prism vault add-provider --provider claude --api-key sk-ant-...
prism vault register --project my-app --ttl 720h
# → prism_tok_xxxxxxxxxxxxxxxx
```

### Автозапуск

```bash
prism install-service    # macOS launchd / Linux systemd / Windows service
```
