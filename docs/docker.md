# Docker и Portainer

После установки маршрутизатора можно отдельно включить Docker Engine из пакетов Debian. Portainer CE устанавливается только второй командой и использует образ 2.45.4, закреплённый по digest.

```sh
cd /opt/debian-router-b5-2840/current
sudo sh ./router-containers.sh --install-docker
sudo sh ./router-containers.sh --install-portainer
```

Перед установкой обновите индексы APT командой `sudo apt-get update`. При удалённой установке используйте независимый SSH через Wi-Fi. Если Docker уже установлен с другой конфигурацией, проверьте её вручную. Установщик проверяет, что конфигурация совместима, и сохраняет данные существующих контейнеров.

Docker работает с отключённым мостом по умолчанию и отключённым управлением iptables, IPv6 iptables, IP forwarding и NAT. Правила и forwarding задаёт маршрутизатор. Для контейнеров доступны режимы `host` и `none`. Команда `-p` требует отдельно спроектированной контейнерной сети. [Правила Docker Engine на маршрутизаторе](https://docs.docker.com/engine/network/packet-filtering-firewalls/)

Portainer слушает HTTPS на `127.0.0.1:9443`. Откройте к нему SSH-туннель. Подставьте свой адрес управления.

```sh
ssh -L 19443:127.0.0.1:9443 router-admin@router.example.net
```

Откройте в браузере `https://localhost:19443`, проверьте сертификат Portainer и завершите первоначальную настройку. Portainer управляет локальным Docker через Unix-сокет. Данные хранятся в томе `debian-router-portainer-data`.

Для остановки Docker вместе с активирующим сокетом используйте `sudo systemctl stop docker.socket docker.service`. Службы маршрутизатора продолжат работать. После `sudo systemctl start docker.service` Portainer запустится по политике перезапуска, если ранее его явно не остановили. Повторная установка Portainer запускает существующий контейнер с его данными.
