# telecli v0.1.0-rc1

## Статус релиза

- Автоматические gates: PASS
- Clean-clone gate: PASS
- macOS Keychain provider C3b: GREEN
- Linux Secret Service provider C3c: NOT VERIFIED в рамках этого релиза
- Direct interactive smoke: NOT VERIFIED / PASS после ручной проверки
- Durable interactive smoke на macOS: NOT VERIFIED / PASS после ручной проверки
- Fail-closed Keychain smoke: NOT VERIFIED / PASS после ручной проверки

Три ручных сценария не входят в автоматический gate. До их фактического
выполнения и фиксации в отчёте они остаются NOT VERIFIED, и релиз не
должен заявлять обратное.

## Основные изменения

### Настройка без ручного редактирования конфигурации

`telecli configure` проводит оператора через настройку TDLib, Telegram
приложения, аккаунта, режима доставки и хранилища, затем проверяет
результат и только после этого записывает конфигурацию.

- `telecli configure` — интерактивная настройка;
- `telecli configure status` — безопасный статус без значений секретов;
- `telecli configure reset` — удаляет конфигурацию и credential profile,
  не трогая durable-базу и её ключ.

Особенности реализации:

- API hash вводится без отображения и никогда не передаётся аргументом
  командной строки, поэтому не попадает в историю оболочки и список
  процессов;
- API hash и номер телефона хранятся в macOS Keychain, а не в конфигурации;
- конфигурация записывается атомарно с правами 0600, каталоги создаются
  с правами 0700;
- если подходящий TDLib не найден в стандартных местах, setup предлагает
  указать путь вручную;
- существующий credential profile никогда не перезаписывается без
  подтверждения, а неудачная запись конфигурации откатывает профиль.

### Обнаружение конфигурации

Конфигурация читается из `--config`, затем из `TELECLI_CONFIG`, затем из
пути, возвращаемого `os.UserConfigDir()/telecli/config.toml`. Явно заданный
путь и `TELECLI_CONFIG` должны существовать: молчаливый переход к значениям
по умолчанию скрыл бы опечатку. Отсутствующий файл по умолчанию означает
первый запуск, повреждённый файл по умолчанию является ошибкой.

### Credential profiles в macOS Keychain

Профиль Telegram хранится одним generic-password item: service `telecli-auth`,
account равно имени профиля, внутри — версионированный конверт. API ID
остаётся в конфигурации, поскольку не является секретом.

Резолвер выбирает источник целиком: либо все три переменные окружения, либо
API ID из конфигурации плюс hash и телефон из одного профиля. Смешивание
источников невозможно. Частично заданное окружение даёт ошибку, а не
тихий переход в mock-only: единственное состояние, допускающее mock-режим,
это полное отсутствие любой авторизации.

### Дедупликация инициализации TDLib

TDLib сообщает состояние `waitTdlibParameters` дважды: прямым ответом на
`getAuthorizationState` и push-обновлением. Автомат авторизации теперь
запоминает, что запрос параметров уже порождён, и на повторном состоянии
возвращает no-op. Ранее второй запрос отклонялся с
`Unexpected setTdlibParameters`.

### Нативный TDLib loader в артефакте

macOS-артефакт собирается с `CGO_ENABLED=1` и содержит native loader.
`release-macos.sh` после сборки metadata требует `TELECLI_TDLIB_LIBRARY`,
запускает `doctor` на собранном бинарнике и прерывает релиз, если TDLib не
загрузился или отклонил проверку совместимости. TDLib собирается на
pinned commit `ea97bcdd3a15523c58ddfe772b4547187cf5bbeb`.

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

### Устранение утечки credential в вывод TDLib

TDLib по умолчанию пишет логи на уровне 5 и печатает каждый входящий
запрос, включая `api_hash` внутри `setTdlibParameters`. Production startup
теперь синхронно выполняет `setLogVerbosityLevel` с
`new_verbosity_level = 1` до создания authorization client, то есть до
любого запроса с credential. Ошибка или пустой ответ на этой настройке
останавливают startup, авторизация не запускается.

Уровень 1 сохраняет ошибки TDLib и отбрасывает подробные дампы запросов.
Уровни 2 и выше для production запрещены: ручной прогон показал, что на
них credential попадает в вывод.

Проверки прикрыты тестами на порядок вызовов, fail-closed при ошибке и
пустом ответе, а также на отсутствие credential в перехваченных
stdout и stderr.

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

Три сценария выполняются в реальном TUI и не входят в автоматический gate:

- **Direct interactive smoke** — авторизация, загрузка чатов, открытие
  чата, единственная отправка без дубликата, отсутствие Delivery-блока,
  чистый выход по `Esc`, отсутствие credential и `Unexpected
  setTdlibParameters` в выводе.
- **Durable interactive smoke** — создание Keychain item и `outbox.db`,
  состояние `○ В очереди`, доставка Dispatcher одной попыткой, обновление
  статуса, restart по существующей базе без повторной отправки.
- **Fail-closed Keychain smoke** — после удаления test key startup
  отклоняется с `outbox: inconsistent initialization`, replacement key не
  создаётся, существующая база не изменяется.

Полный отчёт прикрепляется к релизу отдельным артефактом. Пока сценарии
не выполнены, статус релиза остаётся NOT VERIFIED.

## Release hygiene

`release-macos.sh` удаляет только те файлы в `dist/`, которые сам
генерирует: бинарник, checksum, metadata и release notes. Ручной
smoke-report, перехваченные логи и любые другие файлы оператора
сохраняются, а перед сборкой и после неё checksum отчёта сверяется.

Перед очисткой проверяется, что `DIST_DIR` не пуст, не является корнем
файловой системы, домашним каталогом или корнем репозитория.
