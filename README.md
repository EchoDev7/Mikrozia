[فارسی](/README.md) | [العربية](/README.ar_EG.md) | [中文](/README.zh_CN.md) | [Español](/README.es_ES.md) | [Русский](/README.ru_RU.md) | [Türkçe](/README.tr_TR.md) | [English (upstream history)](/README.upstream.md)

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="media/mikrozia-dark.svg" />
    <source media="(prefers-color-scheme: light)" srcset="media/mikrozia-light.svg" />
    <img src="media/mikrozia-dark.svg" alt="Mikrozia" width="220" />
  </picture>
</p>

# Mikrozia — RouterOS Container Control Panel

[![Docker Hub](https://img.shields.io/badge/Docker%20Hub-echodev7%2Fmikrozia-blue.svg?logo=docker)](https://hub.docker.com/r/echodev7/mikrozia)
[![Release](https://img.shields.io/badge/release-v1.0.7--mikrozia-green.svg)](https://hub.docker.com/r/echodev7/mikrozia/tags)
[![RouterOS](https://img.shields.io/badge/RouterOS-7.25%2B-red.svg)](https://mikrotik.com)
[![License](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)

Mikrozia یک پنل کنترل مستقل برای Xray است که برای محدودیت‌ها و چرخه اجرای **MikroTik RouterOS Container** آماده شده است.

## راهنمای فارسی

[راهنمای جامع نصب، راه‌اندازی، به‌روزرسانی و عیب‌یابی CHR](docs/MIKROZIA_CHR_INSTALL_FA.md)

## محدوده پروژه

این پروژه موارد زیر را فراهم می‌کند:

- Image چندمعماری قابل دریافت از Docker Hub؛
- مسیرهای پایدار دیتابیس و گواهی TLS (گواهی با کلاینت ACME داخلی صادر و تمدید می‌شود)؛
- EntryPoint سازگار با RootFS موقت Container؛
- حذف کامل ابزارها و دستورات Firewall از Image کانتینری؛
- اسکریپت‌های Container-only برای نصب و حذف؛
- Preflight و Generator تشخیص معماری، RAM، Storage و تداخل شبکه؛
- انتشار خودکار هر نسخه در Docker Hub.

Mikrozia هیچ Rule مربوط به Firewall، NAT، Port Forward، Masquerade یا Access Control تولید یا تغییر نمی‌دهد. مدیر RouterOS مالک کامل سیاست شبکه است.

## نسخه فعلی

```text
echodev7/mikrozia:v1.0.7-mikrozia
```

معماری‌های انتشار:

- `linux/amd64` برای CHR؛
- `linux/arm64/v8`؛
- `linux/arm/v7`.

این سه معماری در CI برای همان Platform ساخته، اجرا و با قرارداد Runtime (نسخه‌ها، Mountها، Geo و نبود ابزار Firewall) بررسی می‌شوند. CHR x86_64 محیط مرجع و مسیر نصب مبتدی است؛ اعتبارسنجی نهایی روی سخت‌افزار RouterBOARD بخشی از ماتریس آزمون فاز ۱ است.

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
3. دستورهای زیر را به ترتیب در Terminal RouterOS یا WinBox اجرا کنید.

> این مسیر با قرارداد فعلی Container در RouterOS `7.25` هم‌راستا است. پذیرش روی
> CHR تمیز برای P1-CHR-01 هنوز کامل نشده است؛ پیش از Production کل مسیر را روی
> یک CHR موقت آزمایش کنید. این مستند ادعای پذیرش واقعی CHR ندارد.

اگر `172.16.10.0/24`، نام Bridge/VETH یا مسیر ذخیره‌سازی پیش‌فرض آزاد نیست،
مقادیر را در همهٔ دستورها یکسان تغییر دهید. اشیای Mikrozia با
`[Mikrozia:container]` علامت‌گذاری می‌شوند.

ساخت پوشه‌های پایدار و موقت:

```routeros
/file/add name=containers type=directory
/file/add name=containers/mikrozia type=directory
/file/add name=containers/mikrozia/data type=directory
/file/add name=containers/mikrozia/cert type=directory
/file/add name=containers/mikrozia/tmp type=directory
```

اگر پوشه‌ای از قبل وجود دارد، خط `/file/add` آن را اجرا نکنید. پوشهٔ Root نسخه‌دار
را RouterOS هنگام Extract می‌سازد.

ساخت Bridge و VETH:

```routeros
/interface/bridge/add name=br-mikrozia comment="[Mikrozia:container] Bridge"
/ip/address/add address=172.16.10.1/24 interface=br-mikrozia comment="[Mikrozia:container] Gateway"
/interface/veth/add name=veth-mikrozia address=172.16.10.2/24 gateway=172.16.10.1 comment="[Mikrozia:container] VETH"
/interface/bridge/port/add bridge=br-mikrozia interface=veth-mikrozia comment="[Mikrozia:container] Bridge Port"
```

Mountهای پایدار و متغیرهای محیطی:

```routeros
/container/mounts/add list=MIKROZIA_DATA src=containers/mikrozia/data dst="/etc/x-ui" comment="[Mikrozia:container] Data Mount"
/container/mounts/add list=MIKROZIA_CERT src=containers/mikrozia/cert dst="/root/cert" comment="[Mikrozia:container] Cert Mount"
/container/envs/add list=MIKROZIA_ENV key=XUI_PORT value="2053" comment="[Mikrozia:container] Environment"
/container/envs/add list=MIKROZIA_ENV key=HOME value="/root" comment="[Mikrozia:container] Environment"
/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_ADMIN_USERNAME value="YOUR_ADMIN_USERNAME" comment="[Mikrozia:container] Environment"
/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_ADMIN_PASSWORD value="YOUR_STRONG_PASSWORD" comment="[Mikrozia:container] Environment"
```

> **هشدار حساسیت:** Environment کانتینر RouterOS یک انبار پیکربندی است، نه انبار
> اسرار. هر مقدار در `/container/envs` برای هر کسی که بتواند پیکربندی روتر را
> بخواند یا خروجی بگیرد قابل مشاهده است. `MIKROZIA_ADMIN_USERNAME` و
> `MIKROZIA_ADMIN_PASSWORD` مرجع اصلی حساب مدیر هستند و باید **با هم** تنظیم شوند؛
> اگر فقط یکی از آن‌ها موجود باشد پنل اجرا نمی‌شود. Mikrozia این مقادیر را در Log
> چاپ نمی‌کند و از طریق API برنمی‌گرداند.

تنظیم Registry و ساخت Container با Image نسخه‌دار:

```routeros
/container/config/set registry-url="https://registry-1.docker.io" tmpdir=containers/mikrozia/tmp
/container/add remote-image="echodev7/mikrozia:v1.0.7-mikrozia" interface=veth-mikrozia root-dir=containers/mikrozia/root-v1.0.7-mikrozia mountlists="MIKROZIA_DATA,MIKROZIA_CERT" envlist=MIKROZIA_ENV dns="1.1.1.1,8.8.8.8" memory-high=512M memory-max=1024M logging=yes start-on-boot=yes comment="[Mikrozia:container] Container"
```

Monitor استخراج:

```routeros
/container/print detail
/log/print follow-only where topics~"container"
```

پس از پایان Extract:

```routeros
/container/start [find where comment="[Mikrozia:container] Container"]
```

این دستورها فقط Bridge/VETH، Mountها، Environment و Container را ایجاد می‌کنند و
هیچ Rule مربوط به Firewall، NAT، Port Forward یا Routing نمی‌سازند.

## قرارداد Runtime

| مورد | مقدار |
|---|---|
| دیتابیس و تنظیمات | `/etc/x-ui` |
| گواهی TLS و وضعیت ACME | `/root/cert` |
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

### مدیریت Lifecycle و تفاوت `x-ui` با RouterOS

- دستورهای `x-ui start`، `x-ui stop`، `x-ui enable` و `x-ui disable` داخل کانتینر تغییری در کانتینر ایجاد نمی‌کنند و دستور معادل RouterOS را با شناسه `[find where comment="[Mikrozia:container] Container"]` (از جمله `/container/start`، `/container/stop` و `/container/set ... start-on-boot=yes|no restart-policy=always|no`) نمایش می‌دهند.
- `x-ui restart` سرویس وب پنل (`SIGHUP`) را Reload و Xray (`SIGUSR1`) را داخل کانتینر Restart می‌کند و خود کانتینر را متوقف یا ری‌استارت نمی‌کند؛ `x-ui restart-xray` نیز فقط Xray را ری‌استارت می‌کند.
- برای Restart کامل خود کانتینر از Terminal یا WinBox اجرا کنید: `/container/restart [find where comment="[Mikrozia:container] Container"]`.

## سفارشی‌سازی مقادیر

اگر نام Bridge/VETH، Subnet، پورت داخلی یا مسیر ذخیره‌سازی را تغییر می‌دهید،
مقادیر مرتبط را در همهٔ دستورهای نصب یکسان به‌روزرسانی کنید؛ از جمله مبدأ
Mountها، `mountlists` و `root-dir`. Commentهای `[Mikrozia:container]` را حفظ کنید
تا دستورهای Lifecycle و حذف بتوانند اشیای متعلق به Mikrozia را پیدا کنند. از
Subnet یا نام‌های متداخل استفاده نکنید و هیچ Rule شبکه‌ای به Mikrozia نسبت داده
نشود؛ سیاست Firewall/NAT همچنان در اختیار مدیر RouterOS است.

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
v<major>-mikrozia
```

نمونه:

```bash
git tag v1.0.7-mikrozia
git push origin v1.0.7-mikrozia
```

Workflow تست‌ها و Smoke Test را برای **هر سه معماری منتشرشده** اجرا می‌کند و سپس Imageهای `amd64`، `arm64` و `arm/v7` را همراه SBOM، provenance و Digest در `echodev7/mikrozia` منتشر می‌کند. انتشار فقط از روی Tag نسخه‌دار یا Dispatch صریح همان نسخه انجام می‌شود؛ Push روی `main` یا Pull Request هیچ Image‌ای منتشر نمی‌کند.

هر نسخه فقط به‌صورت Image نسخه‌دار OCI منتشر می‌شود. برای نسخه‌های Mikrozia هیچ Tarball باینری، Package سیستم‌عامل یا Installer مبتنی بر systemd به‌عنوان Artifact انتشار ساخته یا منتشر نمی‌شود؛ مسیر پشتیبانی‌شده همان Image نسخه‌دار و Mountهای پایدار است.

### ساخت محلی Docker Image

برای ساخت محلی Docker Image باید نسخه‌های `MIKROZIA_VERSION`، `XRAY_VERSION` و `MTG_VERSION` را از فایل‌های ریشه مخزن بخوانید و به‌عنوان Build Arg ارسال کنید:

```bash
docker build \
  --build-arg MIKROZIA_VERSION="$(cat MIKROZIA_VERSION)" \
  --build-arg XRAY_VERSION="$(tr -d '[:space:]' < XRAY_VERSION)" \
  --build-arg MTG_VERSION="$(tr -d '[:space:]' < MTG_VERSION)" \
  -t mikrozia:local .
```

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
bash -n DockerEntrypoint.sh
sh -n DockerInit.sh
bash -n x-ui.sh
```

## Upstream و مجوز

- Upstream: [EchoDev7/Mikrozia](https://github.com/EchoDev7/Mikrozia) (forked from MHSanaei/3x-ui v3.11.0)
- Xray: [XTLS/Xray-core](https://github.com/XTLS/Xray-core)
- License: GPL-3.0
