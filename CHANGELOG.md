# Changelog

Формат основан на [Keep a Changelog](https://keepachangelog.com/ru/1.1.0/),
версии нумеруются по [семантическому версионированию](https://semver.org/lang/ru/).

## [Unreleased]

## [0.1.0]

Первый выпуск.

### Added

- Бот-сборщик на Bot API (long polling): сохраняет сообщения, правки и подписи к вложениям в SQLite;
  смещение обновлений хранится в базе, поэтому перезапуск не теряет и не дублирует обновления.
- Поддержка личных чатов через Telegram Business: бизнес-подключения, сообщения, правки и пометка удалённых сообщений.
- Определение исходящих сообщений (написанных ботом или владельцем бизнес-аккаунта).
- Полнотекстовый поиск на FTS5: все слова должны совпасть, поиск по началу слова, регистр не важен (включая кириллицу).
- MCP-сервер по Streamable HTTP (`/mcp`) и по stdio с инструментами `list_chats`, `get_messages`, `search_messages`,
  `get_message_context`, `get_new_messages` (позиция по `consumer`, режим `peek`), `get_status`.
- Инструмент `send_message`, включается только при `TG_ALLOW_SEND=true`.
- Веб-интерфейс просмотра и поиска без JavaScript (список чатов, поиск, окно контекста, постраничный просмотр).
- Режимы запуска `serve`, `bot`, `stdio` (флаги `-no-bot`, `-with-bot`) и команда `version`.
- Аутентификация по токену `MCP_AUTH_TOKEN`: Bearer для MCP, HTTP Basic с токеном в качестве пароля для браузера;
  отказ запускать HTTP на не-loopback адресе без токена. Конечная точка `/healthz`.
- Ограничение сохраняемых чатов переменной `TG_ALLOWED_CHATS`, собственный Bot API сервер через `TELEGRAM_API_URL`,
  отключение интерфейса через `WEB_UI=false`.
- Dockerfile, systemd-юнит и пример env-файла в `deploy/`, релизные архивы для Linux (amd64, arm64, armv7, armv6),
  macOS (amd64, arm64) и Windows (amd64).
- Документация в `docs/`.

[Unreleased]: https://github.com/mac3g0d/tg-secretary-mcp/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/mac3g0d/tg-secretary-mcp/releases/tag/v0.1.0
