# Служба sing-box

С шага 31 управляемый профиль загружается из `/var/lib/debian-router-agent/proxy-routing/active/config.json`. При первом обновлении установщик копирует исходный `/etc/debian-router-sing-box.json` в начальное поколение. Файл в `/etc` остаётся источником конфигурации для более ранних релизов. [Правила и группы](proxy-routing.md)

Установщик читает версию sing-box и SHA-256 архива из манифеста релиза маршрутизатора. Бинарник для Linux amd64 загружается из [официальных релизов sing-box](https://github.com/SagerNet/sing-box/releases) и хранится в `/opt/debian-router-b5-2840/sing-box/ВЕРСИЯ/sing-box` отдельно от API и агента. Установщик сохраняет существующий `/etc/debian-router-sing-box.json`. Конфигурация принадлежит `root` и имеет режим `0600`. При запуске systemd передаёт службе копию файла. `sing-box check` проверяет именно эту копию перед `sing-box run`.

Начальная конфигурация принимает локальные HTTP и SOCKS подключения на `127.0.0.1:11080`. Исходящие соединения идут напрямую по системной таблице маршрутизации. На этом шаге клиент указывает прокси явно. Подписки, подключение через удалённый прокси-сервер и прозрачная маршрутизация LAN относятся к следующим шагам.

## Проверка на Debian

Выполните после установки релиза по командам из README. Внешняя HTTPS-проба использует `wget` и требует доступа к `example.com`.

```sh
sudo systemd-analyze verify /etc/systemd/system/debian-router-sing-box.service
systemctl is-enabled debian-router-sing-box.service
systemctl show -p MainPID -p ActiveState debian-router-sing-box.service
ss -ltn 'sport = :11080'
https_proxy=http://127.0.0.1:11080 no_proxy= wget --server-response \
  --output-document=/dev/null --timeout=15 --tries=1 https://example.com/

sudo systemctl restart debian-router-sing-box.service
systemctl show -p MainPID -p ActiveState debian-router-sing-box.service
https_proxy=http://127.0.0.1:11080 no_proxy= wget --server-response \
  --output-document=/dev/null --timeout=15 --retry-connrefused --waitretry=1 --tries=3 https://example.com/
```

Ожидаются `enabled`, `ActiveState=active`, слушатель `127.0.0.1:11080`, другой `MainPID` после перезапуска и два ответа HTTP 200. Состояние запуска можно посмотреть через `journalctl -u debian-router-sing-box.service`. Повторная установка того же релиза сохраняет конфигурацию и PID активного прокси. Установка более раннего релиза без поддержки sing-box оставляет независимую службу в её текущем состоянии. Её можно остановить отдельно командой `sudo systemctl disable --now debian-router-sing-box.service`.
