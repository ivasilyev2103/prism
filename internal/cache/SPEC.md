# Cache — Module Spec

> Загружать при работе над `internal/cache/`.
> Дополнительно: `docs/INTERFACES.md`.


## Назначение

Семантический кэш: кэширует ответы на похожие запросы по cosine similarity эмбеддингов. Эмбеддинги генерируются локально через Ollama (`nomic-embed-text`).

Prism — универсальный AI-шлюз, поэтому кэш учитывает `ServiceType`: детерминистичные сервисы (chat, embedding) кэшируются по умолчанию, стохастичные (image, 3d_model) — нет.

Кэш хранит **sanitized ответы** + **зашифрованные PII mappings** (AES-256-GCM per entry). PII никогда не хранится на диске в plaintext.

## Реализуемые интерфейсы

- `cache.SemanticCache`
- `cache.Embedder`

## Зависимости

| Направление | Модуль | Что используется |
|-------------|--------|------------------|
| Использует | `internal/types` | SanitizedRequest, Response, ServiceType, CachePolicy |
| Используется в | `internal/policy` | SemanticCache.Get / Set / Invalidate |

## Структура файлов

```
internal/cache/
├── cache.go          # интерфейсы (SemanticCache, Embedder, CachePolicy)
├── semantic.go       # реализация SemanticCache
├── embedder.go       # HTTP-клиент к Ollama (nomic-embed-text)
├── storage.go        # SQLite (modernc.org/sqlite) — persistent backend
├── index.go          # in-memory vector index (O(log n) lookup)
├── similarity.go     # cosine similarity
├── crypto.go         # AES-256-GCM шифрование PII mappings per entry
├── policy.go         # CachePolicy per ServiceType, проверка enabled/TTL
├── doc.go
├── cache_test.go
└── SPEC.md
```

## CachePolicy per ServiceType

Каждый `ServiceType` имеет свою политику кэширования. Get/Set методы проверяют `CachePolicy` для `ServiceType` текущего запроса и возвращают miss / no-op если кэширование отключено.

| ServiceType | По умолчанию | Причина |
|-------------|-------------|---------|
| `chat` | **Включено** | Детерминистично при temperature=0 |
| `embedding` | **Включено** | Полностью детерминистично |
| `image` | **Отключено** | Стохастично (разный seed — разные результаты) |
| `tts` | Конфигурируемо | Детерминистично для одного voice+text |
| `3d_model` | **Отключено** | Стохастично |
| `stt` | Отключено | Audio input не подходит для semantic similarity |
| `moderation` | Отключено | Нет смысла кэшировать |

Конфигурация в `~/.prism/config.yaml`:
```yaml
cache:
  enabled: true
  policies:
    chat: { enabled: true, ttl: 3600 }
    embedding: { enabled: true, ttl: 86400 }
    image: { enabled: false }        # можно включить для конкретных use cases
    tts: { enabled: true, ttl: 86400 }
    3d_model: { enabled: false }
```

## Хранение: sanitized ответы + зашифрованные PII mappings

Кэш хранит два компонента для каждой записи:

1. **Sanitized response** — ответ с placeholder'ами вместо реальных PII
2. **Encrypted PII mapping** — зашифрованный per-entry (AES-256-GCM) маппинг placeholder → оригинальное значение

При cache hit:
1. Найти семантически похожий запрос (cosine similarity > threshold)
2. Расшифровать PII mapping для данной записи
3. Применить restoration (подменить placeholder'ы на оригинальные данные)
4. Вернуть восстановленный ответ

Для бинарных ответов (image, audio, 3D) PII mapping не хранится — restoration не применяется.

## In-memory vector index

Вместо brute-force O(n) scan по SQLite используется **in-memory vector index** для O(log n) lookup:

- Embeddings загружаются в память при старте из SQLite
- Index обновляется при добавлении новых записей (`Set`)
- SQLite (`modernc.org/sqlite`, pure Go, без CGO) — persistent backend для durability
- При рестарте — index восстанавливается из SQLite

## Ключевые ограничения

### Cosine similarity threshold — глобальный
**Ограничение**: `threshold = 0.95` — одно значение для всех проектов.
**Планируется**: per-project threshold в конфиге.

### Ollama для эмбеддингов — опциональная зависимость
**Ограничение**: без Ollama Semantic Cache недоступен.
**Поведение**: если Ollama недоступна → `Get` возвращает miss (не ошибку), `Set` — no-op. Policy Engine продолжает без кэша. Приложение не знает о деградации — просто нет cache hit.
**Тест**: `TestSemanticCache_OllamaUnavailable_ReturnsMiss`.

### CachePolicy проверяется в Get и Set
**Поведение**: `Get(ctx, req)` и `Set(ctx, req, resp)` первым делом проверяют `CachePolicy` для `req.ServiceType`. Если кэширование для данного типа отключено:
- `Get` → возвращает `(nil, nil)` (miss)
- `Set` → no-op (не записывает)

### PII encryption per entry
**Ограничение**: каждая запись шифруется отдельным ключом (derived от master key + entry ID). Компрометация одной записи не раскрывает другие.

## Пример использования

```go
// Проверка кэша перед запросом к провайдеру
// Get автоматически проверяет CachePolicy для req.ServiceType
cached, err := cache.Get(ctx, sanitizedReq)
if err == nil && cached != nil {
    // cache hit — возвращаем без запроса к провайдеру
    // PII mapping расшифрован и применён внутри Get
    return cached, nil
}

// Cache miss (или кэширование отключено для данного ServiceType)
// Запрашиваем провайдера
resp, err := provider.Execute(ctx, sanitizedReq)
if err == nil {
    // Сохраняем в кэш асинхронно
    // Set проверяет CachePolicy — если image/3d_model, будет no-op
    go cache.Set(ctx, sanitizedReq, resp)
}
```
