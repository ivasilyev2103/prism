# Prism — LocalAI Gateway

> Читать в начале каждой сессии. Не расширять — этот файл должен оставаться кратким.

## Что строим

Локальный reverse proxy между приложениями и облачными AI-сервисами.
Не только LLM — также text-to-image, TTS, text-to-3D, embeddings и другие AI API.
Основные потребители — локальные приложения по API (не интерактивные пользователи).
Приложения меняют только `base_url` → `localhost:8080`. Всё остальное делает Prism.

Три главных свойства: **privacy-first** (PII обфусцируется до облака) · **cost control** (бюджеты и лимиты) · **smart routing** (правила маршрутизации между провайдерами).

## Навигация по документации

| Что нужно | Файл |
|-----------|------|
| Текущая фаза, задачи, acceptance criteria | `docs/PHASES.md` |
| Go-интерфейсы всех модулей (контракты) | `docs/INTERFACES.md` |
| Полная архитектура, схемы, SQL | `docs/ARCHITECTURE.md` |
| Security-требования, threat model | `docs/SECURITY.md` |
| Спека конкретного модуля | `internal/<module>/SPEC.md` |

**Правило**: при работе над модулем X загружать `docs/INTERFACES.md` + `internal/X/SPEC.md`.
Если модуль security-критичный (vault, privacy) — дополнительно `docs/SECURITY.md`.

## Стек

- **Go** — ядро (1.22+)
- **Go stdlib crypto** — `crypto/aes`, `crypto/cipher`, `crypto/tls`, `golang.org/x/crypto/argon2`
- **SQLite** (`modernc.org/sqlite`, pure Go) — все БД; per-record AES-256-GCM для vault
- **PII detection** — tiered: встроенный regex (Go) + Ollama NER + опционально Presidio (Python sidecar)
- **Argon2id** — KDF для мастер-пароля
- **nomic-embed-text** через Ollama — локальные эмбеддинги (опционально, для semantic cache)

## Архитектура: pass-through

Prism — прозрачный прокси. Он не полностью парсит тела запросов в свои типы.
Вместо этого: извлекает текстовые части для PII-сканирования + metadata для routing.
Оригинальное тело запроса сохраняется как `json.RawMessage` и проксируется к провайдеру.
Это позволяет поддерживать любой формат AI API без изменения кода Prism.

## Принципы (не нарушать)

### SOLID
- **S**: каждый пакет `internal/X` делает одну вещь. Граница — его интерфейс из `INTERFACES.md`.
- **O**: новая функциональность — новая реализация интерфейса, не правка существующей.
- **L**: все реализации `Provider`, `Detector`, `BudgetChecker` — взаимозаменяемы.
- **I**: интерфейсы маленькие и сфокусированные. Не добавлять методы "на вырост".
- **D**: модули зависят только от интерфейсов из `INTERFACES.md`, никогда от конкретных типов других пакетов.

### Безопасность
- Секреты не логируются нигде и никогда (lint rule в CI).
- Map Table (PII ↔ placeholder) — только в RAM, `defer destroyMapTable()` первой строкой.
- Crypto errors → немедленный fail, не degraded mode.
- Prism биндится только на `127.0.0.1`, никогда `0.0.0.0`.
- Memory zeroing — **best-effort** в Go (GC может копировать данные). Реальная граница безопасности — process isolation (mTLS + loopback binding). См. `docs/SECURITY.md`.
- Dashboard (`localhost:8081`) требует аутентификацию (local token).

### Тесты
- Тест-файл рядом с реализацией: `foo.go` → `foo_test.go`.
- `go test -race ./...` — обязателен, гонки данных недопустимы.
- `vault` и `privacy` — 100% покрытие публичного интерфейса.
- Все остальные модули — минимум 80%.
- Golden tests для privacy: фикстуры в `testdata/privacy/*.json`.

### Документация
- При изменении публичного интерфейса → обновить `internal/X/SPEC.md` в том же коммите.
- HTML-документация генерируется в Фазе 10 по готовым SPEC.md файлам.

## Структура проекта

```
prism/
├── CLAUDE.md
├── cmd/prism/main.go
├── internal/
│   ├── vault/       ├── privacy/     ├── ingress/
│   ├── policy/      ├── cost/        ├── audit/
│   ├── cache/       └── provider/
├── config/routes.yaml
├── testdata/privacy/
├── docs/
└── ui/
```

## Текущее состояние

**Фаза**: см. `docs/PHASES.md` → раздел "Текущая фаза".
Код не написан. Начинать с Фазы 1 (интерфейсы и скелет).
