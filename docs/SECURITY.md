# Security Specification — Prism

Этот документ описывает все security-решения, их обоснование и конкретные требования к реализации.

> **Правило #1**: При любом сомнении между удобством и безопасностью — выбирать безопасность.
> **Правило #2**: Fail-safe defaults. Если компонент недоступен → отказ, не деградация в небезопасный режим.
> **Правило #3**: Fail-closed применяется к security-critical путям (облачные провайдеры). Для local-only провайдеров (Ollama) PII detection — QoS, не security requirement (данные не покидают машину).

---

## Модель угроз (Threat Model)

### Что защищаем

| Актив | Ценность | Последствие компрометации |
|-------|----------|--------------------------|
| API-ключи провайдеров | Высокая | Финансовые потери (тысячи $/день) |
| PII пользователей | Высокая | GDPR/нарушение приватности |
| Содержимое запросов | Средняя | Утечка бизнес-логики |
| Audit log | Средняя | Потеря доказательной базы |
| Local tokens приложений | Средняя | Несанкционированный доступ к Prism |
| Dashboard данные | Средняя | Утечка бюджетов, маршрутов, конфигурации |

### Кто атакует

| Угроза | Вектор | Митигация |
|--------|--------|-----------|
| Вредоносный процесс на машине | Перехват localhost трафика | mTLS, bind только loopback |
| Вредоносный процесс | Чтение памяти другого процесса | Process isolation, mlock (best-effort) |
| Компрометация одного модуля Prism | Боковое движение к ключам | Sign-not-Expose, изоляция Vault |
| Кража файлов `~/.prism/` | Расшифровка секретов | AES-256-GCM + Argon2id KDF |
| Сниффинг исходящего HTTPS | Перехват API-ключей | TLS 1.3, certificate pinning (опционально) |
| Prompt injection в ответах | Подмена placeholder'ов | Whitelist substitution, sanitization ответов |
| Runaway агент | Бесконечные дорогие запросы | Budget Guard, Rate Limiting |
| Перехват Presidio трафика | PII leak через plaintext HTTP | Unix socket с file permissions |
| Доступ к dashboard без auth | Модификация конфигурации | Аутентификация на dashboard |

### Что НЕ в scope

- Атаки на физический доступ к машине (full disk encryption — на пользователе)
- Компрометация самих облачных провайдеров
- Атаки на ОС уровня kernel
- PII в сгенерированных изображениях/аудио (не детектируемо без отдельного CV/NLP-модуля)

---

## Ограничения Go Memory Model

> Этот раздел описывает фундаментальные ограничения Go для memory-secure операций.
> Прочитать при работе над vault и privacy модулями.

Go GC — concurrent collector. Он может **скопировать** объект в новый адрес памяти, оставив «призрачную копию» старых данных в освобождённой памяти. Это означает:

1. **`explicit_bzero + runtime.KeepAlive` — best-effort, не гарантия.** Предотвращает оптимизацию компилятора, но не контролирует GC.

2. **Go strings immutable и heap-allocated.** Конкатенация `string(key)` создаёт новую строку, которую `explicit_bzero` не затрагивает.

3. **`map[string]string` (Map Table)** — ключи и значения как Go-строки. `destroyMapTable` зануляет map, но копии от GC или конкатенации — вне контроля.

### Реальная граница безопасности

| Механизм | Гарантия | Уровень |
|----------|---------|---------|
| mTLS + loopback binding | Сетевая изоляция | **Надёжный** |
| Process isolation | Защита памяти процесса от внешних процессов | **Надёжный** |
| `explicit_bzero` | Зануление конкретного буфера | **Best-effort** |
| `mlock` (pinned memory) | Предотвращение swap + фиксация адреса | **Надёжный** (если доступен) |

### Обязательные меры (Phase 2)

- `mlock` через `golang.org/x/sys/unix` для encryption key и Map Table буферов
- Для `SignRequest`: custom `http.RoundTripper`, инжектирующий auth header на уровне transport и зануляющий после записи в wire (см. `internal/vault/SPEC.md`)
- Не полагаться на memory zeroing как единственную защиту — всегда в паре с process/network isolation

---

