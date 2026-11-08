# Mikrozia — 3x-ui for MikroTik RouterOS Container

[![Docker Hub](https://img.shields.io/badge/Docker%20Hub-majlot%2Fmikrozia-blue.svg?logo=docker)](https://hub.docker.com/r/majlot/mikrozia)
[![Release](https://img.shields.io/badge/release-v3.11.0--mikrozia.4-green.svg)](https://hub.docker.com/r/majlot/mikrozia/tags)
[![RouterOS](https://img.shields.io/badge/RouterOS-7.25%2B-red.svg)](https://mikrotik.com)
[![License](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)

Mikrozia بسته‌ای از 3x-ui و Xray است که برای محدودیت‌ها و چرخه اجرای **MikroTik RouterOS Container** آماده شده است.

## راهنمای فارسی

[راهنمای جامع نصب، راه‌اندازی، به‌روزرسانی و عیب‌یابی CHR](docs/MIKROZIA_CHR_INSTALL_FA.md)

## محدوده پروژه

این پروژه موارد زیر را فراهم می‌کند:

- Image چندمعماری قابل دریافت از Docker Hub؛
- مسیرهای پایدار دیتابیس، Certificate و ACME state؛
- EntryPoint سازگار با RootFS موقت Container؛
- Cron تمدید Certificate؛
- حذف کامل ابزارها و دستورات Firewall از Image کانتینری؛
- اسکریپت‌های Container-only برای نصب و حذف؛
- Preflight و Generator تشخیص معماری، RAM، Storage و تداخل شبکه؛
- انتشار خودکار هر نسخه در Docker Hub.

Mikrozia هیچ Rule مربوط به Firewall، NAT، Port Forward، Masquerade یا Access Control تولید یا تغییر نمی‌دهد. مدیر RouterOS مالک کامل سیاست شبکه است.

## نسخه فعلی

```text
majlot/mikrozia:v3.11.0-mikrozia.4
```

معماری‌های انتشار:

- `linux/amd64` برای CHR؛
- `linux/arm64/v8`؛
- `linux/arm/v7`.

برای محیط Production از Tag نسخه‌دار استفاده کنید، نه `latest`.

## نیازمندی CHR

- RouterOS `7.25.0` یا جدیدتر از کانال Stable؛
- بسته Container دقیقاً هم‌نسخه RouterOS؛
- Device Mode با `container=yes`؛
- حداقل 2GiB RAM؛
- حداقل 2GiB فضای آزاد؛
- CHR x86_64 برای مسیر نصب مبتدی.

## نصب سریع

1. RouterOS و بسته Container هم‌نسخه را نصب کنید.
2. Device Mode را فعال و CHR را Cold Reboot کنید.
3. فایل زیر را با WinBox آپلود کنید:

```text
scripts/routeros/mikrozia-clean-room-deploy.rsc
```

4. Import کنید:

```routeros
/import file-name=mikrozia-clean-room-deploy.rsc verbose=yes
```

5. Extract را دنبال کنید:

```routeros
/container/print detail
/log/print follow-only where topics~"container"
```

6. پس از پایان Extract:

```routeros
/container/start [find where comment="[Mikrozia:container] Container"]
```

نصب‌کننده فقط Bridge/VETH، Mountها، Environment و Container را ایجاد می‌کند.

## قرارداد Runtime

| مورد | مقدار |
|---|---|
| دیتابیس و تنظیمات | `/etc/x-ui` |
| گواهی و ACME | `/root/cert` |
| HOME | `/root` |
| پنل داخلی | `2053/TCP` |
| Subscription داخلی | `2096/TCP` |
| ابزار Firewall داخل Image | وجود ندارد |
| Log | stdout/stderr → RouterOS log |
| Start on boot | فعال |
| Auto restart | `10s` |
| Memory high/max | `512M / 1024M` |

نمونه مسیر نهایی TLS برای دامنه `vpn.yourdomain.com`:

```text
/root/cert/vpn.yourdomain.com/fullchain.pem
/root/cert/vpn.yourdomain.com/privkey.pem
```

## Generator پیشرفته

Preflight روی RouterOS:

```text
scripts/routeros/mikrozia-preflight.rsc
```

Generator روی کامپیوتر:

```bash
python3 scripts/generator/mikrozia_generator.py \
  --preflight preflight.json \
  --target-storage containers \
  --container-subnet 172.16.10.0/24
```

فرمان بالا Dry Run است. برای نوشتن فایل‌ها `--write` اضافه کنید. Generator نیز هیچ دستور Firewall/NAT تولید نمی‌کند.

## انتشار نسخه‌ها در Docker Hub

Workflow انتشار:

```text
.github/workflows/docker.yml
```

Secrets لازم در GitHub:

```text
DOCKER_HUB_USERNAME
DOCKER_HUB_TOKEN
```

فرمت Tag انتشار:

```text
v<upstream-version>-mikrozia.<revision>
```

نمونه:

```bash
git tag v3.11.0-mikrozia.4
git push origin v3.11.0-mikrozia.4
```

Workflow تست‌ها و Smoke Test amd64 را اجرا می‌کند و سپس Imageهای `amd64`، `arm64` و `arm/v7` را همراه SBOM، provenance و Digest در `majlot/mikrozia` منتشر می‌کند.

## به‌روزرسانی

دیتابیس و Certificate روی Mountهای پایدار قرار دارند. هر نسخه باید RootFS جداگانه داشته باشد. روند صحیح:

1. Backup از `data` و `cert`؛
2. Stop نسخه فعلی؛
3. ساخت Container با Tag و RootFS جدید؛
4. اتصال Mountهای قبلی؛
5. تست نسخه جدید؛
6. نگهداری نسخه قبلی تا تأیید نهایی.

از Update داخلی برای بازنویسی RootFS استفاده نکنید.

## محدودیت‌ها

- Image شامل Fail2ban، iptables، nftables یا UFW نیست و هیچ Rule شبکه‌ای تولید نمی‌کند.
- IP Limit وابسته به Fail2ban در این نسخه موجود نیست؛ سیاست محدودسازی IP باید توسط مدیر RouterOS پیاده‌سازی شود.
- Traffic quota و Expiry مستقل از Fail2ban هستند.
- Docker Compose معمولی مستقیماً در `/container` اجرا نمی‌شود.
- TUN، TProxy و قابلیت‌های وابسته به Kernel به آزمون جداگانه نیاز دارند.

## تست

```bash
python3 -m unittest discover -s tests -p 'test_*.py'
sh -n DockerEntrypoint.sh
sh -n DockerInit.sh
bash -n x-ui.sh
```

## Upstream و مجوز

- Upstream: [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui)
- Xray: [XTLS/Xray-core](https://github.com/XTLS/Xray-core)
- License: GPL-3.0
