# DDNS

Служба `debian-router-ddns.service` обновляет одну запись A у провайдера с поддержкой DynDNS2 по HTTPS. После установки служба выключена.

Создайте доступный для чтения файл `/etc/debian-router-b5-2840/ddns.json` и подставьте URL обновления, доменное имя и источник IPv4.

```json
{
  "endpoint": "https://ddns.example.net/nic/update",
  "record": "router.example.net",
  "interface": "enp1s0",
  "interval_seconds": 300
}
```

`interface` подходит, когда у интерфейса один IPv4, который должен быть опубликован. Если внешний адрес выдаёт вышестоящий NAT, вместо `interface` укажите `address_url` собственного или доверенного HTTPS-сервиса, возвращающего один IPv4 обычным текстом. Для установок с PPPoE можно указать имя рабочего PPP-интерфейса. Интервал по умолчанию составляет 300 секунд, допустимый диапазон 5–3600 секунд.

Запишите логин и пароль провайдера в два файла, доступных только root. Выполните `sudo systemctl edit debian-router-ddns.service` и добавьте ссылки на эти файлы.

```ini
[Service]
LoadCredential=ddns-username:/etc/debian-router-b5-2840/credentials/ddns-username
LoadCredential=ddns-password:/etc/debian-router-b5-2840/credentials/ddns-password
```

```sh
cd /opt/debian-router-b5-2840/current
sudo ./router-ddns check-config
sudo systemctl enable --now debian-router-ddns.service
sudo ./router-ddns status
sudo journalctl -u debian-router-ddns.service -n 30 --no-pager
```

После изменения настроек или файлов учётных данных перезапустите службу командой `sudo systemctl restart debian-router-ddns.service`.

`observed_address` показывает адрес источника, `published_address` хранит последний адрес, подтверждённый провайдером. `result` принимает значения `updated`, `unchanged` или `error`. При ошибке поле `error` содержит код, например `interface_unavailable`, `authentication_failed` или `provider_http_error`. Последний принятый адрес сохраняется после ошибки и перезапуска.

Служба повторяет временные ошибки на следующей проверке. При ограничении частоты и ответах `911`, `dnserr` или HTTP 500 служба ждёт 30 минут, время следующей попытки указано в `retry_at`. При отказе в авторизации или явном отказе провайдера поле `requires_restart` указывает, что после устранения причины нужно перезапустить службу. Журнал показывает ошибки запуска и изменения результата. Для остановки обновлений выполните `sudo systemctl disable --now debian-router-ddns.service`. [Коды ответов DynDNS2 в документации No-IP](https://www.noip.com/integrate/response)