## Сетевая безопасность

### mTLS на localhost

Даже на 127.0.0.1 другие процессы могут читать трафик через `/proc` или ptrace. mTLS решает это.

**Реализация**:
```
prism init → генерирует RSA-4096 или ECDSA P-384 CA
         → для каждого зарегистрированного приложения выдаёт клиентский сертификат
         → Prism требует валидный клиентский сертификат на каждом запросе
```

**Параметры TLS**:
- Минимальная версия: TLS 1.3
- Cipher suites: только AEAD (TLS_AES_256_GCM_SHA384, TLS_CHACHA20_POLY1305_SHA256)
- ECDHE для forward secrecy

### Биндинг только на loopback

```go
// ПРАВИЛЬНО
listener, err := net.Listen("tcp", "127.0.0.1:8080")

// ЗАПРЕЩЕНО — даже если пользователь просит
listener, err := net.Listen("tcp", "0.0.0.0:8080")
```

Если нужен доступ с другой машины (редкий кейс) — пользователь сам настраивает SSH tunnel.

### Local Token Auth

Каждое приложение при регистрации получает токен:
```
prism_tok_{project_id_prefix}_{32_random_bytes_base58}
```

Токен передаётся в заголовке: `X-Prism-Token: prism_tok_...`

**Хранение токенов**: только в Vault (зашифрованно).

**Валидация**: HMAC(token) используется как ключ для lookup в БД (constant-time lookup), затем `subtle.ConstantTimeCompare` для финальной проверки. Это устраняет timing leak через database lookup.

**TTL**: токены поддерживают optional TTL. По умолчанию бессрочные, рекомендуется устанавливать TTL для production.

**Ротация**: `prism vault rotate-token --project my-app` — старый токен инвалидируется немедленно.

### Dashboard аутентификация

Dashboard на `localhost:8081` **требует аутентификацию**:
- Сессия через local token (тот же `X-Prism-Token`) или session cookie, производный от master password
- Без аутентификации любой локальный процесс может управлять vault, бюджетами, маршрутами
- CSRF protection для form submissions

---

## Secrets Vault

### Key Derivation

```
Master Password (UTF-8, min 12 символов)
    +
Salt (32 random bytes, хранится рядом с secrets.db в открытом виде)
    │
    ▼ Argon2id
    │   memory:     64 MB
    │   iterations: 3
    │   parallelism: 4
    │   key length: 32 bytes
    ▼
Encryption Key (только в RAM процесса Prism, никогда на диск)
              (mlock-pinned если доступен)
```

**Почему Argon2id**: memory-hard алгоритм, рекомендован OWASP. Даже при GPU-атаке: 64MB × количество_параллельных_попыток = ограничивает throughput.

**Когда ключ удаляется из RAM**:
- При штатном завершении Prism (`prism stop`)
- При получении SIGTERM/SIGINT
- Через `defer explicit_bzero` при завершении main
- **Ограничение**: Go GC может скопировать до зануления (см. "Ограничения Go Memory Model")

### Шифрование секретов

Каждый секрет (API-ключ) шифруется отдельно:

```
plaintext_key = "sk-ant-api03-..."
nonce = random 12 bytes (уникальный для каждого секрета)
ciphertext = AES-256-GCM(encryption_key, nonce, plaintext_key)

stored = nonce || ciphertext   # конкатенация
```

Уникальный nonce на каждый секрет = защита от nonce-reuse атак.

**БД**: `modernc.org/sqlite` (pure Go, без CGO). Шифрование per-record через AES-256-GCM — SQLCipher не нужен.

### Sign-not-Expose

```go
// Vault — единственный, кто может это сделать.
// Реализация через custom RoundTripper:
// 1. roundTripper получает req без auth header
// 2. На уровне transport: инжектирует header, пишет в wire, зануляет header
// 3. API-ключ не задерживается в http.Request.Header
func (v *Vault) SignRequest(projectID string, provider string, req *http.Request) error {
    // ... детали в internal/vault/SPEC.md
}
```

Policy Engine вызывает `vault.SignRequest(req)` — он никогда не получает сам ключ.

### Key rotation

