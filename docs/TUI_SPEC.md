# telecli TUI — сводная спецификация

Единый source of truth для интерфейса. Раздел «Решения и сверка с кодом»
ниже приоритетнее основного текста: там, где они расходятся, действует
он.

## Решения и сверка с кодом

Сверено с `main` на 2026-09-27.

### Принятые решения

1. **Прямой отправки нет.** §7.2 действует: если durable outbox
   недоступен, сообщение не отправляется, черновик остаётся, в статусе —
   `Sending paused` (тексты — решение 3). Это заменяет строку ADR-0002 о том, что
   путь прямой отправки PR-07 остаётся активным. Режим `direct` убирается
   из production-пути, `durable` — единственный режим. Ошибка открытия
   outbox не роняет запуск: TUI открывается с приостановленной отправкой.
2. **Порядок работ.** Шаги 1–2 ADR-0003 (только `internal/telegram`) идут
   параллельно. Затем PR-10A. Шаги 3–4 ADR-0003 (живой список чатов и
   живые сообщения в TUI) делаются после PR-10A, уже в новом интерфейсе.
3. **Понятные тексты вместо «Durable outbox unavailable».** Там, где в
   основном тексте стоит `Durable outbox unavailable` (§4.3, §7.2, §11.2,
   §12.2, §17), интерфейс показывает:
   - строка статуса: `Sending paused`;
   - под полем ввода, при попытке отправить и в пустом чате:
     ```
     Sending is paused. Your message was not sent and is still here.
     telecli could not open its secure message queue, which keeps unsent
     messages safe if the app closes.
     ```
   - третья строка — подсказка по причине:

      | Причина | Подсказка |
      |---|---|
      | Keychain заблокирован или доступ запрещён | `Unlock your Keychain or allow telecli access, then restart telecli.` |
      | Ключа очереди нет в Keychain | `The key for your message queue is missing. Run telecli outbox reset to start a new queue.` |
      | Нет хранилища секретов (Linux без Secret Service) | `This system has no secure key storage, so messages cannot be queued safely.` |
      | У папки данных есть права group/other | `The telecli data folder is accessible to other users. Run telecli doctor to fix it.` |
      | Любая другая | `Run telecli doctor to see what went wrong.` |

      Случай «сброс начат, но не закончен» отображается той же строкой про
      отсутствующий ключ: ключ текущего `database_id` действительно
      отсутствует, и та же команда `telecli outbox reset` его дозапускает.
      Различать их на экране незачем, а ложная подсказка «Run telecli
      doctor» была бы неверной.


   - последняя строка: `Details: https://github.com/naghuale/tele/blob/main/docs/help/sending-paused.md`.
     В режиме Narrow и Short (§3.3, §3.4) её можно скрыть; подсказка по
     причине остаётся.

   Подробные объяснения для пользователя —
   [docs/help/sending-paused.md](help/sending-paused.md). Экранные тексты
   и эта страница меняются вместе. Полная причина ошибки уходит только в
   лог и в `telecli doctor`, на экран — никогда.

### Что уже сделано

- **PR-08H** в основном готов: `MessageSubmitter` и `QueueMessage` в
  composer, dispatcher lifecycle, восстановление после рестарта,
  проекция состояний доставки в TUI (`internal/tui/message_status.go`, те
  же семь состояний, что в §6), `CancelMessage`. Остаётся решение 1 выше.
