# Служба Zapret

Инсталлятор устанавливает `debian-router-zapret.service` с профилем ALT11. Новая служба установлена в состоянии `disabled`, её первое включение выполняет администратор. Повторная установка сохраняет состояние службы. Исполняемый файл Linux `nfqws` загружается из [zapret v72.13](https://github.com/bol-van/zapret/releases/tag/v72.13), данные профиля берутся из закреплённой [ревизии Flowseal](https://github.com/Flowseal/zapret-discord-youtube/tree/cf018db758623ed1813b9d93a6d7f3bdf01cc2b4). Инсталлятор проверяет SHA‑256 обоих архивов и запускает проверку параметров `nfqws --dry-run`.

Linux-профиль сохраняет порядок стратегий ALT11, штатные списки и данные для TCP, QUIC и Discord. Игровой фильтр выключен.

## Выбор профиля и исключения

- `alt11` использует TCP и UDP. Это профиль по умолчанию.
- `alt11-tcp` сохраняет TCP-стратегии ALT11 и отключает обработку UDP в nfqws.

Исключения задаются файлом с одним доменным именем на строку. Обычная запись включает поддомены, префикс `^` ограничивает исключение указанным именем. Допускаются пустые строки и комментарии с `#`. Для интернациональных имён используйте ASCII-форму Punycode. Запись сопоставляется с именем, которое nfqws распознаёт в соединении, например TLS SNI или HTTP Host. Проверка на стенде охватывает HTTPS по TCP.

```sh
zapret_config=/opt/debian-router-b5-2840/current/zapret-config.sh
printf '%s\n' example.org '^video.example.net' > zapret-exclude.txt
sudo "$zapret_config" check alt11-tcp zapret-exclude.txt
sudo "$zapret_config" apply alt11-tcp zapret-exclude.txt
sudo "$zapret_config" show
```

`check` проверяет список и запускает `nfqws --dry-run`. `apply` сохраняет прошедшую проверку конфигурацию, перезапускает активную службу и восстанавливает предыдущую конфигурацию при ошибке запуска. Выключенная служба применит выбор при следующем включении. Повторная установка сохраняет выбранный профиль и исключения. Команда `apply alt11` без файла возвращает профиль по умолчанию и очищает пользовательские исключения.

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

Для приёмки на B5 используется изолированная сеть с LAN-клиентом и HTTPS-сервером на виртуальном WAN. Два имени из штатного ALT11 hostlist направляются на один адрес стенда. Сравниваются обычное соединение, трафик при выбранном профиле и трафик после добавления исключения для одного имени. HTTPS должен отвечать во всех случаях. Для исключённого имени байты TCP на LAN и WAN должны совпадать с учётом сегментации, а для второго имени на WAN должны быть видны дополнительные данные профиля. Контрольная UDP-проба STUN различает `alt11` и `alt11-tcp`.
