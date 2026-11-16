[فارسی](/README.md) | [العربية](/README.ar_EG.md) | [中文](/README.zh_CN.md) | [Español](/README.es_ES.md) | [Русский](/README.ru_RU.md) | [Türkçe](/README.tr_TR.md) | [English (upstream history)](/README.upstream.md)

# Mikrozia — 3x-ui для контейнера RouterOS

Mikrozia упаковывает 3x-ui и Xray для **контейнера MikroTik RouterOS**.
Единственный поддерживаемый путь установки и обновления — версионируемый
OCI-образ с постоянными монтированиями. Полное руководство находится в
[README.md](/README.md) (на персидском).

## Текущая версия

```text
majlot/mikrozia:v1-mikrozia
```

В production используйте версионируемый тег, а не `latest`.

## Требования

- RouterOS `7.25.0` или новее из канала Stable с пакетом Container той же версии;
- Device Mode с `container=yes` и холодная перезагрузка;
- не менее 2 GiB RAM и 2 GiB свободного места;
- CHR x86_64 для базового пути установки.

## Установка

Выполняйте команды по порядку в терминале RouterOS или WinBox. Production-образ —
версионированный тег `majlot/mikrozia:v1-mikrozia`.

> Эти инструкции соответствуют текущему контракту контейнеров RouterOS `7.25`.
> Приёмка на чистом CHR для P1-CHR-01 ещё не завершена; проверьте весь путь на
> одноразовом CHR до production. Этот документ не заявляет реальную приёмку на
> CHR.

```routeros
/file/add name=containers type=directory
/file/add name=containers/mikrozia type=directory
/file/add name=containers/mikrozia/data type=directory
/file/add name=containers/mikrozia/cert type=directory
/file/add name=containers/mikrozia/tmp type=directory
/interface/bridge/add name=br-mikrozia comment="[Mikrozia:container] Bridge"
/ip/address/add address=172.16.10.1/24 interface=br-mikrozia comment="[Mikrozia:container] Gateway"
/interface/veth/add name=veth-mikrozia address=172.16.10.2/24 gateway=172.16.10.1 comment="[Mikrozia:container] VETH"
/interface/bridge/port/add bridge=br-mikrozia interface=veth-mikrozia comment="[Mikrozia:container] Bridge Port"
/container/mounts/add list=MIKROZIA_DATA src=containers/mikrozia/data dst="/etc/x-ui" comment="[Mikrozia:container] Data Mount"
/container/mounts/add list=MIKROZIA_CERT src=containers/mikrozia/cert dst="/root/cert" comment="[Mikrozia:container] Cert Mount"
/container/envs/add list=MIKROZIA_ENV key=XUI_PORT value="2053" comment="[Mikrozia:container] Environment"
/container/envs/add list=MIKROZIA_ENV key=HOME value="/root" comment="[Mikrozia:container] Environment"
/container/config/set registry-url="https://registry-1.docker.io" tmpdir=containers/mikrozia/tmp
/container/add remote-image="majlot/mikrozia:v1-mikrozia" interface=veth-mikrozia root-dir=containers/mikrozia/root-v1-mikrozia mountlists="MIKROZIA_DATA,MIKROZIA_CERT" envlist=MIKROZIA_ENV dns="1.1.1.1,8.8.8.8" memory-high=512M memory-max=1024M logging=yes start-on-boot=yes comment="[Mikrozia:container] Container"
```

Если каталог уже существует, пропустите его строку `/file/add`. Для контроля
распаковки:

```routeros
/container/print detail
/log/print follow-only where topics~"container"
```

После завершения извлечения образа:

```routeros
/container/start [find where comment="[Mikrozia:container] Container"]
```

Установка создаёт только Bridge/VETH, монтирования, переменные окружения и
контейнер; правила firewall, NAT, port forwarding и routing не создаются.

## Доступ и данные

- Панель слушает внутри контейнера на `http://172.16.10.2:2053/`.
- База данных и настройки находятся на постоянном монтировании `/etc/x-ui`,
  сертификаты пользователя — на `/root/cert`.
- Получайте и продлевайте сертификат TLS вне контейнера и указывайте его путь в
  панели; Mikrozia не выпускает и не продлевает сертификаты.
- Политика брандмауэра, NAT, проброса портов, маршрутизации и контроля доступа
  полностью принадлежит администратору RouterOS; Mikrozia не создаёт и не меняет
  сетевые правила.

## Обновление

Загрузите новый версионируемый образ, пересоздайте контейнер с теми же
монтированиями `/etc/x-ui` и `/root/cert` и сохраняйте предыдущий образ до
проверки нового. Обновления ПО внутри контейнера нет, а RootFS образа нельзя
перезаписывать изнутри.

## Upstream и лицензия

- Upstream: [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui)
- Xray: [XTLS/Xray-core](https://github.com/XTLS/Xray-core)
- License: GPL-3.0