```bash
# Ротация API-ключа провайдера
prism vault rotate-key --provider claude --api-key sk-ant-NEW...
# Старый ключ немедленно удаляется, новый сохраняется
```

### Защита от memory dumps (best-effort)

```go
// После использования ключа
func explicit_bzero(b []byte) {
    for i := range b {
        b[i] = 0
    }
    runtime.KeepAlive(b)  // предотвращает оптимизацию компилятором
}
// ВАЖНО: это best-effort. См. "Ограничения Go Memory Model".
```

---

## Privacy Pipeline: безопасность

### Инварианты Map Table

Нарушение любого из этих инвариантов — критический баг:

1. **Map Table создаётся в начале запроса** — `defer destroyMapTable()` должен быть первой строкой после создания
2. **Map Table не передаётся между горутинами** — только в рамках одного request context
3. **Map Table не сериализуется** — нет JSON/protobuf/gob представления
4. **Map Table не логируется** — `%v` format на структуре запроса в логах должен исключать Map Table
5. **explicit_bzero при уничтожении** — не просто `mt = nil`

### Presidio sidecar: безопасность коммуникации

**Требование**: коммуникация с Presidio через **Unix domain socket** с file permissions (chmod 600), не через HTTP.

Обоснование: PII отправляется на Presidio **до обфускации** — это самые чувствительные данные. Plaintext HTTP на localhost подвержен тем же атакам, от которых mTLS на порту 8080 защищает.

Если Unix socket недоступен (Windows): использовать loopback + TLS.

**Rate limiting**: semaphore на Presidio клиенте (max concurrent requests = количество spaCy workers), предотвращает DoS через перегрузку sidecar.

### Fail-closed vs. Fail-open по типу провайдера

| Провайдер | PII detection | Поведение при отказе Presidio |
|-----------|-------------|------------------------------|
| Облачный (Claude, OpenAI, Gemini) | Обязательно | **Fail-closed**: запрос отклоняется |
| Локальный (Ollama) | Опционально | **Pass-through**: данные не покидают машину |

### Защита от prompt injection через placeholder'ы

Атака: вредоносный ответ модели содержит `[PERSON_1]` пытаясь подменить данные.

Митигация:
- Whitelist: восстанавливаем **только** те placeholder'ы, которые мы сами создали в этом запросе
- Формат placeholder'ов содержит уникальный request ID: `[PERSON_a3f9_1]`
- Placeholder из чужого запроса → игнорируется, не восстанавливается

### PII в не-текстовых ответах

Для `ServiceType = image | tts | 3d_model`: PII detection **не применяется** к бинарным ответам.
PII может содержаться в сгенерированных изображениях (текст на вывеске), но детекция требует Computer Vision, что out of scope.

PII detection применяется **только** к текстовым частям запроса (`TextParts`).

### Логирование Privacy Pipeline

```go
// ЗАПРЕЩЕНО
log.Debug("Processing request", "messages", req.TextParts)
log.Debug("Map table", "entries", mapTable)

// РАЗРЕШЕНО
log.Debug("Processing request",
    "project_id", req.ProjectID,
    "service_type", req.ServiceType,
    "pii_entities_count", len(detectedEntities),
    "privacy_score", score,
)
```

---

## Audit Log: целостность

### WORM-семантика

```sql
-- Триггер, запрещающий изменение записей
CREATE TRIGGER audit_no_update
BEFORE UPDATE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit log is append-only');
END;

CREATE TRIGGER audit_no_delete
BEFORE DELETE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit log is append-only');
END;
```

**Ограничение**: SQLite triggers защищают только от кода внутри Prism. Обход: открыть `audit.db` другим SQLite клиентом, DROP TRIGGER, прямое редактирование файла. Поэтому HMAC chain — обязательна.

### HMAC Chain (обязательно)

Каждая запись содержит HMAC предыдущей:
```json
{
  "id": "req_002",
  "prev_hmac": "sha256(req_001_content)",
  "content": "...",
  "hmac": "sha256(content || prev_hmac)"
}
```

Позволяет обнаружить любую модификацию цепочки. Верификация через `audit.VerifyChain(from, to)`.

**Ключ для HMAC**: производный от master password, но отдельный от encryption key (HKDF expand с разными info strings).

