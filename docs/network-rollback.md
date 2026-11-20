# Проверка независимого таймера отката

Шаг 15 проверяет, что таймер systemd system manager продолжает работу после завершения SSH-сеанса. Интеграционный сценарий использует только отдельный root-owned fixture в `/var/lib/debian-router-rollback-fixture`: он меняет и восстанавливает тестовый файл. Сценарий не читает и не меняет `/etc`, интерфейсы, firewall или сетевые службы.

Таймер и одноразовая служба создаются через `systemd-run --system`. Служба ограничена записью в каталог текущего fixture, не получает capabilities и не имеет сетевых адресных семейств. Это временный таймер systemd: перезагрузка устройства до срабатывания его удалит. Будущая операция изменения сети должна вооружать таймер перед применением и отменять его только после подтверждения связи.

## Проверка после закрытия SSH

Скачайте сценарий в SSH-сеансе B5 и создайте fixture с задержкой 30 секунд:

```sh
/usr/lib/apt/apt-helper download-file https://raw.githubusercontent.com/ASSever/debian-router-b5-2840/main/tests/network-rollback-fixture.sh /tmp/network-rollback-fixture.sh
sudo sh /tmp/network-rollback-fixture.sh arm 30
```

Сохраните выведенный 16-значный hex ID и сразу закройте SSH. Через 35 секунд откройте новое SSH-соединение и проверьте восстановление:

```sh
sudo sh /tmp/network-rollback-fixture.sh verify ID
sudo journalctl -u debian-router-rollback-fixture-ID.service --no-pager
sudo sh /tmp/network-rollback-fixture.sh cleanup ID
rm /tmp/network-rollback-fixture.sh
```

`verify` требует совпадения SHA-256 снимка и восстановленного содержимого, `Result=success`, завершённого timer и фактического применения systemd-ограничений службы. Сценарий проверки отмены выполняется до истечения срока:

```sh
id=$(sudo sh /tmp/network-rollback-fixture.sh arm 90)
sudo sh /tmp/network-rollback-fixture.sh cancel "$id"
sudo sh /tmp/network-rollback-fixture.sh cleanup "$id"
```

`cancel` подтверждает, что timer остановлен до срабатывания, а тестовый файл остался в состоянии `pending-change`. Очистка удаляет только каталог с проверенным ID внутри фиксированного fixture-root. Этот этап доказывает независимость systemd timer от SSH; он ещё не подключён к применению реальной сетевой конфигурации.