- **Пагинация истории** из PR-10D готова (#11, #12), но в старой модели
  (см. расхождение 1).

### Расхождения, которые закрывает PR-10A

1. **Порядок timeline.** Сейчас история идёт от новых к старым: самое
   новое сообщение сверху, `↓`/`j` на самом старом подгружает более
   старые. §3 и §8.3 описывают хронологический порядок: новые внизу, `G`
   — к последнему сообщению. В PR-10A timeline становится
   хронологическим, подгрузка старых срабатывает на `↑`/`k` у верхнего
   края; логика склейки и дедупликации из #12 сохраняется.
2. **Esc.** Сейчас `Esc` в списке чатов завершает программу. По §8.5
   `Esc` никогда не завершает программу; выход — `Ctrl+C` и `q` в списке
   чатов (подсказка из §3.3).
3. **Shift+Enter.** Bubble Tea v1 в большинстве терминалов не отличает
   `Shift+Enter` от `Enter`. Новая строка по умолчанию — `Alt+Enter`;
   `Shift+Enter` работает там, где терминал его передаёт. Hint bar
   показывает то сочетание, которое действует.
4. **Стили.** Lip Gloss сейчас косвенная зависимость (`go.mod`), в
   PROJECT_FACTS «Styling library: none yet». PR-10A делает её прямой.

### Закрытые расхождения

1. **Хронологическая лента — закрыто (#50).** Расхождение 1 выше
   закрыто: страница истории приходит от TDLib новыми сверху, модель
   разворачивает её там, где она приходит (`chronological` в
   `internal/tui/model.go`), и `Chat.Messages` хранится старыми
   сверху. На экране самое старое сообщение — на первой строке ленты,
   самое новое — на последней, прямо над полем ввода, `G` идёт к
   самому новому, а `↑`/`k` у верхнего края подгружает более старые.
   Склейка и дедупликация #12 не изменились. Свои сообщения, которые
   ещё в очереди, стоят после самого нового подтверждённого: они новее
   его по времени. Проверка порядка —
   `TestTheFeedIsInTheOrderOfTime`, и золотые снимки рисуются из
   страницы в том порядке, в каком TDLib её отдаёт.

2. **Текст из Telegram очищается один раз на границе (#53).** Название
   чата, превью, текст сообщения, подпись, слово медиа и имя автора
   приходят из TDLib, а терминал исполняет управляющие символы: `\r`
   возвращает курсор в начало строки, `\v` и `\f` ведут вниз без
   возврата, escape-последовательность очищает экран или кладёт строку в
   буфер обмена, а bidi-управляющие переставляют слова. Ни одну из этих
   вещей интерфейс не просит, и любой собеседник или канал может прислать
   текст, который их вызовет.

   Поэтому очистка одна и стоит на границе «данные → модель»
   (`internal/tui/screen_text.go`): уходят остальные C0, DEL, остальные
   C1, escape-последовательности целиком — CSI, OSC, DCS, APC, PM, SOS
   вместе с их содержимым, в семи- и восьмибитной форме, — и
   bidi-управляющие U+202A–U+202E и U+2066–U+2069. Видимые символы,
   эмодзи с ZWJ-последовательностями, флаги и комбинирующие знаки не
   трогаются.

   Перевод строки остаётся переводом строки. Кроме `\n` это `\r`, `\v`,
   `\f`, NEL (U+0085) и U+2028/U+2029: в тексте, откуда они пришли, это
   переводы строки, и если их убрать без замены, слова по краям
   склеятся. Серия стоящих рядом переводов — это один перевод, поэтому
   `CRLF` — одна строка, а не две. Остальные управляющие символы, в том
   и backspace, уходят без замены.

   Перевод строки в сообщении — это перенос строки в ленте, а в
   однострочных местах (строка чата, превью, заголовок разговора, имя
   автора) — пробел. Проверки —
   `TestScreenTextRemovesWhatATerminalWouldActOn`,
   `TestChatListOfUntrustedNamesDrawsTheScreenItWasGiven`,
   `TestFeedOfUntrustedMessagesDrawsTheScreenItWasGiven` и золотой снимок
   `TestSnapshotUntrustedNames`.

   Второй половиной той же задачи стало `media_album_id`: TDLib пишет
   64-битные числа строкой, и страница истории переставала разбираться
   целиком. Все числа TDLib читает один тип (`internal/telegram/tdint.go`),
   а одно неразборчивое сообщение теперь пропускается, а не роняет
   страницу.

### Вне этой спецификации

§25 п. 1–2 (packaged TDLib, подписанный архив, Homebrew) — отдельная
работа, не TUI.

### Разбиение PR-10A на задачи

Каждый пункт — отдельный issue и PR, по порядку:

1. **10A.1 Theme engine.** Palette, Tokens, три тёмных пресета, профили
   цвета Lip Gloss (True Color, ANSI-256, ANSI-16, No Color), `[tui]
   theme` и `color` в конфиге. Экраны не меняются. Тесты §21 «Theme
   contract» и «Fallback».
2. **10A.2 Layout и фокус.** Borderless layout, breakpoints §10, одна
   фокусированная область, иерархия `Esc`, hint bar, хронологический
   timeline (расхождения 1–2). Тесты «Borderless invariant» и «Layout».
3. **10A.3 Composer.** Многострочный ввод (расхождение 3), высота §4.5,
   `Ctrl+W`, пустой ввод §7.3, inline error при ошибке enqueue с safe
   reason §7.2.
4. **10A.4 Delivery и status block.** Отрисовка §6 (символ + текст +
   токен), Retrying без мерцания §6.3, status block §11 с приоритетами и
   safe reasons, пустые состояния §17, loading §18.
5. **10A.5 Action sheet.** §13, modal подтверждения для uncertain §12.3.
6. **10A.6 Поиск чатов.** §9.
7. **10A.7 Snapshot-тесты** §21 «Snapshot» для всех экранов и тем.

До 10A.2 отдельной задачей идёт решение 1 (убрать `direct` из
production-пути).

---

## 0. Назначение

Основной экран telecli — компактный messenger, а не терминальная таблица. Пользователь должен:

- видеть список чатов;
- открывать разговор;
- читать историю;
- набирать и отправлять сообщения;
- видеть состояние доставки каждого исходящего;
- понимать состояние Telegram-сессии и durable outbox;
- отменять сообщение, пока это разрешено состоянием;
- безопасно обрабатывать `failed` и `uncertain`;
- работать в широком, среднем и узком терминале.

## 1. Ключевая формула интерфейса

```text
структура   → поверхности
фокус       → одна акцентная сторона
состояние   → цвет + символ + текст
градиент    → необязательное украшение
рамки       → отсутствуют
```

Никаких полных ASCII/Unicode-рамок. Никаких вертикальных разделителей на всю высоту. Никаких соединённых линий.

## 2. Архитектура темы

### 2.1 Два уровня абстракции

1. **Palette** — сырые цвета (`Base`, `Mantle`, `Surface0`, `Accent`, `Error`…).
2. **Tokens** — семантические роли интерфейса (`ComposerBackground`, `Focus`, `SecondaryText`…).

Компоненты используют **только tokens**. Никогда `palette.Surface1` или `palette.Mauve`. Это позволяет заменить Catppuccin на Tokyo Night без правок компонентов.

### 2.2 Palette

```go
type Palette struct {
    Base     Color
    Mantle   Color
    Crust    Color

    Surface0 Color
    Surface1 Color
    Surface2 Color

    Overlay0 Color
    Overlay1 Color
    Overlay2 Color

    Text      Color
    Subtext0  Color
    Subtext1  Color

    Accent    Color
    AccentAlt Color

    Success Color
    Warning Color
    Error   Color
    Info    Color

    Link    Color
    Mention Color
    Code    Color
}
```

### 2.3 Tokens

```go
type Tokens struct {
    AppBackground      Color
    SidebarBackground  Color
    ChatBackground     Color
    ComposerBackground Color
    PopupBackground    Color
    ShadowBackground   Color
    FooterBackground   Color

    PrimaryText   Color
    SecondaryText Color
    MutedText     Color
    DisabledText  Color

    Focus        Color
    FocusAlt     Color
    Selected     Color
    Unread       Color

    StatusInfo      Color
    StatusActive    Color
    StatusSuccess   Color
    StatusWarning   Color
    StatusError     Color
    StatusUncertain Color
    StatusCanceled  Color

    IncomingMessage Color
    OutgoingMessage Color
    CodeBackground  Color
    Cursor          Color
    Selection       Color
}
```

### 2.4 Модель темы

```go
type Theme struct {
    Name      string
    Mode      ThemeMode
    Palette   Palette
    Tokens    Tokens
    Gradients Gradients
}

type ThemeMode string

const (
    ThemeModeDark  ThemeMode = "dark"
    ThemeModeLight ThemeMode = "light"
)

type Gradients struct {
    Focus    []Color
    Accent   []Color
    Progress []Color
}
```

`Typography`, `Icons`, `Breakpoints`, `Spacing` — **не в PR-10A**. Отдельными PR, когда появится реальная необходимость.

### 2.5 Пользовательский TOML

На первом этапе пользователь задаёт только `palette`, tokens вычисляет theme engine.

```toml
[tui]
theme = "catppuccin-mocha"
color = "auto"
nerd_font = false

[theme.palette]
base        = "#1e1e2e"
mantle      = "#181825"
crust       = "#11111b"
surface0    = "#313244"
surface1    = "#45475a"
surface2    = "#585b70"
overlay0    = "#6c7086"
overlay1    = "#7f849c"
overlay2    = "#9399b2"
text        = "#cdd6f4"
subtext0    = "#a6adc8"
subtext1    = "#bac2de"
accent      = "#cba6f7"
accent_alt  = "#89b4fa"
success     = "#a6e3a1"
warning     = "#f9e2af"
error       = "#f38ba8"
info        = "#89dceb"
link        = "#89b4fa"
mention     = "#f5c2e7"
code        = "#fab387"
```

### 2.6 Пресеты

В `PR-10A` — **ровно три тёмные темы**:

- `catppuccin-mocha`
- `tokyo-night-storm`
- `gruvbox-dark`

Остальные тёмные — `PR-10C`. Одна светлая (Latte или Solarized Light) — отдельный compatibility gate.

12 тем сразу — snapshot-ад, отладка контраста и маскировка ошибок в semantic roles.

### 2.7 Fallback-профили

Использовать профили Lip Gloss, а не вручную `COLORTERM=truecolor`.

| Профиль | Палитра | Градиенты | Тени |
|---|---|---|---|
| True Color | полная | ограниченные | да |
| ANSI-256 | индексированные | максимум 3 ступени | нет |
| ANSI-16 | semantic ANSI | нет | нет |
| No Color / `NO_COLOR` / `--no-color` / `TERM=dumb` | символы, bold/reverse, отступы | нет | нет |

Фокус обязан оставаться видимым во всех режимах.

```text
focused   ▌ или >
selected  reverse или bold
status    символ + текст
```

Статус никогда не зависит только от цвета.

## 3. Визуальный макет

### 3.1 Wide (≥ 100 колонок)

```text
 Chats                  Anna
 Search chats           Online · Connected · 2 queued

▌Anna                    Anna                         14:28
 ● 2                     Привет, как дела?

 Dev Team                You                            14:30
    Build passed         Проверяю новую сборку.
                                                        ✓ Sent

 Release                 You                            14:31
    RC checklist         Отправляю результаты проверки.
                                                        ↻ Retrying in 12s

                         You                            14:32
                         Важное сообщение
                                                        ? Delivery uncertain
                                                        Message may already
                                                        have been sent

                         ▌ Write a message…

                         Enter send · Shift+Enter newline
                         Tab focus · Esc cancel · / search
```

Между панелями — разница фона, отступ, тень, акцентная линия фокуса. Полной вертикальной линии нет.

### 3.2 Medium (72–99)

Скрываются: preview второго ряда, подробные timestamps, дополнительные индикаторы, длинные hints.

```text
 Chats         Anna
               Connected · 2 queued

▌Anna          Anna                 14:28
 ● 2           Привет, как дела?

 Dev Team      You                    14:30
               Проверяю сборку.
               ✓ Sent

 Release       You                    14:31
               Отправляю результаты.
               ↻ Retrying

               ▌ Write a message…

               Enter send · Esc cancel
```

### 3.3 Narrow (< 72)

Одновременно — только одна основная область.

**Chat list:**

```text
 Chats
 Connected · 2 queued

 Search chats

▌Anna
 ● 2  Привет, как дела?

 Dev Team
 Build passed

 Release
 RC checklist

 Enter open · / search · q quit
```

**Conversation:**

```text
‹ Chats

 Anna
 Online · Connected

 Anna  14:28
 Привет, как дела?

 You  14:30
 Проверяю новую сборку.
 ✓ Sent

 You  14:31
 Отправляю результаты.
 ↻ Retrying in 12s

▌Write a message…

 Enter send · Esc back
```

В узком режиме список чатов **не сжимается слева**. Это ухудшает читаемость.

### 3.4 Short (height < 20)

Скрываются: второстепенные hints, preview в списке, дополнительная строка status block, расширенное explanation delivery state.

Composer и текущее сообщение остаются видимыми.

Правила:

```text
height < 10  → composer = 1 строка, без hints
height < 6   → только composer, timeline скрыт
```

## 4. Логические области экрана

Основной экран состоит из шести областей:

1. chat list header
2. chat list
3. conversation header
4. message timeline
5. composer
6. hint/status area

Ни одна не обводится полной рамкой.

### 4.1 Chat list header

```text
Chats
/ Search · 3 unread
```

### 4.2 Chat list

Строка чата: название, preview, timestamp, unread count, muted, pinned.

При нехватке места скрываются в порядке:

1. дополнительные иконки;
2. timestamp;
3. preview;
4. unread count сохраняется максимально долго;
5. название не скрывается.

Правила unread badge:

```text
1–99    → число
100+    → 99+
muted   → ● без числа или muted цветом
```

Строка занимает три строки: название с timestamp, preview с badge, пустая. Выбранный чат — карточка: вместо пустой строки снизу и пустой строки сверху у него по половинке воздуха, `▄` сверху и `▀` снизу, цветом `Selected` на фоне списка; имя и badge живут внутри неё.

```text
▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄
    Anna            14:28
     ● 2  Привет, как дела?
  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀
```

- **воздух** — по 2 колонки слева и справа *внутри* карточки, на фоне списка: выделение, идущее во всю ширину панели, — это полоса, которой у списка больше нет;
- **время и badge** заканчиваются в одной и той же колонке, а preview обрезается как минимум за 2 колонки до badge, чтобы они не слипались;
- **badge** — ` 2 `, то есть с пробелом с каждой стороны: счёт внутри своей подложки. При `[tui] nerd_font = true` он рисуется теми же полукружьями (`U+E0B6`/`U+E0B4`), что и пилюля сообщения;
- **полукружья на самой карточке не рисуются** — у неё две строки слов, а полукружье высотой в одну строку.

У невыбранных строк половинок воздуха нет вовсе: они остаются тремя строками, как были. `▄` и `▀` — глифы, а не пятна цвета, и глиф без явного цвета рисуется тем цветом, который у терминала случается, — на тёмной теме это светлая полоса под каждым чатом списка. Плюс список показывает на четверть больше чатов, чем если бы карточка была у каждой строки. Высоты строк в списке поэтому разные, и окно списка считается по высотам нарисованных строк, а не делением бюджета на три.

Обозначение выбора: акцентная линия слева, выделенный фон карточки, основной цвет названия, увеличенная яркость unread indicator.

### 4.3 Conversation header

```text
Anna
Online · Connected · 2 queued · 1 retrying
```

`presence`, `connection`, `outbox summary` — независимы. При ошибке одного остальные не исчезают.

При ошибке:

```text
Anna
Durable outbox unavailable
```

При восстановлении:

```text
Anna
Reconnecting…
```

Status block — не более двух строк в обычном режиме.

### 4.4 Message timeline

Каждое сообщение — в своём блоке, со своей стороны ленты, шириной со свой
текст. Оба блока есть, оба округляются по настройке, и различаются они и
стороной, и цветом.

```text
░░░░░░░░░░░░░░░░░░░░░░░░░░
 ░░ Anna Example · 14:28
 ░░ Привет, как дела?
 ░░░░░░░░░░░░░░░░░░░░░░░░░░
                    ░░░░░░░░░░░░░░
```

```text
                    ░░░░░░░░░░░░░░░
                    ░░ Проверяю новую сборку.
                    ░░              ✓ Sent 14:30
                    ░░░░░░░░░░░░░░░
```

`▄` и `▀` — те же цвета, что фон блока, на фоне ленты, и по 1 колонке на
каждый столбец блока.

`░` — клетка фона блока; рамки и линии вокруг блока не рисуются (§1).

Различия:

- **входящие** — блок у левого поля, фон `ComposerBackground`
  (`Surface0`), нейтральный серый;
- **исходящие** — блок у правого поля, фон `OutgoingBubble`: та же
  `Surface0`, подмешанная к акценту темы. Свои сообщения — те, что в
  цвете темы, как в Telegram;
- **поля ленты** — 3 колонки слева и справа (в Narrow — 1);
- **ширина блока** — `max(16, ширина строк текста, ширина строки состояния,
  ширина строки «автор · время»)` + внутренние отступы 2+2 (в Narrow — те же
  2+2), не больше 70 % ширины ленты (в Narrow блок может занять её всю);
  длинный текст переносится внутри. Минимум 16 колонок текста — это
  `bubbleMinTextColumns`: слово в блоке шириной в шесть колонок — не
  сообщение, а полоска, и пилюля из §4.4.1 в такой ширине не помещается;
- **воздух** — 2 колонки слева и справа внутри блока: текст не прилипает к
  фону блока; у входящих текст прижат влево, у исходящих строка состояния —
  вправо;
- **воздух вокруг блока** — по половинке строки сверху (`▄`) и снизу (`▀`),
  цветом фона блока на фоне ленты. Он и есть скругление у блока длиннее одной
  строки, и поэтому он не рисуется на экране ниже §3.4 `Short`: там
  половинка — это две строки из двадцати, которые нужны разговору;
- **имя `You`** не рисуется: сторона блока и его цвет и есть признак;
- **состояние доставки** — под текстом, внутри блока, у его правого края;
- **фон** — только у блоков и у сообщения под курсором (`Selected`, на
  клетках блока); строки ленты вне блоков — фон ленты (`ChatBackground`),
  без полос;
- **полная рамка или ASCII bubble не используется**.

### 4.4.1 Роль `OutgoingBubble`

Фон блока сообщения этого пользователя — отдельная роль темы, а не
`ComposerBackground`:

- значение — смесь `Surface0` и акцента: каждый его канал строго между
  ними, и `TestTheMessageBubblesAreMixesOfTheSurfaceAndTheAccent` это
  проверяет;
- доля смешения мала, потому что слова своего сообщения рисуются
  акцентом, а поверхность, смешанная к акценту, — это поверхность, на
  которой у акцента меньше контраста. Каждое значение в палитрах — наибольшая
  доля, которая держит слова сообщения на WCAG AA 4.5:1
  (`TestTheWordsOfAMessageAreReadableOnItsOwnBubble`), а у входящих слова
  держатся на `Surface0` тем же тестом;
- в ANSI256 и ANSI16 индексы выписаны явно, и на 16-цветном терминале роль
  **не** обнуляется, как остальные поверхности: это единственное, что
  отличает стороны цветом там, где остаётся только положение.

Скруглённые края — по настройке `[tui] nerd_font = true` (по умолчанию
`false`): слева и справа от строки блока рисуется по одному полукруглому
краю из шрифта Nerd Font (`U+E0B6` и `U+E0B4`) цветом фона блока на фоне
ленты, по 1 колонке каждый, и они считаются в ширине блока. **У обеих
сторон.** Без настройки и в профиле `No Color` — прямые углы и никакого
фона: терминал не сообщает свой шрифт, поэтому программа не может угадать
(см. §2.7, §14).

**Только однострочный блок.** Полукружье — высотой в одну строку, и на
блоке из двух строк оно даёт две пилюли одна над другой: два сообщения
вместо одного. Поэтому:

- блок **из одной строки слов** — пилюля: полукруглые края на этой строке;
- блок **из нескольких строк** — прямоугольник: края прямые, а скругление
  несут половинки воздуха `▄`/`▀` вокруг него (см. §4.4);
- форма считается по строкам блока целиком — строка «автор · время» и строка
  состояния тоже строки блока. Экраном, где блок бывает однострочным,
  остаётся тот, что ниже §3.4 `Short`: там у сообщения есть только текст.

Тот же принцип у badge в списке чатов (§4.2): счёт в пилюле, и при
включённой настройке — с полукружьями; у самой карточки чата, у неё две
строки, полукруглых краёв нет.

Header sticky — да. Date separators — `PR-10D`.

### 4.5 Composer

```text
▌ Write a message…
```

Фокус — одна акцентная линия слева, более светлый фон, яркий курсор, заметный placeholder. Рамок сверху, снизу и справа нет.

Многострочный:

```text
▌ Первая строка сообщения
  Вторая строка сообщения
  Третья строка сообщения
```

Высота:

- минимум: 1 строка;
- обычно: до 4 строк;
- максимум: до 30% высоты conversation view.

После превышения — прокрутка внутри.

### 4.6 Hint area

```text
Wide:    Enter send · Shift+Enter newline · Tab focus · Esc cancel · / search
Medium:  Enter send · Tab focus · Esc cancel
Narrow:  Enter send · Esc back
```

Hints зависят от **фокуса**, а не только от ширины.

## 5. Фокус

В каждый момент — ровно одна сфокусированная область:

- chat list;
- message timeline;
- composer;
- search;
- modal action sheet.

Обозначение фокуса: одна акцентная линия, цвет заголовка, оттенок фона, курсор (если редактируемая область).

Запрещено одновременно: рисовать рамку, подсвечивать все границы, использовать несколько акцентных линий, помечать несколько областей как активные.

### 5.1 Focus style

```go
func focusedStyle(background, accent lipgloss.Color) lipgloss.Style {
    return lipgloss.NewStyle().
        Background(background).
        PaddingLeft(1).
        BorderLeft(true).
        BorderStyle(lipgloss.ThickBorder()).
        BorderForeground(accent)
}
```

Запрещено: `Border(true)`, `BorderTop`, `BorderRight`, `BorderBottom`.

### 5.2 Timeline focus

Линия у каждого сообщения не рисуется. Используется: акцентный заголовок conversation, активный scrollbar, подсветка выбранного сообщения (только в message selection mode).

## 6. Delivery states

| State | UI state | Символ | Токен | Особенности |
|---|---|---|---|---|
| StateQueued | Queued | ● | StatusInfo | без анимации, разрешена отмена |
| StateDispatching | Sending… | ◐ | StatusActive | лёгкий spinner, отмена обычно недоступна |
| StateAccepted | Sending… | ◐ | StatusActive | TDLib взял, Telegram ещё не подтвердил |
| StateFailedRetryable | Retrying | ↻ | StatusWarning | relative time для коротких, absolute для длинных |
| StateSent | ✓ Sent | ✓ | StatusSuccess | terminal, подтверждено Telegram |
| StateFailedPermanent | ! Failed | ! | StatusError | без automatic retry, действие: Create new message |
| StateUncertain | ? Delivery uncertain | ? | StatusUncertain | warning о duplicate, без обычного Retry |
| StateCanceled | Canceled | ⊘ | StatusCanceled | muted, не error red |

`StateAccepted` и `StateSent` — это разные вещи, и различать их
обязательно. Ответ на `sendMessage` приходит с **временным**
идентификатором: TDLib отдаёт его раньше, чем отправлено сообщение, и
заменяет на настоящий, когда Telegram подтвердит отправку. Нарисовать
`✓ Sent` по ответу `sendMessage` — значит сообщить о доставке раньше
Telegram.

Подтверждённое сообщение (`StateSent`) перестаёт быть «ожидающим»:
оно становится обычным сообщением открытого чата, под своим настоящим
идентификатором, и остаётся в ленте при возврате в чат.

### 6.1 Проекция

```go
type DeliveryViewState uint8

const (
    DeliveryQueued DeliveryViewState = iota
    DeliverySending
    DeliveryRetrying
    DeliverySent
    DeliveryFailed
    DeliveryUncertain
    DeliveryCanceled
)
```

UI не придумывает переходы state machine. Только отображает projection из application/outbox.

### 6.2 Terminal states

```text
Sent            terminal
FailedPerm      terminal
Canceled        terminal
Queued          нетерминальное
Dispatching     нетерминальное
Accepted        нетерминальное: ждёт updateMessageSendSucceeded/Failed
Uncertain       нетерминальное до явного решения пользователя
```

`Accepted` нетерминальное **для очереди**, а не для интерфейса:
диспетчер его больше не трогает, потому что TDLib уже взял сообщение, и
повторная отправка была бы вторым сообщением. Дальше его ведёт
`updateMessageSendSucceeded` (в `Sent`) или `updateMessageSendFailed`
(в `FailedPermanent`).

### 6.3 Retrying без мерцания

Не обновлять весь экран каждую секунду. Варианты:

- обновлять только строку с этим сообщением;
- или показывать `Retry scheduled at 14:35` без обратного отсчёта.

### 6.4 Canceled и payload

```text
Canceled message: payload остаётся в timeline
                  до ручной очистки пользователем
                  не отправляется ни при каких условиях
```

## 7. Composer behavior

### 7.1 Успешный enqueue

```text
Enter
→ validate text
→ MessageSubmitter.QueueMessage
→ on success: append Queued to projection, clear composer
→ on failure: keep draft, show safe inline error
```

Уведомление dispatcher — **внутри** MessageSubmitter, не часть UI-контракта.

### 7.2 Ошибка enqueue

```text
Durable outbox unavailable.
Message was not queued. Your text is still in the composer.
```

- composer text сохраняется;
- focus остаётся в composer;
- нет direct-send fallback;
- нет MemoryStore fallback;
- текст — **safe reason**, не `err.Error()`.

### 7.3 Пустой ввод

Не отправляется, ошибка не создаётся, composer активен, placeholder кратко подсвечивается.

### 7.4 Многострочный ввод

```text
Enter                 send
Shift+Enter           newline
Alt+Enter             optional newline fallback
```

Если терминал не различает `Shift+Enter` — настройка должна позволять альтернативу.

## 8. Навигация

### 8.1 Глобальные

```text
Tab             следующий focus region
Shift+Tab       предыдущий focus region
Ctrl+C          graceful shutdown
Ctrl+L          redraw (не пишет файл)
?               открыть справку
```

### 8.2 Chat list

```text
j / Down        следующий чат
k / Up          предыдущий чат
Enter           открыть выбранный чат
/               поиск
g               первый чат
G               последний чат
```

### 8.3 Conversation

```text
j / Down        прокрутка вниз
k / Up          прокрутка вверх
PageDown        страница вниз
PageUp          страница вверх
G               перейти к последнему сообщению
Enter / i       перейти в composer
Esc             вернуться к списку в single-pane
/               поиск в чате (не в PR-10A)
```

`g` в conversation не используется.

`k / Up` у самого старого загруженного сообщения запрашивает следующую
страницу истории и продолжает запрашивать, пока лента не заполнена, —
тем же проходом, что и первая страница при открытии чата (§18.1).

Окно ленты ставится по высотам сообщений, а не по оценке (#57). Раньше
оно начиналось на «страницу» выше новейшего, деля строки ленты на две
строки на сообщение; с #50 входящее сообщение — это три строки (автор,
текст, пустая строка под ним), исходящее — тоже три, альбом — одна запись,
и окно начиналось примерно на треть экрана ниже, а сверху были пустые
строки: чат открывался последними двумя сообщениями в углу пустой ленты,
и ↑ приносил остальное. Теперь `anchoredAt` идёт от новейшей записи вверх,
прибавляя `len(entryLines(...))` — ту же функцию, которой рисует view, —
пока строки ленты не кончатся, а `timelineCut` хранит, сколько строк
верхней записи не показать, чтобы сработала последняя строка.

Окно ставится заново, когда разговор ведётся следом: чат открыт, отправлено
сообщение, пришла страница старых сообщений, изменился размер. Когда
читатель ушёл от конца вверх, окно остаётся там, где было: страница,
добавившая N сообщений, сдвигает курсор и якорь на N, и сообщение, которое
читают, остаётся на месте (§10.5). `timelinePageSize` — единственная
оценка, и она только для PgUp/PgDn: на сколько сообщений они двигают курсор.

### 8.4 Composer

```text
Enter           отправить
Shift+Enter     новая строка
Tab             следующий focus region
Shift+Tab       предыдущий focus region
Esc             выйти из composer без потери текста
Ctrl+U          очистить строку (readline-совместимо)
Ctrl+W          удалить слово (readline-совместимо)
```

Глобальные single-letter shortcuts отключаются во время ввода.

### 8.5 Иерархия Esc

```text
modal → search → composer → conversation (single-pane) → chat list
```

Иначе поведение `Esc` непредсказуемо.

## 9. Поиск

```text
Search chats
query text

▌Anna
  Dev Team
  Release
```

Без полной рамки. Результаты обновляются по мере ввода, match fragment подсвечивается, первый результат выбирается автоматически, `Enter` открывает, `Esc` закрывает и восстанавливает предыдущий выбор, пустой результат — спокойный empty state.

## 10. Responsive

### 10.1 Breakpoints

```text
Wide:    width >= 100
Medium:  width 72–99
Narrow:  width < 72
Short:   height < 20
```

Стартовые tokens, не жёсткое UX-обещание. Инвариант — «одна основная область при узком экране».

### 10.2 Wide

Две панели, preview сообщений, полные timestamps, полный hint bar, status block до двух строк.

### 10.3 Medium

Две панели, preview сокращён или скрыт, короткий status block, сокращённый hint bar, message metadata переносится на следующую строку.

### 10.4 Narrow

Single-pane, отдельный chat list screen, отдельный conversation screen, `Esc` возвращает назад, composer на всю ширину.

### 10.5 Возврат narrow → wide

Сохраняются: выбранный чат, scroll position в conversation, composer draft.

## 11. Status block

### 11.1 Приоритеты

1. key provider / durable outbox failure;
2. authentication failure;
3. connection failure;
4. retry/recovery activity;
5. normal connected state.

### 11.2 Примеры

```text
Connected
Connected · 2 queued · 1 retrying
Recovering interrupted messages…
Durable outbox unavailable
Message was not queued
```

### 11.3 Запрещено в status block

`TDLibError.Message`, message body, номер телефона, API hash, auth code, password, file path с чувствительными компонентами, keychain secret, stack trace.

Status block получает **safe reason**, transport-neutral.

## 12. Errors

### 12.1 Inline errors

```text
Message was not queued
Your text is still in the composer
```

### 12.2 Status errors

```text
Durable outbox unavailable
```

### 12.3 Modal confirmation

Только для рискованных действий:

```text
Send a new copy?

The previous delivery is uncertain and may already have succeeded.
Sending again can create a duplicate.

Create new copy
Keep uncertain
Cancel
```

Modal без ASCII-рамки. Raised background, shadow, внутренний padding.

## 13. Message actions (action sheet)

```text
Queued
Cancel message
Copy text
Close

Sending
Copy text
Close

Retrying
Cancel message
Copy text
Close

Failed
Create new message
Copy text
Close

Uncertain
Create a new message
Copy text
Keep as uncertain
Cancel record
Close

Sent
Copy text
Close
```

`Esc` в uncertain = `Keep as uncertain` (безопасный дефолт). Без `Retry`.

## 14. Accessibility

Интерфейс должен:

- работать без Nerd Font: символы Nerd Font (в том числе скруглённые края
  сообщений, §4.4) включаются только настройкой `[tui] nerd_font = true` и
  по умолчанию выключены;
- не полагаться только на цвет;
- иметь readable fallback для Unicode icons;
- поддерживать уменьшенную палитру;
- оставаться понятным при отключённых animations;
- иметь стабильную ширину status labels;
- корректно обрабатывать wide Unicode;
- не использовать combining characters как критические indicators.

Fallback:

```text
✓ Sent              -> Sent
↻ Retrying          -> Retrying
? Uncertain         -> Delivery uncertain
! Failed            -> Failed
● Queued            -> Queued
```

## 15. State model UI

```go
type DeliveryViewState uint8

const (
    DeliveryQueued DeliveryViewState = iota
    DeliverySending
    DeliveryRetrying
    DeliverySent
    DeliveryFailed
    DeliveryUncertain
    DeliveryCanceled
)
```

Проекция:

```text
StateQueued           -> DeliveryQueued
StateDispatching      -> DeliverySending
StateAccepted         -> DeliverySending
StateFailedRetryable  -> DeliveryRetrying
StateSent             -> DeliverySent
StateFailedPermanent  -> DeliveryFailed
StateUncertain        -> DeliveryUncertain
StateCanceled         -> DeliveryCanceled
```

## 16. Компоненты

```text
RootModel
  ChatListModel
  ConversationModel
  ComposerModel
  StatusModel
  SearchModel
  ActionSheetModel
  HelpModel (статический overlay в PR-10A)

View helpers:
renderChatListHeader
renderChatRow
renderConversationHeader
renderMessage
renderDeliveryStatus
renderComposer
renderStatusBlock
renderHintBar
renderActionSheet
renderInlineError
renderEmptyState
```

## 17. Пустые состояния

```text
No chats yet
Start a new conversation or wait for chats to load.

Select a chat
Use ↑ and ↓, then press Enter.

No messages yet
Write the first message below.

No chats found

Durable outbox unavailable
Messages cannot be queued safely.
Your draft has not been deleted.
```

## 18. Loading и recovery

```text
Loading chats…
Recovering interrupted messages…
Reconnecting…
```

Timeout для `Loading chats…`: через N секунд показать `Taking longer than expected. Check connection or press R to retry.`

Queued entries при reconnect остаются queued или retrying, **не** превращаются в failed.

### 18.1 Дозагрузка истории при открытии чата (#57)

Первая страница истории приходит из локальной базы TDLib, и на только что
открытом аккаунте это одно-два сообщения независимо от того, сколько
просили. Экран не показывает пустую ленту с собственными сообщениями
пользователя где-то за её краем, поэтому страница запрашивается заново от
самого старого сообщения предыдущей — и ещё раз, пока в ленте не будет
столько сообщений, сколько у неё строк.

Границы прохода, все три обязательны:

```text
страница пришла пустой        конец истории, дальше не идём
страница говорит HasMore=false  источнику верим, дальше не идём
страница ничего не добавила    граница не двинулась, конец истории
ошибка                         оставляем что загружено, повторит следующий ↑
запросов больше 5              стоп
сообщений больше 100 сверх     стоп
первой страницы                стоп
```

Размер считается по числу строк ленты, а не по высоте окна: ресайз не
гоняет проход. Чат, открытый повторно из списка, спрашивается заново, а не
берётся из кэша двадцати последних сообщений: `k` вверху ленты — это
вопрос про остальное.

Сколько записей страницы этот билд прочитать не смог — число, не текст:
у пользователя в переписке может не хватать сообщения, и молча пропадать
оно не должно. На экран это число не попадает (Privacy, §19), в
диагностику попадает.

Смена чата — движение выделения в списке, открытие чата, закрытие
conversation — запрашивает полную перерисовку окна того же размера
(`tea.WindowSizeMsg` текущего размера). Рендерер bubbletea пишет построчно
и пропускает строку, байты которой не изменились, поэтому строка списка,
у которой изменились байты без изменения ширины, рисуется ровно
поверх старой — от этого и удвоение (#57).

## 19. Privacy

- UI не показывает transport error text без sanitization.
- UI не показывает message payload в diagnostics.
- UI не показывает credentials и auth material.
- Status block использует transport-neutral safe reasons.
- Логи рендера не содержат message text.
- Snapshot-тесты не сохраняют реальный message text в CI artifacts.
- Crash report не содержит composer buffer.
- `Ctrl+L` dump не пишет файл.
- Текст из Telegram очищается от управляющих символов, escape-последовательностей
  и bidi-управляющих на границе «данные → модель» (#53), а не в каждом виде.

## 20. Acceptance criteria

### Visual

- [ ] Нет полных ASCII-рамок вокруг панелей.
- [ ] Нет `+---+`, `|`, `┌─┐` как layout boundaries.
- [ ] Фокус — максимум одна акцентная линия.
- [ ] Chat list и conversation разделяются фоном и padding.
- [ ] Composer без полной рамки.
- [ ] Интерфейс читаем без цветов.
- [ ] Catppuccin и Tokyo Night используют одну token model.

### Focus

- [ ] Ровно одна сфокусированная область в любой момент.
- [ ] `Esc` имеет детерминированный порядок.
- [ ] Focus line видна при `NO_COLOR`.

### Responsive

- [ ] При width < narrow — single-pane.
- [ ] Chat list не сжимается до нечитаемой полосы.
- [ ] Composer всегда доступен.
- [ ] Hint bar сокращается.
- [ ] Длинные status labels корректно переносятся.
- [ ] При возврате narrow → wide сохраняются чат, scroll, draft.
- [ ] При height < минимума composer остаётся видимым.

### Composer

- [ ] Blank payload не отправляется.
- [ ] Meaningful whitespace сохраняется verbatim.
- [ ] Composer очищается только после успешного durable enqueue.
- [ ] При ошибке enqueue текст остаётся.
- [ ] Нет silent direct-send fallback.
- [ ] Нет MemoryStore production fallback.
- [ ] Dispatcher notification не входит в UI-контракт.
- [ ] Placeholder объясняет Enter, если hints скрыты.

### Delivery

- [ ] Каждый outbox state имеет текстовый label.
- [ ] `uncertain` содержит warning о duplicate.
- [ ] `failed_permanent` не получает automatic retry.
- [ ] `accepted` показывается как `Sending`, а не `Sent`: это ответ
      TDLib, а не подтверждение Telegram.
- [ ] `sent` показывается как `Sent`.
- [ ] `accepted`-запись предыдущего процесса разрешается при старте:
      найдена в чате — `sent`, не найдена — `uncertain`, никогда не
      остаётся `accepted`.
- [ ] Поиск идёт по страницам истории назад, а не по одной странице, и
      останавливается, когда все записи чата найдены или бюджет страниц
      исчерпан.
- [ ] `uncertain`, помеченный этим же поиском, проверяется заново при
      следующем запуске; `uncertain` из-за потерянной связи — нет.
- [ ] Chat подтверждений доступен в `telecli doctor` числами: seen,
      matched, named no record, window gaps.
- [ ] Delivery poll вооружается при открытии чата, а не при старте
      программы: на старте чата нет, и команда, которая бы его
      вооружила, не отвечает ничем.
- [ ] Отправка Enter читает очередь сразу и перерисовывает экран.
- [ ] Отправленное сообщение ставит окно на себя: строка встаёт в конец
      разговора, курсор и окно — на неё. Строка, добавленная в уже полную
      ленту, иначе уходит за нижний край экрана.
- [ ] Изменение формы ленты (строка ушла из середины, сообщение доставлено
      в переписку) переставляет окно. Окно ставится проходом по строкам
      выше него; если лента изменилась, проход больше не про той форме, и
      низ ленты не добирает строк — пустые строки появляются СВЕРХУ.
- [ ] Подтверждённое сообщение попадает в переписку под финальным
      номером ДО того, как покинет список ожидающих: очередь перестаёт
      его перечислять сразу после `sent`.
- [ ] Открытие чата перечитывает его новейшую страницу и сливает её с
      кэшем, а не заменяет кэш.
- [ ] `canceled` — не critical error.
- [ ] `dispatching` показывается как `Sending`.
- [ ] Pending-сообщения — часть ленты: та же колонка, та же прокрутка,
      те же высоты; очередь не вытесняет историю.
- [ ] Строка очереди стоит на своём месте в разговоре, а не в конце
      ленты: по времени, когда Telegram датировал сообщение (строка
      очереди — по времени, когда очередь её записала). Строка вчерашнего
      `uncertain` рисуется между вчерашними сообщениями, а не под
      сегодняшними.
- [ ] Пока работает интерфейс, в терминал пишет только рендерер: ни одна
      строка журнала не сдвигает экран.
- [ ] Причины, которые нельзя показать, пишутся в `telecli.log` (0600,
      ротация) рядом с очередью; `telecli doctor` печатает его путь.
- [ ] Компонент без логгера молчит, а не берёт process-wide default.
- [ ] Queued не превращается в Failed при временном disconnect.
- [ ] Sent / FailedPermanent / Canceled — terminal states.
- [ ] Uncertain требует явного решения.

### Privacy

- [ ] UI не показывает transport error text без sanitization.
- [ ] UI не показывает message payload в diagnostics.
- [ ] UI не показывает credentials.
- [ ] Status block использует safe reasons.
- [ ] Логи рендера не содержат message text.
- [ ] Snapshot-тесты не сохраняют реальный message text.
- [ ] Crash report не содержит composer buffer.
- [ ] `Ctrl+L` dump не пишет файл.
- [ ] Список чатов и лента не содержат ни одного управляющего символа, escape-последовательности или bidi-управляющего из текста Telegram (#53).
- [ ] `\r`, `\v`, `\f` и NEL остаются переводом строки, серия стоящих рядом переводов — это один перевод, а остальные управляющие символы уходят без замены.
- [ ] Перевод строки в сообщении — перенос в ленте и пробел в однострочных местах; эмодзи, флаги и нелатинские шрифты проходят без изменений.

### Lifecycle

- [ ] После restart entries снова появляются в timeline.
- [ ] Expired dispatching после recovery — uncertain.
- [ ] Dispatcher shutdown не удаляет queued entries.
- [ ] Key provider failure не создаёт новый memory queue.
- [ ] Existing encrypted DB без ключа — outbox unavailable.

## 21. Тесты

### Theme contract

```text
TestEveryBuiltInThemeHasAllSemanticRoles
TestEveryBuiltInThemeHasUniqueName
TestThemeRejectsUnknownName
TestThemeDefaultsToCatppuccinMocha
TestThemeDoesNotMutateGlobalStyles
```

### Borderless invariant

```text
TestChatLayoutContainsNoFullBorders
TestSidebarContainsNoRightBorder
TestComposerUsesOnlyLeftFocusBorder
TestDeliveryUsesOnlyLeftFocusBorder
TestPopupUsesBackgroundAndShadowWithoutFrame
TestOnlyFocusedRegionHasAccentLine
```

### Fallback

```text
TestTrueColorUsesRGBTokens
TestANSI256UsesIndexedFallback
TestANSI16UsesBasicFallback
TestNoColorPreservesFocusIndicator
TestNoColorPreservesStatusMeaning
TestDumbTerminalDisablesGradients
```

### Layout

```text
TestLayoutAtMinimumSupportedWidth
TestLayoutAtWideWidth
TestLayoutWithCyrillic
TestLayoutWithEmoji
TestLayoutWithWideUnicode
TestResizeDoesNotLeaveBorderFragments
TestLongChatTitleDoesNotShiftComposer
```

### Status semantics

```text
TestQueuedUsesSymbolAndText
TestRetryingUsesSymbolAndText
TestFailedUsesSymbolAndText
TestUncertainUsesWarningText
TestStatusNeverDependsOnlyOnColor
```

### Snapshot

```text
TestSnapshotWideChatListAndConversation
TestSnapshotMediumChatListAndConversation
TestSnapshotNarrowChatList
TestSnapshotNarrowConversation
TestSnapshotDeliveryQueued
TestSnapshotDeliverySending
TestSnapshotDeliveryRetrying
TestSnapshotDeliverySent
TestSnapshotDeliveryFailed
TestSnapshotDeliveryUncertain
TestSnapshotDeliveryCanceled
TestSnapshotActionSheetUncertain
TestSnapshotEnqueueErrorKeepsDraft
TestSnapshotThemeCatppuccin
TestSnapshotThemeTokyoNight
TestSnapshotNoColor
TestSnapshotANSI256
TestSnapshotANSI16
TestSnapshotUntrustedNames
```

### Text from Telegram (#53)

```text
TestScreenTextRemovesWhatATerminalWouldActOn
TestScreenLineFoldsEveryLineBreakIntoASpace
TestScreenTextKeepsWhatAPersonWrote
TestTheCleanerCoversEveryStringOfBothProjections
TestChatListOfUntrustedNamesDrawsTheScreenItWasGiven
TestFeedOfUntrustedMessagesDrawsTheScreenItWasGiven
```

### История и дозагрузка (#57)

```text
TestGetChatHistoryHasMoreWhenTheAnswerWasNotEmpty
TestGetChatHistoryReportsMoreWhenTheAnswerWasNotEmpty
TestTheBoundaryCrossesAnEntryThatCouldNotBeRead
TestAPageThatCouldNotBeReadWholeStillMovesTheBoundary
TestThePageCountsTheEntriesItCouldNotRead
TestTelegramChatServiceCarriesTheUnreadableCount
TestAChatThatAnswersOneMessageAtATimeStillFillsTheFeed
TestAPageThatFillsTheFeedStopsTheRepeat
TestAFeedIsFullOfTheMessagesThatWereThere
TestAChatWithNoHistoryStopsAtTheFirstEmptyPage
TestASourceThatNeverSaysNoCostsAFixedNumberOfRequests
TestTheRepeatIsBoundedByMessagesAsWellAsByRequests
TestScrollingUpAtTheTopFillsTheFeedTheSameWay
TestAHistoryPageSaysHowManyEntriesItCouldNotRead
TestPageThatSaysThereIsNoMoreStopsTheLoading
TestPageThatSaysThereIsMoreKeepsTheLoading
```

### Окно ленты (#57)

Окно ставится по высотам, которые рисует view, а не по оценке: 40 сообщений
разных видов (входящее, исходящее, две строки текста, альбом) в 120×40,
открытый на новейшем, заполняет ленту до верхней строки; новейшее сообщение —
на последней; после подгрузки старой страницы нарисованные строки не
съезжают; после resize окно заполняется заново, если разговор ведётся следом;
событие `TestSnapshotFullFeedFromTheNewest` — золотой экран этого.

```text
TestAChatOpensWithTheFeedFullToItsTopRow
TestTheAreaOfTheMessagesIsFullToItsFirstRow
TestAConversationShorterThanTheFeedSitsAboveTheComposer
TestAChatWhoseHistoryArrivesInPagesEndsWithTheFeedFull
TestAnOlderPageDoesNotMoveTheWindowOfAReader
TestAResizeFillsTheWindowOfAConversationThatIsBeingFollowed
TestSnapshotFullFeedFromTheNewest
```

### Перерисовка экрана (#57)

Тест идёт через настоящий `tea.Program` в альт-экране, вывод которого —
эмулятор терминала в памяти из этого же пакета: программа пишет escape-
последовательности в `io.Writer`, а тест читает получившуюся сетку ячеек.
`github.com/charmbracelet/x/vt` в дереве зависимостей нет, поэтому
эмулятор — минимальный и в тестовых файлах.

Ожидание кадра — по кадру, а не по времени. Рендерер рисует по своему
тамеру (60 fps), поэтому «экран равен тому, что нарисовала программа» —
единственное, что можно знать снаружи; кадр помечен номером обновления,
каждый `Update` рисует ровно один кадр, и ожидание заканчивается, когда
ячейки терминала совпали с кадром, нарисованным позже отправленного ключа,
либо когда кадр побайтово тот же, что терминал уже держит (рендерер не
пишет кадр, который не изменился, и ждать записи тогда нечего). Дедлайн
2 с, при несовпадении печатаются расходящиеся строки.

```text
TestMovingTheSelectionDrawsTheScreenAgain
TestOpeningAChatDrawsTheScreenAgain
TestLeavingAChatDrawsTheScreenAgain
TestAModelWithoutASizeHasNothingToRepaint
TestAChangeOfChatIsFollowedByAWholeScreenOfCells
TestARepaintIsWrittenOverEveryCellOfTheWindow
TestTheProgramDrawsWhatItThinksItDraws
TestEveryFrameIsTheSizeOfTheWindow
```

Две половины утверждения измеряются по отдельности: первая группа тестов —
что смена чата просит `tea.WindowSizeMsg` текущего размера, вторая — что
такое сообщение действительно записано в терминал целиком. Считать ячейки
вокруг одного нажатия («надеемся, repaint попал в это окно») — это гонка с
таймером рендерера, чего тест делать не должен.

### Часы в тестах

Тест, который строит проверяемое из системных часов, — это тест, который
проходит сегодня и падает в тот день, когда выбранные им данные кончатся.
`TestPresencePrintsWithoutTheStatus` был таким: он строил presence со сроком
действия и рисовал его через `time.Now()`, и 28 сентября 2026 в 15:00 UTC срок
истёк, экран сказал «last seen at 15:00», а тест счёл это утечкой времени —
правильно про текст и неправильно про момент.

Часы у модели — это два поля, а не вызов: `m.now` (по умолчанию `time.Now`) и
`m.location` (по умолчанию `time.Local`). Тест, который их не закрепляет,
рисует присутствие по часам машины, а наборщик (`modelWithPresence`,
`modelWithSummaryAndPaused`) закрепляет и то и другое, так что статусная строка
читает `testClock` и UTC. Проверка: `TestThePresenceIsDrawnFromTheClockOfTheCaseAndNotTheMachine`
рисует присутствие в 2031 году в `America/Adak` и в `Asia/Vladivostok` и
требует именно тот текст, который дают момент и зона случая, —
`TestThePresenceIsDrawnBeforeTheConnection` без закрепления даёт
«last seen at 02:00» вместо «Online» в зоне машины.

Системные часы читаются в одном месте, `wallClock`, и только для того, что про
время, а не про проверяемое: дедлайн ожидания и затраченное время.
`TestNoTestOfThisPackageBuildsItsDataFromTheWallClock` grep-ает тестовые файлы
пакета и называет файл, который прочитал часы не по назначению.

Проверять: `TZ=UTC go test -count=1 -run Presence ./internal/tui` и то же в
`Asia/Vladivostok` и `America/Adak`.

## 22. Порядок PR

```text
PR-08H  TUI ↔ application wiring
        MessageSubmitter
        outbox open/close
        dispatcher lifecycle
        composer QueueMessage
        enqueue error keeps draft
        delivery-state projection
        no direct-send fallback
        no memory fallback

PR-10A  semantic theme engine
        palette + tokens
        borderless layout
        catppuccin + tokyo night + gruvbox
        fallback profiles
        delivery states отрисовка
        hint bar
        chat list + conversation + composer + status block
        action sheet uncertain
        inline error enqueue
        snapshot tests

PR-10B  theme list / preview / set
        атомарное обновление config
        не трогает auth profile

PR-10C  дополнительные тёмные темы
        одна светлая как gate
        custom palette validation

PR-10D  history pagination
        поиск в чате
        date separators

PR-10E  media
        reply
        forward
        copy
        message information
```

## 23. Что явно вне PR-10A

- reply, forward, media, reactions;
- градиенты по всей ширине header/footer;
- тени на постоянных панелях;
- mouse support;
- i18n;
- drafts между сессиями;
- multiple profiles;
- auto-updater;
- Linux packaging.

## 24. Что поправить в визуальном reference

- Убрать цветные точки слева от `You` — дублируют delivery state.
- Убрать вертикальные линии между панелями, если они есть.
- Не дублировать `✓ Sent` и справа, и под текстом.
- Заменить `📎`, `➤`, `⋮` на варианты с ASCII fallback.
- Action sheet — без рамки, только raised background.
- Inline error — фон только на свой блок, не на всю ширину.
- `Uncertain` не должен выглядеть как `Failed`.
- Проверить контраст текста ≥ 4.5:1.

## 25. Итог

Сводная спецификация закрывает:

1. **Поставку** — packaged TDLib + signed archive (вне TUI).
2. **Установку и настройку** — Homebrew + configure + doctor.
3. **Интерфейс** — semantic theme engine, borderless layout, delivery states, responsive.

Ключевые инварианты, которые нельзя нарушать:

```text
структура   → поверхности
фокус       → одна акцентная сторона
состояние   → цвет + символ + текст
градиент    → необязательное украшение
рамки       → отсутствуют
статус      → никогда только цвет
enqueue     → draft сохраняется при ошибке
uncertain   → без Retry, только явное решение
```

После этого спецификация реализуема без скрытых решений в голове разработчика.