---

## Конфигурация: чего нельзя делать

### Запрещённые паттерны в конфиг-файлах

```yaml
# ЗАПРЕЩЕНО — ключи в конфиге
providers:
  claude:
    api_key: "sk-ant-..."     # НИКОГДА

# ПРАВИЛЬНО
providers:
  claude:
    key_ref: vault://claude   # ссылка на vault
```

### Линтер-правила (обязательно настроить в CI)

```yaml
# .golangci.yml
linters-settings:
  forbidigo:
    forbid:
      # запрет логирования потенциально чувствительных полей
      - pattern: 'log\..*(key|secret|token|password|api_key)'
        msg: "потенциально чувствительное поле в логах"
      - pattern: 'fmt\.Print.*(key|secret|token|password)'
        msg: "потенциально чувствительное поле в stdout"
```

---

## Dependency Security

### Принципы выбора зависимостей

- Криптографические примитивы — только из Go stdlib (`crypto/tls`, `crypto/aes`, `crypto/cipher`) и `golang.org/x/crypto`
- Новые зависимости — только с явным обоснованием в PR
- `go mod verify` в CI — проверка контрольных сумм
- Без CGO: используем `modernc.org/sqlite` (pure Go), не `mattn/go-sqlite3`

### Supply chain

```bash
# В CI обязательно
go mod verify
govulncheck ./...    # проверка известных CVE в зависимостях
```

---

## Security Checklist (перед каждым релизом)

### Vault
- [ ] Argon2id параметры соответствуют OWASP рекомендациям
- [ ] Encryption Key в mlock-pinned буфере (если доступен)
- [ ] Sign-not-Expose: custom RoundTripper зануляет auth header после wire write
- [ ] explicit_bzero вызывается во всех путях (включая error paths)
- [ ] Key rotation работает корректно
- [ ] Token TTL проверяется при валидации

### Privacy Pipeline
- [ ] Map Table уничтожается в defer (включая panic recovery)
- [ ] Логи не содержат тела запросов и TextParts
- [ ] Placeholder'ы содержат request-unique prefix
- [ ] Whitelist restoration работает корректно
- [ ] Presidio коммуникация через Unix socket (или TLS на Windows)
- [ ] Presidio rate limiting (semaphore) настроен
- [ ] Бинарные ответы (image/audio/3D) не сканируются на PII

### Network
- [ ] Prism не биндится на 0.0.0.0
- [ ] mTLS требует валидный клиентский сертификат
- [ ] TLS 1.2 отключён (только 1.3)
- [ ] Rate limiting активен для всех endpoints
- [ ] Dashboard требует аутентификацию

### Audit
- [ ] WORM триггеры активны
- [ ] HMAC chain работает и верифицируется
- [ ] HMAC ключ отделён от encryption key (HKDF)
- [ ] Нет PII в audit записях

### General
- [ ] `govulncheck` прошёл без критических CVE
- [ ] Все секреты в тестах — фиктивные (не реальные ключи)
- [ ] `git secrets` или аналог настроен для предотвращения коммита ключей
- [ ] Permissions на `~/.prism/` — 700, на `ca.key` — 600
- [ ] Без CGO зависимостей

---

## Инциденты: что делать

### Если скомпрометирован API-ключ

```bash
# 1. Немедленно отозвать у провайдера (на их сайте)
# 2. Ротировать ключ в Vault
prism vault rotate-key --provider claude --api-key sk-ant-NEW...
# 3. Проверить audit log на аномальные запросы
prism audit --from "2 hours ago" --filter "provider=claude"
# 4. Проверить целостность audit chain
prism audit verify-chain --from "24 hours ago"
```

### Если скомпрометирован local token приложения

```bash
prism vault revoke-token --token prism_tok_xxx
prism vault register --project my-app --ttl 720h  # выдать новый с TTL
```

### Если скомпрометирован мастер-пароль

```bash
# 1. Остановить Prism
prism stop
# 2. Отозвать ВСЕ API-ключи у всех провайдеров
# 3. prism init --reset  (пересоздать vault с новым паролем)
# 4. Добавить все ключи заново
```
