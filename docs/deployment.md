# Развёртывание

Как запустить `tg-secretary` как постоянный сервис: бинарник и systemd, Docker, Orange Pi PC Plus,
резервные копии, обновление, TLS. Параметры описаны в [configuration.md](configuration.md).

Содержание:

- [Бинарник](#бинарник)
- [systemd](#systemd)
- [Docker](#docker)
- [Orange Pi PC Plus (Armbian)](#orange-pi-pc-plus-armbian)
- [Резервные копии](#резервные-копии)
- [Обновление](#обновление)
- [Обратный прокси и TLS](#обратный-прокси-и-tls)

## Бинарник

Готовые архивы публикуются на странице [Releases](https://github.com/mac3g0d/tg-secretary-mcp/releases)
для каждого тега `v*`. Имя архива: `tg-secretary_<версия>_<os>_<arch>.tar.gz` (для Windows `.zip`).
Рядом лежит `checksums.txt` с суммами sha256.

| Платформа | Суффикс |
| --- | --- |
| Linux x86-64 | `linux_amd64` |
| Linux 64-bit ARM (`aarch64`) | `linux_arm64` |
| Linux 32-bit ARM v7 (`armv7l`) | `linux_armv7` |
| Linux 32-bit ARM v6 | `linux_armv6` |
| macOS Intel / Apple Silicon | `darwin_amd64` / `darwin_arm64` |
| Windows x86-64 | `windows_amd64` |

Архитектуру Linux показывает `uname -m`:

```bash
uname -m
```

`armv7l` соответствует `linux_armv7`, `aarch64` соответствует `linux_arm64`, `x86_64` соответствует `linux_amd64`.

В архиве лежат бинарник `tg-secretary`, `README.md`, `LICENSE` и `.env.example`. Бинарник статический,
отдельных зависимостей не требует. Проверить версию:

```bash
tg-secretary version
```

Либо соберите его сами, см. [development.md](development.md).

## systemd

Эталонные файлы лежат в каталоге `deploy/`:

- [deploy/tg-secretary.service](../deploy/tg-secretary.service): юнит;
- [deploy/tg-secretary.env.example](../deploy/tg-secretary.env.example): пример файла окружения.

Юнит запускает `/usr/local/bin/tg-secretary serve` от пользователя `tg-secretary`, читает окружение из
`/etc/tg-secretary/tg-secretary.env`, хранит данные в `/var/lib/tg-secretary` (`StateDirectory`) и ограничивает
процесс: `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp` и другие. При ошибке сервис
перезапускается через 5 секунд (`Restart=on-failure`): это нужно, потому что бот завершается, если при старте
недоступна сеть или Telegram.

Бинарник читает `.env` из рабочей директории, а `TG_DB_PATH` по умолчанию относительный (`data/secretary.db`).
Поэтому в env-файле `TG_DB_PATH` задан явно: `/var/lib/tg-secretary/secretary.db`.

Установка по шагам (бинарник уже лежит в `/usr/local/bin/tg-secretary`):

```bash
sudo useradd --system --home-dir /var/lib/tg-secretary --shell /usr/sbin/nologin tg-secretary
```

```bash
sudo install -d -m 0750 /etc/tg-secretary
```

```bash
sudo install -m 0600 deploy/tg-secretary.env.example /etc/tg-secretary/tg-secretary.env
```

Впишите в `/etc/tg-secretary/tg-secretary.env` токен бота и остальные значения:

```bash
sudoedit /etc/tg-secretary/tg-secretary.env
```

```bash
sudo install -m 0644 deploy/tg-secretary.service /etc/systemd/system/tg-secretary.service
```

```bash
sudo systemctl daemon-reload
```

```bash
sudo systemctl enable --now tg-secretary
```

Статус и логи (приложение пишет логи в stderr, их собирает journald):

```bash
systemctl status tg-secretary
```

```bash
journalctl -u tg-secretary -f
```

Проверка работы без аутентификации:

```bash
curl http://127.0.0.1:8080/healthz
```

В ответе будет `ok`. Что бот собирает сообщения, видно по `get_status` или в [веб-интерфейсе](web-ui.md).

Для нескольких процессов на одной базе (демон `bot` и `stdio` для агентов) замените `serve` в `ExecStart` на `bot`
и запускайте `tg-secretary stdio` из агента с тем же абсолютным `TG_DB_PATH`. Пользователь агента должен иметь доступ
на чтение и запись к каталогу `/var/lib/tg-secretary` (SQLite создаёт там файлы `-wal` и `-shm`), а юнит выдаёт
каталогу права `0700` для `tg-secretary`. Проще запускать агента и сервис под одним пользователем.

## Docker

Образ собирается из `Dockerfile` в корне репозитория (два этапа: сборка на `golang`, запуск в `distroless`
под непривилегированным пользователем `nonroot`).

```bash
docker build -t tg-secretary .
```

```bash
docker run -d --name tg-secretary --restart unless-stopped --env-file .env -e MCP_ADDR=0.0.0.0:8080 -e MCP_AUTH_TOKEN=change-me -p 127.0.0.1:8080:8080 -v tg-secretary-data:/app/data tg-secretary
```

Замечания:

- Рабочая директория в образе `/app`, база по умолчанию `/app/data/secretary.db`, том монтируется в `/app/data`.
- Внутри контейнера сервис должен слушать не loopback, иначе порт недоступен снаружи; поэтому `-e MCP_ADDR=0.0.0.0:8080`
  перекрывает значение из `.env`. По этой же причине `MCP_AUTH_TOKEN` обязателен. Порт публикуется только на
  `127.0.0.1` хоста; для доступа из сети поставьте TLS-прокси.
- `--env-file` Docker не вырезает кавычки вокруг значений, поэтому в файле для Docker пишите значения без кавычек.
- Логи: `docker logs -f tg-secretary`.

## Orange Pi PC Plus (Armbian)

Orange Pi PC Plus построен на Allwinner H3 (4 ядра Cortex-A7, 32-битный ARMv7); под Armbian это `armv7l`.
Нужен архив `linux_armv7`. Если на плате другая плата или 64-битная ОС, сверьтесь с `uname -m`:
`armv7l` соответствует `linux_armv7`, `aarch64` соответствует `linux_arm64`.

Команды ниже выполняются на плате по SSH. Нужны `curl`, `sudo`, `tar`. Токен бота у @BotFather уже должен быть.

1. Проверьте архитектуру:

   ```bash
   uname -m
   ```

   Ожидается `armv7l`.

2. Выберите версию и скачайте архив с контрольными суммами во временный каталог:

   ```bash
   VERSION=v0.1.0
   ```

   ```bash
   mkdir -p ~/tg-secretary-dl && cd ~/tg-secretary-dl
   ```

   ```bash
   curl -fLO "https://github.com/mac3g0d/tg-secretary-mcp/releases/download/${VERSION}/tg-secretary_${VERSION}_linux_armv7.tar.gz"
   ```

   ```bash
   curl -fLO "https://github.com/mac3g0d/tg-secretary-mcp/releases/download/${VERSION}/checksums.txt"
   ```

   Переменная `VERSION` действует только в текущей сессии оболочки; выполняйте шаги в одной сессии.
   Имя файла должно совпадать с тем, что указано на странице релиза.

3. Проверьте sha256:

   ```bash
   sha256sum --ignore-missing -c checksums.txt
   ```

   Строка с вашим архивом должна закончиться на `OK`. При `FAILED` архив скачан неверно или подменён; не устанавливайте его.

4. Распакуйте и установите бинарник:

   ```bash
   tar -xzf "tg-secretary_${VERSION}_linux_armv7.tar.gz"
   ```

   ```bash
   sudo install -m 0755 tg-secretary /usr/local/bin/tg-secretary
   ```

   Если архив распаковался в подкаталог, укажите путь к бинарнику в нём.

   ```bash
   tg-secretary version
   ```

5. Создайте системного пользователя и каталог данных:

   ```bash
   sudo useradd --system --home-dir /var/lib/tg-secretary --shell /usr/sbin/nologin tg-secretary
   ```

   ```bash
   sudo install -d -o tg-secretary -g tg-secretary -m 0700 /var/lib/tg-secretary
   ```

6. Создайте файл окружения с правами 600. Исходный пример есть в архиве (`.env.example`) и в репозитории
   (`deploy/tg-secretary.env.example`); можно скачать его так:

   ```bash
   sudo install -d -m 0750 /etc/tg-secretary
   ```

   ```bash
   sudo curl -fL -o /etc/tg-secretary/tg-secretary.env "https://raw.githubusercontent.com/mac3g0d/tg-secretary-mcp/${VERSION}/deploy/tg-secretary.env.example"
   ```

   ```bash
   sudo chmod 600 /etc/tg-secretary/tg-secretary.env
   ```

   ```bash
   sudoedit /etc/tg-secretary/tg-secretary.env
   ```

   Заполните `TELEGRAM_BOT_TOKEN`. Если интерфейс нужен из локальной сети, задайте `MCP_ADDR=0.0.0.0:8080` и
   обязательно `MCP_AUTH_TOKEN` (без него сервис не запустится на таком адресе). Токен можно сгенерировать
   командой `openssl rand -hex 32`.

7. Установите юнит (файл `deploy/tg-secretary.service`) и включите сервис:

   ```bash
   sudo curl -fL -o /etc/systemd/system/tg-secretary.service "https://raw.githubusercontent.com/mac3g0d/tg-secretary-mcp/${VERSION}/deploy/tg-secretary.service"
   ```

   ```bash
   sudo systemctl daemon-reload
   ```

   ```bash
   sudo systemctl enable --now tg-secretary
   ```

8. Проверьте логи:

   ```bash
   journalctl -u tg-secretary -n 50 --no-pager
   ```

   В логе должны быть строки `bot started` и `MCP server listening`.

9. Откройте интерфейс из локальной сети. Узнайте адрес платы:

   ```bash
   hostname -I
   ```

   В браузере откройте `http://<адрес платы>:8080/`. Логин любой, пароль равен `MCP_AUTH_TOKEN`.
   Если файрвол включён, откройте порт 8080 только для локальной сети.

10. Напишите боту (или добавьте его в чат, см. [что бот видит](../README.md#что-бот-видит-и-чего-не-видит)):
    сообщения появятся в интерфейсе.

Замечания по плате:

- Запись на SD-карту невелика: SQLite работает в режиме WAL с `synchronous=NORMAL`, а объём сообщений обычно мал.
  Тем не менее SD-карты изнашиваются и выходят из строя внезапно, поэтому настройте регулярные
  [резервные копии](#резервные-копии) на другой носитель или другой компьютер.
- Если доступ нужен только самой плате (например, агент запущен на ней же), оставьте `MCP_ADDR=127.0.0.1:8080`.
- При первом запуске нужны точное время и сеть: без доступа к `api.telegram.org` сервис завершится и будет перезапущен
  systemd через 5 секунд.

## Резервные копии

В базе вся архивная переписка и служебное состояние (в том числе смещение обновлений). Её стоит копировать регулярно.

Предпочтительный способ: онлайн-копия средствами SQLite. Нужен пакет `sqlite3`. Команду запускайте от пользователя
сервиса, чтобы не создать рядом файлы `-wal` и `-shm` с владельцем root:

```bash
sudo -u tg-secretary sqlite3 /var/lib/tg-secretary/secretary.db ".backup '/var/lib/tg-secretary/backup.db'"
```

Копия получается целостной даже при работающем сервисе. Затем перенесите её в безопасное место
(другой диск, другой хост) и не забудьте, что в ней личная переписка: храните с теми же ограничениями доступа.

Альтернатива: остановить сервис и скопировать файл.

```bash
sudo systemctl stop tg-secretary
```

```bash
sudo cp -p /var/lib/tg-secretary/secretary.db* /srv/backup/
```

```bash
sudo systemctl start tg-secretary
```

Про файлы WAL: в режиме WAL рядом с `secretary.db` лежат `secretary.db-wal` и `secretary.db-shm`. Не копируйте
только `secretary.db` у работающего сервиса: последние записи могут лежать ещё в `-wal`, и копия окажется неполной или
неконсистентной. После корректной остановки SQLite обычно сливает WAL в основной файл и удаляет его, но если `-wal`
остался, копируйте его вместе с основным файлом (как в шаблоне `secretary.db*` выше).

Восстановление: остановите сервис, положите копию на место `secretary.db`, удалите старые `secretary.db-wal` и
`secretary.db-shm`, верните владельца (`chown tg-secretary:tg-secretary`) и запустите сервис. Смещение обновлений
откатится вместе с базой; Telegram хранит неподтверждённые обновления ограниченное время (по документации Bot API
до 24 часов), так что сообщения за период между копией и восстановлением могут не вернуться.

## Обновление

1. Скачайте новый архив и проверьте sha256 (как в разделе про [Orange Pi](#orange-pi-pc-plus-armbian)).
2. Прочитайте [CHANGELOG.md](../CHANGELOG.md): миграций нет, схема применяется при запуске командами
   `CREATE ... IF NOT EXISTS`, поэтому важные изменения схемы будут отмечены там.
3. Сделайте [резервную копию](#резервные-копии).
4. Остановите сервис, замените бинарник и запустите снова:

   ```bash
   sudo systemctl stop tg-secretary
   ```

   ```bash
   sudo install -m 0755 tg-secretary /usr/local/bin/tg-secretary
   ```

   ```bash
   sudo systemctl start tg-secretary
   ```

5. Проверьте версию и логи:

   ```bash
   tg-secretary version
   ```

   ```bash
   journalctl -u tg-secretary -n 20 --no-pager
   ```

В Docker: пересоберите образ (`docker build -t tg-secretary .`), удалите старый контейнер
(`docker rm -f tg-secretary`) и запустите новый с тем же томом.

## Обратный прокси и TLS

Сервис сам TLS не терминирует. И `Authorization: Bearer`, и пароль HTTP Basic передаются открытым текстом,
поэтому всё, что выходит за пределы доверенной локальной сети, должно идти через прокси с TLS.
Оставьте сервис на `127.0.0.1:8080` и пересылайте на него запросы.

Минимальный пример для Caddy (сертификат выпускается автоматически):

```
secretary.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

В других прокси (например, nginx) отключите буферизацию ответов для `/mcp`: Streamable HTTP может отдавать
ответ потоком. Токен `MCP_AUTH_TOKEN` при этом остаётся обязательным: прокси его не заменяет.
