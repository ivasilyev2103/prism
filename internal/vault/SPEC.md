# Vault — Module Spec

> Загружать при работе над `internal/vault/`.
> Дополнительно: `docs/INTERFACES.md` + `docs/SECURITY.md`.

## Назначение

Единственный модуль, который знает plaintext API-ключи провайдеров.
Реализует паттерн **Sign-not-Expose**: подписывает HTTP-запросы к провайдерам самостоятельно, не передавая ключ другим модулям.

## Реализуемые интерфейсы

- `vault.Vault` — см. `docs/INTERFACES.md`

## Зависимости

| Направление | Модуль | Зачем |
|-------------|--------|-------|
| Использует | `internal/types` | типы `ProviderID` |
| Используется в | `internal/ingress` | `ValidateToken` |
| Используется в | `internal/policy` | `SignRequest` |

## Структура файлов

```
internal/vault/
├── vault.go           # интерфейс Vault + конструктор New()
├── impl.go            # конкретная реализация vaultImpl
├── storage.go         # modernc.org/sqlite + per-record AES-256-GCM, CRUD для секретов
├── kdf.go             # Argon2id key derivation + mlock
├── crypto.go          # AES-256-GCM шифрование/расшифровка + explicit_bzero
├── transport.go       # signingRoundTripper — custom http.RoundTripper для Sign-not-Expose
├── tokens.go          # RegisterProject (tokenTTL), ValidateToken (HMAC lookup), RevokeToken
├── rotate.go          # RotateProviderKey — ротация API-ключей
├── doc.go             # package-level documentation
├── vault_test.go      # тесты
└── SPEC.md            # этот файл
```

## Ключевые решения реализации

### Хранилище: modernc.org/sqlite + per-record AES-256-GCM

БД — `modernc.org/sqlite` (pure Go, без CGO). Шифрование уровня БД (SQLCipher) **не используется**.
Вместо этого каждая запись (API-ключ, токен) шифруется индивидуально через AES-256-GCM.

Преимущества:
- Zero CGO — кросс-компиляция без дополнительных тулчейнов
- Шифрование per-record — компрометация одного nonce не раскрывает другие записи
- Go stdlib crypto (`crypto/aes`, `crypto/cipher`) — аудированная реализация, без внешних зависимостей

```go
// storage.go
import "modernc.org/sqlite"

// secrets.db — обычный SQLite без шифрования на уровне движка.
// Каждый API-ключ хранится как: nonce (12 байт) || AES-256-GCM(key, nonce, plaintext)
// Encryption Key существует только в RAM (mlock-pinned).
```

### KDF (key derivation)

```go
// kdf.go
// Salt хранится рядом с secrets.db в открытом виде (32 байта).
// Encryption Key существует только в RAM — никогда на диск.
// В Phase 2 Encryption Key размещается в mlock-pinned буфере.
func deriveKey(password []byte, salt []byte) ([]byte, error) {
    return argon2.IDKey(password, salt,
        3,        // iterations
        64*1024,  // memory: 64MB
        4,        // parallelism
        32,       // keyLen: AES-256
    ), nil
}
```

### Шифрование секретов

```go
// crypto.go
// Каждый секрет шифруется с уникальным nonce.
// Формат хранения: nonce (12 байт) || ciphertext
// Crypto errors → немедленный fail, не degraded mode.
func encrypt(key, plaintext []byte) ([]byte, error) { ... }
func decrypt(key, data []byte) ([]byte, error) { ... }

// explicit_bzero зануляет буфер и предотвращает оптимизацию компилятора.
// Вызывать через defer на все буферы с ключами/секретами.
//
// ВАЖНО: это best-effort мера. Go GC — concurrent collector, он может
// скопировать объект в новый адрес памяти до вызова explicit_bzero,
// оставив «призрачную копию» в освобождённой памяти.
// runtime.KeepAlive предотвращает оптимизацию компилятора, но не GC.
//
// Реальная граница безопасности — process isolation (mTLS + loopback binding)
// и mlock-pinned буферы, а не memory zeroing в одиночку.
// См. docs/SECURITY.md → "Ограничения Go Memory Model".
func explicitBzero(b []byte) {
    for i := range b { b[i] = 0 }
    runtime.KeepAlive(b)
}
```

### mlock — обязательно в Phase 2

Encryption Key и расшифрованные API-ключи размещаются в mlock-pinned буферах через `golang.org/x/sys/unix`.

Что даёт mlock:
- **Предотвращает swap** — ключи не попадают на диск через swap partition
- **Фиксирует адрес** — GC не перемещает данные, explicit_bzero зануляет единственную копию

