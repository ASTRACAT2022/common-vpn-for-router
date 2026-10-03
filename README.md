# Common VPN Router

Минимальный VPN-клиент для OpenWrt и Entware на базе Xray Core. Пользователь добавляет ссылку на подписку, выбирает сервер и подключается из встроенного Web UI. Ссылка Happ/Common с правилами маршрутизации необязательна.

## Возможности

- Подписки VLESS, VMess, Trojan и Shadowsocks: URI, Base64-списки URI и VMess JSON.
- Встроенная русскоязычная Web UI в тёмной теме: добавить подписку, выбрать сервер, подключить или отключить VPN.
- Постоянный авто-режим проверяет серверы HTTPS GET-запросом через Xray, выбирает самый быстрый прошедший сервер и продолжает проверять туннель каждые 10 секунд. После двух подряд неудачных проверок автоматически проверяет другие серверы и переключается на лучший рабочий.
- Ручное подключение или отключение авто-режима сохраняет выбранный вручную сервер. Состояние авто-режима хранится в настройках и возобновляется после перезапуска daemon.
- На OpenWrt и Entware по умолчанию используется Xray TUN с системными маршрутами. Исходящие соединения Xray привязываются к физическому интерфейсу, чтобы не зациклить трафик.
- Необязательный импорт `happ://routing/onadd/<BASE64>` и `common://routing/onadd/<BASE64>`. Импортируются только `GlobalProxy`, порядок `block/proxy/direct`, доменные/IP-списки и `DomainStrategy`. DNS, geo-файлы и остальные поля Happ игнорируются.
- Без профиля весь TCP/UDP трафик направляется через выбранный VPN-сервер. С профилем применяются его правила и `GlobalProxy` fallback.
- После подключения состояние сохраняется, а Xray восстанавливается при перезапуске daemon.
- Новая конфигурация сначала проверяется Xray. Если проверка или запуск не проходят, прежняя рабочая конфигурация восстанавливается.
- Подписки обновляются с ETag и Last-Modified; ошибка обновления сохраняет последнюю загруженную версию.

Common VPN не настраивает DNS. DNS-поля из Happ routing-ссылки не применяются.

## Сборка

Нужен Go 1.22 или новее.

```sh
make test
make build
make build-linux
make release-linux VERSION=v1.0.0
```

`make build-linux` собирает Linux amd64, arm64, armv7, mips и mipsle. `make release-linux` дополнительно создаёт архивы common-vpn-linux-<архитектура>.tar.gz для релиза.

## Запуск

На роутере запустите daemon от root, чтобы Xray мог создавать TUN-интерфейс и системные маршруты:

```sh
./common-vpn -xray /usr/bin/xray
```

Для Entware укажите путь к установленному Xray, например `/opt/bin/xray`. По умолчанию API и UI доступны на `127.0.0.1:8787`. Чтобы открыть UI с устройства в LAN, задайте LAN-адрес роутера и убедитесь, что firewall не разрешает доступ со стороны WAN:

```sh
./common-vpn -listen 192.168.1.1:8787 -xray /usr/bin/xray
```

У API/UI нет отдельной учётной записи; порт нельзя публиковать в WAN. Для локальной разработки:

```sh
go run ./cmd/common-vpn -config-dir ./local-config -data-dir ./local-data -xray xray -tun=false
```

### Установка на роутер

Скрипт install-auto обнаруживает OpenWrt, Entware или обычный Linux, определяет архитектуру и LAN IPv4, скачивает сборку клиента и официальный Xray Core, включает системную службу и проверяет ответ панели. Для OpenWrt он ставит kmod-tun, если модуль доступен в пакетном репозитории. DNS скрипт и daemon не настраивают.

Установщик уже настроен на репозиторий `ASTRACAT2022/common-vpn-for-router`. Чтобы выпустить новую версию, отправь тег вида `v1.0.0`: GitHub Actions соберёт архивы для amd64, arm64, armv7, mips и mipsle и создаст GitHub Release с бинарными файлами. Установщик скачивает актуальные архивы из последнего релиза. Workflow также можно запустить вручную в GitHub Actions — сборка появится среди артефактов запуска на 30 дней.

Запускай её в SSH-сеансе на самом роутере от root:

    curl -fsSL https://raw.githubusercontent.com/ASTRACAT2022/common-vpn-for-router/main/install-auto | sh

или:

    wget -qO- https://raw.githubusercontent.com/ASTRACAT2022/common-vpn-for-router/main/install-auto | sh

Установщик напечатает адрес панели, например http://192.168.1.1:8787/. UI/API не имеют отдельного пароля: не публикуй порт панели в WAN и не добавляй правило его проброса наружу.

## Работа с UI

1. Откройте `http://192.168.1.1:8787/` (подставьте адрес роутера).
2. Вставьте URL подписки и нажмите «Добавить».
3. Выберите сервер и нажмите «Подключиться».
4. Для постоянного авто-режима нажмите «Авто · лучший сервер». Кнопка сменится на «Отключить авто»; ручное подключение и отключение также выключают мониторинг.
5. При необходимости импортируйте ссылку Happ/Common. Правила применятся при подключении VPN.

Подписки ограничены 2 MiB. URL подписки маскируются в API-ответах. Данные daemon хранятся в JSON-файле с правами `0600`.

## API

- `GET /api/status`
- `GET, POST /api/subscriptions`
- `GET, PUT, DELETE /api/subscriptions/{id}`
- `POST /api/subscriptions/{id}/update`
- `GET /api/nodes`, `POST /api/nodes/{id}/select`
- `GET /api/routing`, `POST /api/routing/import`, `DELETE /api/routing/{id}`
- `POST /api/vpn/connect`, `POST /api/vpn/disconnect`, `POST /api/vpn/restart`
- `POST /api/vpn/auto-connect`, `POST /api/vpn/auto-disable`
- `GET /api/system/info`

## Структура

- `cmd/common-vpn`: daemon
- `internal/subscription`, `internal/node`: подписки и модель серверов
- `internal/routing`: импорт Happ/Common и правила маршрутизации
- `internal/xray`: конфигурация, HTTPS-проверка через SOCKS и управление Xray
- `internal/storage`: локальное атомарное хранилище
- `internal/api`, `internal/webui`: REST API и встроенный UI
- `internal/platform`: пути по умолчанию для OpenWrt и Entware

Сборка и unit-тесты проверяются в Go. Реальную работу TUN и прохождение трафика нужно отдельно проверить на целевой модели роутера с установленным Xray Core.
