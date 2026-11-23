[فارسی](/README.md) | [العربية](/README.ar_EG.md) | [中文](/README.zh_CN.md) | [Español](/README.es_ES.md) | [Русский](/README.ru_RU.md) | [Türkçe](/README.tr_TR.md) | [English (upstream history)](/README.upstream.md)

# Mikrozia — panneaux کنترل کانتینر RouterOS

Mikrozia بسته‌ای از Mikrozia و Xray برای **MikroTik RouterOS Container** است. تنها
مسیر نصب و به‌روزرسانی پشتیبانی‌شده، Image نسخه‌دار OCI همراه با Mountهای پایدار
است. راهنمای کامل فارسی در [README.md](/README.md) قرار دارد.

## نسخه فعلی

```text
echodev7/mikrozia:v1-mikrozia
```

برای محیط تولید از Tag نسخه‌دار استفاده کنید، نه `latest`.

## نیازمندی

- RouterOS `7.25.0` یا جدیدتر از کانال Stable و بسته Container دقیقاً هم‌نسخه؛
- Device Mode با `container=yes` و یک Cold Reboot؛
- حداقل 2GiB RAM و 2GiB فضای آزاد؛
- CHR x86_64 برای مسیر نصب مبتدی.

## نصب

دستورهای زیر را به ترتیب در Terminal RouterOS یا WinBox اجرا کنید. Image تولیدی
همان Tag نسخه‌دار `echodev7/mikrozia:v1-mikrozia` است.

> این مسیر با قرارداد فعلی Container در RouterOS `7.25` هم‌راستا است. پذیرش روی
> CHR تمیز برای P1-CHR-01 هنوز کامل نشده است؛ پیش از Production روی CHR موقت
> آزمایش کنید. این مستند ادعای پذیرش واقعی CHR ندارد.

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
/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_ADMIN_USERNAME value="YOUR_ADMIN_USERNAME" comment="[Mikrozia:container] Environment"
/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_ADMIN_PASSWORD value="YOUR_STRONG_PASSWORD" comment="[Mikrozia:container] Environment"
/container/config/set registry-url="https://registry-1.docker.io" tmpdir=containers/mikrozia/tmp
/container/add remote-image="echodev7/mikrozia:v1-mikrozia" interface=veth-mikrozia root-dir=containers/mikrozia/root-v1-mikrozia mountlists="MIKROZIA_DATA,MIKROZIA_CERT" envlist=MIKROZIA_ENV dns="1.1.1.1,8.8.8.8" memory-high=512M memory-max=1024M logging=yes start-on-boot=yes comment="[Mikrozia:container] Container"
```

> **هشدار حساسیت:** Environment کانتینر RouterOS یک انبار پیکربندی است، نه انبار
> اسرار. هر مقدار در `/container/envs` برای هر کسی که بتواند پیکربندی روتر را
> بخواند یا خروجی بگیرد قابل مشاهده است. `MIKROZIA_ADMIN_USERNAME` و
> `MIKROZIA_ADMIN_PASSWORD` مرجع اصلی حساب مدیر هستند و باید **با هم** تنظیم شوند؛
> اگر فقط یکی از آن‌ها موجود باشد پنل اجرا نمی‌شود. Mikrozia این مقادیر را در Log
> چاپ نمی‌کند و از طریق API برنمی‌گرداند.

اگر پوشه‌ای از قبل وجود دارد، خط `/file/add` آن را اجرا نکنید. برای Extract:

```routeros
/container/print detail
/log/print follow-only where topics~"container"
```

پس از پایان Extract:

```routeros
/container/start [find where comment="[Mikrozia:container] Container"]
```

نصب فقط Bridge/VETH، Mountها، Environment و Container را می‌سازد و هیچ Rule
Firewall، NAT، Port Forward یا Routing ایجاد نمی‌کند.

## دسترسی و داده

- پنل داخل کانتینر روی `http://172.16.10.2:2053/` شنود می‌کند.
- دیتابیس و تنظیمات روی Mount پایدار `/etc/x-ui` و گواهی‌های کاربر روی `/root/cert` قرار دارند.
- Image یک کلاینت ACME با نسخهٔ Pin‌شده دارد: گواهی را داخل کانتینر با HTTP-01 (تمدید دستی) یا Cloudflare DNS-01 (تمدید خودکار) صادر کنید. گواهی و وضعیت حساب روی `/root/cert` پایدار می‌مانند. Mikrozia هرگز Rule فایروال، NAT یا Port Forward ایجاد یا تغییر نمی‌دهد.
- سیاست Firewall، NAT، Port Forward، Routing و Access Control کاملاً در اختیار مدیر RouterOS است؛ Mikrozia هیچ Rule شبکه‌ای ایجاد یا تغییر نمی‌دهد.

## به‌روزرسانی

Image نسخه‌دار جدید را دریافت کنید، کانتینر را با همان Mountهای `/etc/x-ui` و
`/root/cert` دوباره بسازید و Image قبلی را تا تأیید نسخه جدید نگه دارید. به‌روزرسان
نرم‌افزاری داخل کانتینر وجود ندارد و RootFS ایمیج نباید از داخل بازنویسی شود.

## Upstream و مجوز

- Upstream: [EchoDev7/Mikrozia](https://github.com/EchoDev7/Mikrozia) (forked from MHSanaei/3x-ui v3.11.0)
- Xray: [XTLS/Xray-core](https://github.com/XTLS/Xray-core)
- License: GPL-3.0
