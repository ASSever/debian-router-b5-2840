# Служба Zapret

Инсталлятор устанавливает `debian-router-zapret.service` с профилем ALT11. Новая служба установлена в состоянии `disabled`, её первое включение выполняет администратор. Повторная установка сохраняет состояние службы. Исполняемый файл Linux `nfqws` загружается из [zapret v72.13](https://github.com/bol-van/zapret/releases/tag/v72.13), данные профиля берутся из закреплённой [ревизии Flowseal](https://github.com/Flowseal/zapret-discord-youtube/tree/cf018db758623ed1813b9d93a6d7f3bdf01cc2b4). Инсталлятор проверяет SHA‑256 обоих архивов и запускает проверку параметров `nfqws --dry-run`.

Linux-профиль сохраняет порядок стратегий ALT11, штатные списки и полезные нагрузки для TCP, QUIC и Discord. Игровой фильтр выключен. Пользовательские списки и выбор профилей предусмотрены отдельным шагом плана.

## Включение и выключение

```sh
sudo systemctl enable --now debian-router-zapret.service
systemctl is-enabled debian-router-zapret.service
systemctl is-active debian-router-zapret.service
sudo nft list table inet b5_zapret

sudo systemctl disable --now debian-router-zapret.service
systemctl is-enabled debian-router-zapret.service
systemctl is-active debian-router-zapret.service
```

По умолчанию служба обрабатывает ранние пакеты соединений, пересылаемые из `enp2s0` в `enp1s0`. Для другой пары интерфейсов задайте `ZAPRET_LAN_IF` и `ZAPRET_WAN_IF` в секции `[Service]` через `sudo systemctl edit debian-router-zapret.service`. Зарезервированы очередь 62232 и метки `0x02000000` для пересылаемых пакетов и `0x40000000` для пакетов, созданных `nfqws` после SNAT. Созданные пакеты на WAN исключаются из повторной обработки conntrack. Когда процесс перестаёт слушать очередь, `bypass` пропускает пакеты. При остановке служба удаляет таблицу `b5_zapret`.

При загрузке правил ядро включает учёт пакетов conntrack в сетевом пространстве службы. Эта настройка ядра может сохраняться после удаления таблицы.

Для приёмки на B5 используется отдельное сетевое пространство имён с LAN-клиентом и HTTPS-сервером на виртуальном WAN. Имя тестового TLS-запроса берётся из штатного ALT11 hostlist и направляется на локальный адрес. Сравниваются ответы и пакеты до включения, при работающем профиле и после выключения. Этот тест измеряет работу службы и обработку трафика на стенде.
