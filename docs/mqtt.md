# Публикация статуса через MQTT

Пакет устанавливает службу `debian-router-mqtt.service`. Для работы ей нужен отдельный конфиг `/etc/debian-router-b5-2840/mqtt.json`, доступный для чтения службе. Пример для брокера с TLS:

```json
{
  "broker": "mqtts://broker.example:8883",
  "client_id": "b5-router",
  "topic_prefix": "debian-router/b5",
  "interval_seconds": 30
}
```

Укажите уникальные `client_id` и `topic_prefix` для каждого маршрутизатора. Для `mqtts://` требуется сертификат, подтверждаемый системным хранилищем доверия. `mqtt://` передаёт данные и пароль без шифрования и подходит для доверенной изолированной сети. Ограничьте брокерную учётную запись публикацией под выбранным префиксом.

Для брокера с аутентификацией разместите имя пользователя и пароль в двух отдельных файлах с владельцем root и режимом `0600`. В `sudo systemctl edit debian-router-mqtt` укажите пути к файлам:

```ini
[Service]
LoadCredential=mqtt-username:/etc/debian-router-b5-2840/mqtt-secrets/username
LoadCredential=mqtt-password:/etc/debian-router-b5-2840/mqtt-secrets/password
```

Проверьте конфиг и включите службу:

```sh
sudo /opt/debian-router-b5-2840/current/router-mqtt check-config
sudo systemctl enable --now debian-router-mqtt
```

Клиент публикует JSON в `<prefix>/status` с UTC-временем измерения, uptime в секундах и состояниями несущей WAN/LAN. Неизвестное показание представлено JSON-значением `null`. Тема `<prefix>/availability` содержит `online` или `offline`. Обе темы используют QoS 1 и retain. При потере клиента брокер может публиковать `offline` через Last Will. При штатной остановке клиент публикует `offline` перед разрывом соединения.

После сбоя клиент повторяет соединение с увеличением ожидания с 2 до 30 секунд. Первое сообщение после восстановления содержит свежий снимок, после него публикуется `online`. Подписчик должен учитывать `availability` и `sampled_at`, поскольку брокер может хранить последний снимок. Home Assistant Discovery предусмотрен на шаге 40.
