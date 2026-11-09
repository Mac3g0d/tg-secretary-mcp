# Разработка

Как собрать проект, прогнать проверки, выпустить релиз и добавить новый инструмент MCP.
Правила для вкладов: [CONTRIBUTING.md](../CONTRIBUTING.md). Устройство проекта: [architecture.md](architecture.md).

## Что нужно

- Go 1.26 или новее (версия указана в `go.mod`);
- `make`;
- для `go test -race` нужен cgo и C-компилятор (детектор гонок без них не работает); сам бинарник собирается с `CGO_ENABLED=0`;
- токен тестового бота от @BotFather для ручной проверки. Юнит-тесты настоящий Telegram не используют.

## Структура

```
cmd/tg-secretary       точка входа: команды serve, bot, stdio, version
internal/config        переменные окружения и .env
internal/telegram      клиент Bot API
internal/collector     long polling и запись обновлений в базу
internal/store         схема SQLite и запросы
internal/mcpserver     инструменты MCP
internal/web           веб-интерфейс (шаблоны и стили вшиты через embed)
internal/httpserver    HTTP-сервер и проверка токена
deploy/                systemd-юнит и пример env-файла
docs/                  документация
```

Подробно об ответственности пакетов: [architecture.md](architecture.md#пакеты).

## Команды make

| Команда | Что делает |
| --- | --- |
| `make build` | Собирает `bin/tg-secretary` (`CGO_ENABLED=0`) |
| `make test` | `go test -race ./...` |
| `make lint` | Проверяет `gofmt -l .` (должен быть пуст) и `go vet ./...` |
| `make run` | Собирает и запускает `./bin/tg-secretary serve` |
| `make dist VERSION=v0.1.0` | Кросс-компилирует все релизные архивы в `dist/` |

Версия вшивается при сборке флагом компоновщика, `tg-secretary version` её печатает:

```bash
go build -ldflags "-X main.version=v0.1.0" ./cmd/tg-secretary
```

## Тесты

```bash
make test
```

Тесты лежат рядом с кодом (`*_test.go`) и работают на временной базе SQLite и не обращаются к настоящему Telegram.
Один пакет можно прогнать так:

```bash
go test -race ./internal/store/...
```

Перед отправкой изменений запустите:

```bash
make lint test
```

CI (GitHub Actions) выполняет те же проверки: `gofmt`, `go vet`, `go test -race ./...`.

## Стиль кода

- Следуем [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md).
- Код форматируется `gofmt`, а `go vet` должен проходить без замечаний; обе проверки входят в `make lint`.
- Ошибки оборачиваются с контекстом (`fmt.Errorf("...: %w", err)`), логи идут через `log/slog` в stderr.
- Комментарии к экспортируемым идентификаторам обязательны, к пакету тоже.
- Поведение, видимое снаружи (инструменты MCP, переменные окружения, схема базы), меняйте вместе с документацией.

## Как выпускается релиз

1. Убедитесь, что `main` зелёный, и обновите [CHANGELOG.md](../CHANGELOG.md): перенесите пункты из «Unreleased»
   в раздел новой версии с датой.
2. Поставьте тег и отправьте его:

   ```bash
   git tag v0.1.0
   ```

   ```bash
   git push origin v0.1.0
   ```

3. Workflow на теги `v*` собирает архивы и публикует их на странице
   [Releases](https://github.com/mac3g0d/tg-secretary-mcp/releases). Имена архивов:
   `tg-secretary_<версия>_<os>_<arch>.tar.gz` (для Windows `.zip`) и файл `checksums.txt` (sha256).
   Платформы: `linux_amd64`, `linux_arm64`, `linux_armv7`, `linux_armv6`, `darwin_amd64`, `darwin_arm64`, `windows_amd64`.
   В каждом архиве лежат `tg-secretary`, `README.md`, `LICENSE` и `.env.example`.
4. Проверьте скачанный архив: `tg-secretary version` должен печатать выпущенную версию.

Те же архивы можно собрать локально командой `make dist VERSION=v0.1.0`.

## Как добавить инструмент MCP

Инструменты живут в `internal/mcpserver`. В общих чертах:

1. Если нужен новый запрос к данным, добавьте метод в `internal/store` (и покройте его тестом).
   Сам инструмент не должен писать SQL.
2. В `internal/mcpserver` опишите структуру входных параметров с тегами `json` и `jsonschema`
   (описание параметра берётся из `jsonschema`; необязательные поля помечаются `omitempty`) и структуру результата.
3. Напишите обработчик с сигнатурой `func(ctx, *mcp.CallToolRequest, Input) (*mcp.CallToolResult, Output, error)`.
   Возвращённая ошибка уходит клиенту как результат с `isError`.
4. Зарегистрируйте инструмент в конструкторе сервера через `mcp.AddTool` с именем, описанием и подсказками
   (`ReadOnlyHint` для инструментов, которые только читают).
   Инструмент, который пишет наружу (как `send_message`), регистрируйте только при явном включении через конфигурацию.
5. Добавьте тест в пакете `mcpserver`: тесты поднимают сервер и вызывают инструменты через клиентское соединение.
6. Опишите инструмент в [mcp-tools.md](mcp-tools.md), а при необходимости в таблице README и в CHANGELOG.

Ответы инструментов читают модели, поэтому держите описания короткими и точными, а ошибки понятными:
с подсказкой, что делать дальше (как в сообщении `no chat matches ...; use list_chats ...`).
