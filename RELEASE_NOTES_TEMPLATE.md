# telecli v0.1.0-rc1

## Статус релиза

- Автоматические gates: PASS
- Clean-clone gate: PASS
- macOS Keychain provider C3b: GREEN
- Linux Secret Service provider C3c: NOT VERIFIED в рамках этого релиза
- Direct interactive smoke: NOT VERIFIED / PASS после ручной проверки
- Durable interactive smoke на macOS: NOT VERIFIED / PASS после ручной проверки

## Основные изменения

### Явные режимы отправки

Добавлены два явно выбираемых режима:

- `direct` сохраняет существующий синхронный путь отправки;
- `durable` сохраняет сообщение в зашифрованном SQLite outbox до отправки.

Миграционный default остаётся `direct`.

### Durable encrypted outbox

Durable path использует:

- SQLite для transactional и scheduling metadata;
- application-level XChaCha20-Poly1305 для пользовательского payload;
- macOS Keychain для хранения ключа;
- supervised Dispatcher для доставки и recovery.

Ошибки key provider, cipher, SQLite или runtime initialization обрабатываются fail-closed. Нет fallback на direct mode, `MemoryStore` или plaintext storage.

### Application lifecycle

Добавлены:

- `DurableOutboxRuntime`;
- supervision `Dispatcher.Run`;
- идемпотентное конкурентно-безопасное закрытие;
- правильный shutdown order: health sampler, delivery runtime, Telegram session;
- явный runtime factory для `direct` и `durable`.

### TUI

Composer использует нейтральный application submitter и не вызывает Telegram или outbox напрямую.

Для durable mode отображаются состояния:

- `○ В очереди`
- `● Отправляется`
- `↻ Повторная попытка`
- `× Не отправлено`
- `? Результат неизвестен`
- `✓ Отправлено`
- `− Отменено`

Для `uncertain` показывается предупреждение:

> Повторная отправка может создать дубликат.

Автоматический retry для `uncertain` отсутствует.

### Operational health и telemetry

Добавлены:

- metadata-only operational snapshot;
- runtime health lifecycle;
- безопасная telemetry projection без identifiers и payload;
- durable health sampler с initial и periodic sampling;
- обязательная остановка sampler до закрытия Store.

### Restart/recovery

Integration tests подтверждают:

- queued entry переживает restart;
- retry schedule и `NextAttemptAt` сохраняются;
- до eligibility повторная отправка не выполняется;
- expired dispatch lease становится `uncertain`;
- `uncertain` не отправляется повторно;
- существующая база без ключа остаётся fail-closed;
- replacement key не создаётся;
- direct fallback отсутствует.

## Security

Подтверждены следующие свойства:

- `internal/outbox` остаётся transport-neutral;
- plaintext fallback отсутствует;
- production `MemoryStore` fallback отсутствует;
- payload не попадает в operational snapshot, health, telemetry или status UI;
- metadata status query не выбирает `encrypted_text` и не вызывает decrypt;
- raw backend errors не отображаются пользователю;
- HTML entities отсутствуют в Go-файлах;
- TUI не вызывает `SendTextMessage` или `QueueMessage` напрямую.

## Platform support

### macOS

macOS Keychain provider прошёл:

- compile-only gate без CGO;
- targeted tests `-count=10`;
- race tests;
- `CGO_ENABLED=1` build;
- полный outbox test gate.

Статус: **GREEN**.

### Linux

Linux Secret Service provider не проверялся в текущей macOS release-сессии.

Статус: **NOT VERIFIED**.

## Известные ограничения

- Default mode остаётся `direct`.
- Для durable mode требуется доступный platform key provider.
- SQLite database без соответствующего provider key не восстанавливается через plaintext fallback.
- Копии одного `outbox.db` недостаточно для восстановления без соответствующего секрета Keychain или Secret Service.
- Interactive direct и durable smoke должны быть выполнены в реальном TTY с Telegram credentials.

## Проверки релиза

- `CGO_ENABLED=0 go test ./... -count=1`
- `CGO_ENABLED=0 go build ./...`
- `go test ./... -count=1`
- `go test -race ./... -count=1`
- `go vet ./...`
- `gofmt`
- clean-clone verification
- macOS C3b provider gate
- package-boundary и security greps
- release checksum verification

## Артефакт

```text
File: dist/telecli
Platform: macOS arm64
Version: v0.1.0-rc1
Commit: <COMMIT>
Built: <UTC_TIMESTAMP>
SHA-256: <SHA256>
```

## Ручной smoke

Перед публикацией финального `v0.1.0` подтвердить:

- direct authorization, chat open, send и clean `Esc` exit;
- durable Keychain creation, `outbox.db`, queued state и Dispatcher delivery;
- restart queued/retryable entry;
- fail-closed reopen существующей базы после удаления test key;
- отсутствие direct fallback;
- корректный shutdown sampler, Store и Telegram session.