```go
// kdf.go
// mlockBuffer выделяет буфер фиксированного размера и закрепляет в RAM.
// На Linux/macOS: mlock(2). На Windows: VirtualLock.
// Если mlock недоступен (ulimit) — логируем предупреждение, продолжаем работу.
func mlockBuffer(size int) ([]byte, error) { ... }

// encryptionKey хранится в mlock-pinned буфере.
// Расшифрованные API-ключи в SignRequest — также mlock-pinned,
// зануляются через defer explicitBzero после записи в wire.
```

### Sign-not-Expose: custom RoundTripper

Прежний подход (прямая установка header в `req.Header.Set`) оставлял plaintext ключ в структуре `http.Request`, доступной вызывающему коду после возврата из `SignRequest`.

Новый подход: custom `http.RoundTripper`, который инжектирует auth header **на уровне transport** и зануляет его сразу после записи в wire.

```go
// transport.go
// signingRoundTripper оборачивает http.DefaultTransport.
// Ключ инжектируется непосредственно перед отправкой и зануляется после.
// API-ключ не задерживается в http.Request.Header.
type signingRoundTripper struct {
    base      http.RoundTripper
    key       []byte // расшифрованный ключ (mlock-pinned)
    headerKey string // "Authorization" | "X-API-Key" и т.д.
}

func (rt *signingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
    // 1. Клонировать req (не мутировать оригинал)
    clone := req.Clone(req.Context())

    // 2. Инжектировать auth header
    headerValue := "Bearer " + string(rt.key)
    clone.Header.Set(rt.headerKey, headerValue)

    // 3. Отправить запрос через базовый transport
    resp, err := rt.base.RoundTrip(clone)

    // 4. Занулить header value сразу после wire write
    clone.Header.Del(rt.headerKey)
    explicitBzero([]byte(headerValue))

    return resp, err
}

// impl.go
func (v *vaultImpl) SignRequest(ctx context.Context, projectID string, providerID types.ProviderID, req *http.Request) error {
    // 1. Проверить права projectID на providerID
    if !v.hasAccess(projectID, providerID) {
        return ErrAccessDenied
    }

    // 2. Расшифровать ключ в mlock-pinned буфер
    key, err := v.getDecryptedKey(providerID)
    if err != nil { return err }
    // key будет занулён в signingRoundTripper после wire write

    // 3. Создать signing transport
    rt := &signingRoundTripper{
        base:      http.DefaultTransport,
        key:       key,
        headerKey: v.authHeaderKey(providerID),
    }

    // 4. Привязать transport к запросу через context или возвратить client
    req.Header.Set("X-Prism-Vault-Signed", "true") // маркер для Policy Engine
    // Policy Engine использует http.Client с rt для отправки
    return nil
}
```

### Валидация токена (HMAC lookup + constant-time compare)

```go
// tokens.go
// Для устранения timing leak через database lookup, в БД хранится
// HMAC(token), а не сам токен. Lookup по HMAC — constant-time
// относительно значения токена (любой невалидный токен даёт одинаковый
// паттерн обращения к БД).
func (v *vaultImpl) ValidateToken(token string) (string, error) {
    // 1. Вычислить HMAC(token) как ключ для поиска в БД
    hmacKey := computeHMAC(v.hmacSecret, []byte(token))

    // 2. Найти запись по HMAC ключу
    record, err := v.lookupTokenByHMAC(hmacKey)
    if err != nil { return "", ErrInvalidToken }

    // 3. Проверить TTL (если задан)
    if record.ExpiresAt > 0 && time.Now().Unix() > record.ExpiresAt {
        return "", ErrTokenExpired
    }

    // 4. constant-time compare HMAC (финальная проверка)
    if subtle.ConstantTimeCompare(hmacKey, record.HMACKey) != 1 {
        return "", ErrInvalidToken
    }

    return record.ProjectID, nil
}
```

### Ротация API-ключей провайдера

```go
// rotate.go
// RotateProviderKey атомарно заменяет API-ключ провайдера.
// Старый ключ немедленно удаляется и зануляется.
// Новый ключ шифруется и сохраняется в одной транзакции.
func (v *vaultImpl) RotateProviderKey(providerID types.ProviderID, newAPIKey string) error {
    // 1. Проверить, что провайдер существует
    if !v.providerExists(providerID) {
        return ErrProviderNotFound
    }

    // 2. Зашифровать новый ключ
    newEncrypted, err := encrypt(v.encryptionKey, []byte(newAPIKey))
    if err != nil { return err }

    // 3. В одной SQLite-транзакции: заменить зашифрованный ключ
    err = v.db.WithTx(func(tx *sql.Tx) error {
        _, err := tx.Exec("UPDATE providers SET encrypted_key = ?, updated_at = ? WHERE id = ?",
            newEncrypted, time.Now().Unix(), string(providerID))
        return err
    })
    if err != nil { return err }

    // 4. Занулить plaintext нового ключа из аргумента
    explicitBzero([]byte(newAPIKey))

    return nil
}
```

## Ограничения

