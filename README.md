# tg-secretary-mcp

Telegram-секретарь для AI-агентов: бот на Go читает сообщения через Bot API, складывает их в SQLite
и отдаёт агентам (Hermes, Claude и любым другим MCP-клиентам) инструменты для чтения, поиска и ответа.

```
Telegram ──Bot API (long polling)──▶ бот-сборщик ──▶ SQLite (FTS5) ◀── MCP-сервер ◀── агент
```

- один статический бинарник, без cgo и внешних сервисов;
- полнотекстовый поиск по сообщениям (FTS5, кириллица без учёта регистра);
- правки сообщений обновляют запись, удалённые в бизнес-чатах помечаются, а не стираются;
- лента «что нового» с запоминанием позиции для каждого агента;
- MCP по Streamable HTTP (с Bearer-токеном) или stdio.

## Что видит бот

Bot API отдаёт боту только то, что ему адресовано, и только пока он запущен: истории «задним числом» нет.

| Где | Что нужно сделать |
| --- | --- |
| Личка с ботом | Написать боту `/start` |
| Группы | Добавить бота и выключить Privacy Mode в @BotFather (`/setprivacy` → Disable) либо сделать его админом |
| Каналы | Добавить бота администратором |
| Ваши личные переписки | Подключить бота в Telegram → Настройки → Telegram для бизнеса → Чат-боты (нужен Premium). У бота в @BotFather должен быть включён Business Mode |

Вложения не скачиваются: сохраняется подпись и тип (`photo`, `voice`, `document`…).

## Быстрый старт

Нужен Go 1.26+.

```bash
git clone https://github.com/mac3g0d/tg-secretary-mcp.git
```

```bash
cd tg-secretary-mcp && cp .env.example .env
```

Впишите в `.env` токен от @BotFather и запустите:

```bash
make run
```

Бот начнёт собирать сообщения, MCP-сервер поднимется на `http://127.0.0.1:8080/mcp`.

### Docker

```bash
docker build -t tg-secretary .
```

```bash
docker run -d --name tg-secretary --env-file .env -e MCP_ADDR=0.0.0.0:8080 -e MCP_AUTH_TOKEN=change-me -p 127.0.0.1:8080:8080 -v tg-secretary-data:/app/data tg-secretary
```

## Режимы запуска

| Команда | Что делает |
| --- | --- |
| `tg-secretary serve` | Бот + MCP по HTTP в одном процессе. Основной режим |
| `tg-secretary bot` | Только бот-сборщик (демон) |
| `tg-secretary stdio` | Только MCP по stdio, читает ту же базу. Для агентов, которые сами запускают сервер |

Telegram допускает один `getUpdates` на токен, поэтому бот должен работать ровно в одном процессе:
либо `serve`, либо `bot` + сколько угодно `stdio`. Флаги `serve -no-bot` и `stdio -with-bot` меняют поведение по умолчанию.

## Настройки

| Переменная | По умолчанию | Описание |
| --- | --- | --- |
| `TELEGRAM_BOT_TOKEN` | — | Токен бота |
| `TG_DB_PATH` | `data/secretary.db` | Файл базы SQLite |
| `TG_ALLOWED_CHATS` | все | ID чатов через запятую, которые разрешено сохранять |
| `TG_ALLOW_SEND` | `false` | Включает инструмент `send_message` |
| `MCP_ADDR` | `127.0.0.1:8080` | Адрес HTTP-сервера |
| `MCP_AUTH_TOKEN` | — | Bearer-токен для `/mcp`; обязателен, если адрес не localhost |
| `TELEGRAM_API_URL` | `https://api.telegram.org` | Свой Bot API сервер |

Переменные читаются из окружения и из `.env` в рабочей директории.

## Инструменты MCP

| Инструмент | Назначение |
| --- | --- |
| `get_new_messages` | Входящие с момента прошлого вызова. Позиция хранится по имени `consumer`; `peek` — посмотреть, не сдвигая |
| `list_chats` | Чаты по убыванию активности, с числом сообщений и превью последнего |
| `get_messages` | Сообщения чата по порядку; фильтры `since` / `until` / `sender` |
| `search_messages` | Полнотекстовый поиск: все слова, можно начало слова; фильтры по чату, отправителю, времени |
| `get_message_context` | Сообщения вокруг найденного |
| `get_status` | Статистика базы и когда бот последний раз получал обновления |
| `send_message` | Отправить текст от имени бота (в бизнес-чате — от вашего имени). Только при `TG_ALLOW_SEND=true` |

Чат задаётся ID, `@username` или частью названия. Время — RFC 3339, `2026-01-31` или относительно: `30m`, `24h`, `7d`, `2w`.

## Подключение агентов

### Hermes Agent

`~/.hermes/config.yaml`, по HTTP (секретарь запущен через `serve`):

```yaml
mcp_servers:
  tg_secretary:
    url: "http://127.0.0.1:8080/mcp"
    headers:
      Authorization: "Bearer change-me"   # если задан MCP_AUTH_TOKEN
```

Или через stdio (в фоне работает `tg-secretary bot`):

```yaml
mcp_servers:
  tg_secretary:
    command: "/path/to/tg-secretary"
    args: ["stdio"]
    env:
      TG_DB_PATH: "/path/to/data/secretary.db"
```

### Claude Code

```bash
claude mcp add --transport http tg-secretary http://127.0.0.1:8080/mcp
```

## Безопасность

- База содержит чужую переписку. Файл создаётся с правами `0600`; не коммитьте `data/` и `.env`.
- Без `MCP_AUTH_TOKEN` сервер отказывается слушать не-localhost адрес. Для доступа по сети ставьте его за TLS-прокси.
- Тексты сообщений пишут посторонние люди, для агента это недоверенные данные (возможны prompt-инъекции).
  Поэтому отправка выключена по умолчанию, а круг чатов стоит ограничить через `TG_ALLOWED_CHATS`.
- Боту может написать кто угодно; без `TG_ALLOWED_CHATS` такие сообщения тоже попадут в базу.

## Разработка

```bash
make lint test
```

```
cmd/tg-secretary     точка входа и HTTP-сервер
internal/telegram    минимальный клиент Bot API
internal/collector   long polling и запись обновлений
internal/store       схема SQLite и запросы
internal/mcpserver   инструменты MCP
```

## Лицензия

MIT