### Мастер-пароль запрашивается при каждом старте
**Ограничение**: Encryption Key не персистируется. При перезапуске Prism требует ввода мастер-пароля.
**Причина**: намеренно. Хранение Encryption Key на диске сводит на нет защиту.
**Воркэраунд для автозапуска**: использовать `systemd` с `StandardInput=tty` или secret-менеджер ОС (Keychain, Secret Service). Реализация в Фазе 9.
**Планируется**: опциональная интеграция с OS keychain (Фаза 9+).

### explicit_bzero — best-effort в Go
**Ограничение**: Go GC может скопировать объект в новый адрес памяти до вызова `explicitBzero`, оставив «призрачную копию» в освобождённой памяти. `runtime.KeepAlive` предотвращает оптимизацию компилятора, но не GC. Go strings immutable и heap-allocated — конкатенация `string(key)` создаёт копию вне контроля `explicitBzero`.
**Митигация**: mlock-pinned буферы (Phase 2) фиксируют адрес, explicit_bzero зануляет единственную копию. Реальная граница безопасности — process isolation + mlock, не memory zeroing в одиночку.
**Подробности**: `docs/SECURITY.md` → "Ограничения Go Memory Model".

### mlock зависит от ulimit
**Ограничение**: `mlock(2)` требует достаточного лимита `RLIMIT_MEMLOCK`. По умолчанию 64KB в некоторых дистрибутивах Linux.
**Митигация**: если mlock недоступен — логируем предупреждение уровня WARN, продолжаем работу с best-effort explicit_bzero. Документируем требование в `prism init`.

## Примеры использования

```go
// Инициализация Vault
v, err := vault.New(vault.Config{
    DBPath:         filepath.Join(home, ".prism", "secrets.db"),
    MasterPassword: []byte(os.Getenv("PRISM_MASTER_PASSWORD")), // или интерактивный ввод
})
if err != nil {
    log.Fatal("vault init failed:", err)
}
defer v.Close() // зануляет Encryption Key из RAM

// Добавление провайдера
err = v.AddProvider(types.ProviderClaude, "sk-ant-api03-...", []string{"*"})

// Ротация API-ключа провайдера
err = v.RotateProviderKey(types.ProviderClaude, "sk-ant-api03-NEW...")
// Старый ключ немедленно удалён и занулён

// Регистрация приложения (с TTL)
token, err := v.RegisterProject("game-engine",
    []types.ProviderID{types.ProviderClaude, types.ProviderOllama},
    720*time.Hour, // TTL: 30 дней; 0 = бессрочный
)
// token = "prism_tok_7xK2mN..." — передать в приложение

// Валидация входящего токена (в Ingress)
// Использует HMAC(token) для lookup в БД — constant-time относительно значения токена
projectID, err := v.ValidateToken(r.Header.Get("X-Prism-Token"))
if err != nil {
    http.Error(w, "unauthorized", 401)
    return
}

// Подпись исходящего запроса (в Policy Engine)
// Custom RoundTripper инжектирует auth header на уровне transport,
// зануляет после записи в wire. Plaintext ключ не задерживается в http.Request.Header.
req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", body)
err = v.SignRequest(ctx, projectID, types.ProviderClaude, req)
```

## Тесты

Файл: `internal/vault/vault_test.go`

Ключевые кейсы:
- `TestVault_SignNotExpose_KeyNeverLeaks` — через reflection проверяем, что ключ не в возвращаемых значениях и не в req.Header после SignRequest
- `TestVault_SignNotExpose_RoundTripper` — проверяем, что custom RoundTripper зануляет header после wire write
- `TestVault_KeyZeroedOnAllPaths` — mock panic, проверяем что defer сработал
- `TestVault_KDFParameters_OWASPCompliant` — параметры Argon2id соответствуют требованиям
- `TestVault_WrongPassword_ReturnsError` — не паника
- `TestVault_NonceUniqueness` — 1000 encrypt — все nonce уникальны
- `TestVault_ConstantTimeTokenCompare` — время валидации не зависит от позиции ошибки
- `TestVault_TokenHMACLookup` — ValidateToken использует HMAC(token) для lookup, не сам токен
- `TestVault_TokenTTL_Expired` — токен с истёкшим TTL возвращает ErrTokenExpired
- `TestVault_TokenTTL_Valid` — токен в пределах TTL проходит валидацию
- `TestVault_TokenTTL_Zero_NeverExpires` — tokenTTL=0 означает бессрочный
- `TestVault_RevokedToken_Returns401` — после RevokeToken валидация провалена
- `TestVault_RotateProviderKey` — после RotateProviderKey старый ключ недоступен, новый работает
- `TestVault_RotateProviderKey_NonExistent` — ротация несуществующего провайдера возвращает ошибку
- `TestVault_MlockBuffer` — encryption key размещается в mlock-pinned буфере (если доступен)
- `TestVault_PureGoSQLite` — БД открывается через modernc.org/sqlite без CGO
