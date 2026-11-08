# راهنمای جامع Mikrozia برای MikroTik CHR

این راهنما نصب و نگهداری نسخه سازگارشده 3x-ui با **MikroTik RouterOS Container** را از صفر توضیح می‌دهد. مخاطب آن کاربری است که با Container میکروتیک آشنایی قبلی ندارد.

## مرز مسئولیت پروژه

Mikrozia فقط این بخش‌ها را مدیریت می‌کند:

- ایمیج سازگار 3x-ui/Xray برای RouterOS Container؛
- Bridge و VETH اختصاصی Container؛
- فضای پایدار دیتابیس و گواهی‌ها؛
- متغیرهای محیطی لازم؛
- ساخت، اجرا، توقف، به‌روزرسانی و عیب‌یابی Container.

Mikrozia **هیچ Rule مربوط به Firewall، NAT، Port Forward، Masquerade یا Access Control ایجاد، جابه‌جا، بررسی یا حذف نمی‌کند**. سیاست شبکه کاملاً در اختیار مدیر RouterOS باقی می‌ماند.

## نسخه مرجع

| جزء | نسخه/مقدار |
|---|---|
| RouterOS | `7.25.1` یا Stable جدیدتر |
| Container package | دقیقاً هم‌نسخه RouterOS |
| Docker image | `majlot/mikrozia:v3.11.0-mikrozia.4` |
| معماری CHR | `linux/amd64` |
| IP پیش‌فرض Container | `172.16.10.2/24` |
| Gateway پیش‌فرض | `172.16.10.1` |
| پورت داخلی پنل | `2053/TCP` |
| پورت داخلی Subscription | `2096/TCP` |

Tag `latest` برای آزمایش مناسب است؛ برای نصب واقعی همیشه Tag نسخه‌دار را استفاده کنید.

## 1. پیش‌نیازها

### منابع CHR

| منبع | حداقل عملی | پیشنهاد تولیدی |
|---|---:|---:|
| CPU | 2 vCPU | 2 تا 4 vCPU |
| RAM | 2 GiB | 4 GiB |
| فضای آزاد | 2 GiB | 8 GiB یا بیشتر |
| RouterOS | 7.25+ Stable | آخرین Stable |

برای دیدن وضعیت:

```routeros
/system/resource/print
/system/package/print
/disk/print
```

### چرا RAM و فضای اضافه لازم است؟

RouterOS هنگام دریافت `remote-image` لایه‌های OCI را در حافظه و `tmpdir` پردازش می‌کند. کمبود RAM یا دیسک می‌تواند استخراج را متوقف یا خود RouterOS را ناپایدار کند.

## 2. Backup قبل از نصب

```routeros
/system/backup/save name=before-mikrozia password="یک-رمز-قوی"
/export hide-sensitive file=before-mikrozia
```

دو فایل ایجادشده را از WinBox بخش **Files** روی کامپیوتر دانلود کنید.

## 3. نصب RouterOS و بسته Container

1. RouterOS را از کانال `stable` ارتقا دهید.
2. از وب‌سایت MikroTik، **Extra packages** همان نسخه و معماری x86 را دانلود کنید.
3. فایل `container-<version>-x86.npk` را با WinBox در Files قرار دهید.
4. CHR را reboot کنید.
5. بررسی کنید:

```routeros
/system/package/print where name="container"
```

شماره نسخه RouterOS و بسته Container باید دقیقاً یکسان باشد. برای مثال ترکیب RouterOS `7.25.1` با Container `7.25` پشتیبانی نمی‌شود.

## 4. فعال‌سازی Device Mode

```routeros
/system/device-mode/update container=yes
```

در CHR باید VM را از پنل Hypervisor یا ارائه‌دهنده کاملاً **Power Off** و دوباره **Power On** کنید. به reboot نرم داخل RouterOS اکتفا نکنید.

پس از روشن شدن:

```routeros
/system/device-mode/print
```

مقدار `container` باید `yes` باشد.

## 5. بررسی تداخل شبکه

پروفایل مبتدی از شبکه زیر استفاده می‌کند:

```text
172.16.10.0/24
RouterOS bridge: 172.16.10.1
Container:       172.16.10.2
```

ابتدا بررسی کنید این شبکه قبلاً استفاده نشده باشد:

```routeros
/ip/address/print
/ip/route/print
/interface/bridge/print
/interface/veth/print
```

اگر شبکه، Bridge یا VETH همنام وجود دارد، از اسکریپت آماده استفاده نکنید و بخش «Generator پیشرفته» را بخوانید.

## 6. روش پیشنهادی نصب از Docker Hub

ایمیج رسمی پروژه از Docker Hub دریافت می‌شود:

```text
majlot/mikrozia:v3.11.0-mikrozia.4
```

فایل زیر را روی کامپیوتر باز کنید:

[`scripts/routeros/mikrozia-clean-room-deploy.rsc`](../scripts/routeros/mikrozia-clean-room-deploy.rsc)

در حالت معمول نیازی به تغییر فایل نیست. اگر مسیر ذخیره‌سازی شما متفاوت است، فقط این مقدار را تغییر دهید:

```routeros
:local storageBase "containers"
```

فایل را با WinBox در Files آپلود و Import کنید:

```routeros
/import file-name=mikrozia-clean-room-deploy.rsc verbose=yes
```

اسکریپت فقط موارد زیر را می‌سازد:

- `br-mikrozia`؛
- `veth-mikrozia`؛
- آدرس `172.16.10.1/24` روی Bridge؛
- Mount دیتابیس `/etc/x-ui`؛
- Mount گواهی و ACME در `/root/cert`؛
- Environmentهای سازگار با RouterOS؛
- Container با Image نسخه‌دار Docker Hub.

هیچ تنظیم Firewall یا NAT در این مرحله ایجاد یا تغییر نمی‌شود.

## 7. صبر برای دریافت و استخراج Image

```routeros
/container/print detail
/log/print follow-only where topics~"container"
```

بسته به سرعت اینترنت، دیسک و CPU، Extract می‌تواند چند دقیقه زمان ببرد. تا پایان Extract، Container را حذف یا CHR را خاموش نکنید.

پس از پایان:

```routeros
/container/start [find where comment="[Mikrozia:container] Container"]
```

بررسی وضعیت:

```routeros
/container/print detail where comment="[Mikrozia:container] Container"
```

وضعیت باید `running` باشد.

## 8. مسئولیت شبکه پس از نصب

Container اکنون روی `172.16.10.2` قرار دارد، اما پروژه عمداً مسیر اینترنت، NAT، Port Forward و Rule دسترسی را ایجاد نکرده است.

مدیر RouterOS باید متناسب با طراحی شبکه خود این موارد را تعیین کند:

- Container چگونه به اینترنت خروجی داشته باشد؛
- کاربران از کدام Interface یا Route به Container برسند؛
- کدام پورت‌های داخلی منتشر شوند؛
- پنل مدیریت از کدام شبکه قابل دسترسی باشد؛
- سیاست Drop، Rate Limit و ثبت Log چگونه باشد.

پورت‌های داخلی موردنیاز را می‌توانید ببینید، اما نحوه انتشار آن‌ها در اختیار شماست:

| سرویس | مقصد داخلی |
|---|---|
| پنل مدیریت | `172.16.10.2:2053/TCP` |
| Subscription پیش‌فرض | `172.16.10.2:2096/TCP` |
| Inboundهای Xray | پورتی که داخل پنل تعریف می‌کنید |

## 9. مشاهده نام کاربری و رمز اولیه

در اولین Boot، پنل یک نام کاربری و رمز تصادفی می‌سازد و یک بار در Log نمایش می‌دهد:

```routeros
/log/print where topics~"container"
```

به دنبال `Username:` و `Password:` بگردید و آن‌ها را در Password Manager ذخیره کنید.

اگر Log اولیه از دست رفته است:

```routeros
/container/print
/container/shell 0
```

عدد `0` را با شماره واقعی Container عوض کنید. سپس داخل Shell:

```sh
/app/x-ui setting -username NEW_USER -password 'NEW_STRONG_PASSWORD'
exit
```

Container را restart کنید:

```routeros
/container/stop 0
:delay 3s
/container/start 0
```

## 10. تنظیمات اولیه پنل

پس از دسترسی شبکه‌ای به پورت داخلی پنل:

1. با اطلاعات اولیه وارد شوید.
2. رمز قوی و یکتا انتخاب کنید.
3. احراز هویت دومرحله‌ای را فعال کنید.
4. Web Base Path تصادفی تنظیم کنید.
5. تنظیمات را ذخیره و پنل را restart کنید.

## 11. دامنه و TLS

یک رکورد DNS برای IP عمومی موردنظر خود بسازید. برای RouterOS Container روش **DNS-01** پیشنهاد می‌شود، چون به بازبودن پورت خاصی هنگام صدور و تمدید وابسته نیست.

مسیر پایدار گواهی‌ها:

```text
/root/cert/<domain>/fullchain.pem
/root/cert/<domain>/privkey.pem
```

State مربوط به acme.sh نیز در این مسیر نگهداری می‌شود:

```text
/root/cert/.acme.sh
```

EntryPoint کانتینر هنگام Boot، مسیر ACME را به فضای پایدار متصل و Cron تمدید را دوباره ثبت می‌کند.

## 12. ساخت اولین Inbound

در پنل به **Inbounds → Add Inbound** بروید. نمونه تنظیم معمول:

| گزینه | مقدار نمونه |
|---|---|
| Protocol | VLESS |
| Listen IP | خالی یا `0.0.0.0` |
| Transport | TCP |
| Security | REALITY |
| Port | یک پورت آزاد انتخابی |

کلیدهای REALITY و UUID را در خود پنل تولید کنید. پس از ساخت Inbound، مدیر RouterOS تصمیم می‌گیرد ترافیک آن پورت چگونه به IP داخلی Container برسد.

## 13. فضای پایدار

این دو مسیر هنگام تعویض Image حفظ می‌شوند:

```text
containers/mikrozia/data/
containers/mikrozia/cert/
```

محتوای مهم:

- `data/x-ui.db`: کاربران، Inboundها، تنظیمات و آمار؛
- `cert/`: کلید خصوصی، Certificate و ACME state.

RootFS هر نسخه جداست:

```text
containers/mikrozia/root-<version>/
```

این طراحی اجازه می‌دهد نسخه جدید را بدون بازنویسی دیتابیس و گواهی‌ها آزمایش کنید.

## 14. Backup صحیح

برای Backup سازگار SQLite، ابتدا Container را متوقف کنید:

```routeros
/container/stop [find where comment="[Mikrozia:container] Container"]
```

پوشه‌های `data` و `cert` را از طریق WinBox یا SFTP روی سیستم دیگری کپی کنید. سپس:

```routeros
/container/start [find where comment="[Mikrozia:container] Container"]
```

فایل دیتابیس را هنگام نوشتن فعال برنامه کپی نکنید.

## 15. به‌روزرسانی به نسخه جدید

هر Release باید ابتدا روی Docker Hub منتشر شده باشد. برای ارتقا:

1. Release Notes و Tag جدید را بررسی کنید.
2. از `data` و `cert` Backup بگیرید.
3. Container قدیمی را Stop کنید.
4. یک Container جدید با Tag نسخه‌دار و `root-dir` جدید بسازید.
5. همان Mountهای پایدار را متصل کنید.
6. نسخه جدید را Start و بررسی کنید.
7. فقط پس از اطمینان، درباره حذف RootFS قدیمی تصمیم بگیرید.

از Update داخلی پنل برای جایگزین‌کردن فایل‌های RootFS استفاده نکنید؛ Image باید واحد قابل انتشار و بازتولیدپذیر باقی بماند.

## 16. انتشار هر نسخه در Docker Hub

Workflow زیر همراه پروژه است:

```text
.github/workflows/docker.yml
```

در GitHub Repository دو Secret بسازید:

```text
DOCKER_HUB_USERNAME
DOCKER_HUB_TOKEN
```

Token باید از Docker Hub ساخته شود؛ رمز حساب را در Repository قرار ندهید.

برای انتشار یک نسخه:

```bash
git tag v3.11.0-mikrozia.4
git push origin v3.11.0-mikrozia.4
```

Workflow قبل از Push این کارها را انجام می‌دهد:

1. تست‌های سازگاری Mikrozia؛
2. بررسی Syntax اسکریپت‌ها؛
3. اطمینان از نبود دستورات Firewall در Installer/Generator؛
4. Build و Smoke Test معماری amd64؛
5. Build چندمعماری؛
6. انتشار Tag نسخه‌دار و `latest`؛
7. ثبت OCI provenance، SBOM و Digest.

معماری‌های منتشرشده:

```text
linux/amd64
linux/arm64/v8
linux/arm/v7
```

هر Tag نسخه‌ای مستقل است و نباید پس از انتشار بازنویسی شود.

## 17. Generator پیشرفته

برای CHR دارای شبکه‌های موجود، چند Container یا Storage سفارشی، ابتدا Preflight را اجرا کنید:

```text
scripts/routeros/mikrozia-preflight.rsc
```

خروجی JSON را در `preflight.json` ذخیره کنید. سپس:

```bash
python3 scripts/generator/mikrozia_generator.py \
  --preflight preflight.json \
  --target-storage containers \
  --container-subnet 172.16.10.0/24 \
  --image-source registry
```

فرمان بالا Dry Run است. پس از بررسی Preview:

```bash
python3 scripts/generator/mikrozia_generator.py \
  --preflight preflight.json \
  --target-storage containers \
  --container-subnet 172.16.10.0/24 \
  --image-source registry \
  --write
```

Generator نیز هیچ Rule شبکه‌ای تولید نمی‌کند.

## 18. حذف امن

فایل زیر را Upload و Import کنید:

[`scripts/routeros/mikrozia-clean-room-teardown.rsc`](../scripts/routeros/mikrozia-clean-room-teardown.rsc)

```routeros
/import file-name=mikrozia-clean-room-teardown.rsc verbose=yes
```

این اسکریپت Container object، Mount/Environment definition و شبکه اختصاصی آن را حذف می‌کند؛ دیتابیس، Certificate و تمام تنظیمات Firewall/NAT دست‌نخورده می‌مانند.

## 19. عیب‌یابی

### Container ساخته نمی‌شود

```routeros
/system/package/print where name="container"
/system/device-mode/print
/system/resource/print
/log/print where topics~"container"
```

نسخه Package، Device Mode، RAM و فضای دیسک را بررسی کنید.

### Container در Extracting مانده است

- فضای `tmpdir` کافی نیست؛
- RAM کم است؛
- ارتباط CHR با Docker Hub مشکل دارد؛
- دیسک بسیار کند است.

```routeros
/container/print detail
/system/resource/print
/log/print follow-only where topics~"container"
```

### Container بلافاصله متوقف می‌شود

Log را بررسی کنید:

```routeros
/log/print where topics~"container"
```

موارد رایج: خرابی دیتابیس، دسترسی‌نداشتن Mount، کمبود حافظه یا اشغال‌شدن پورت داخلی توسط Inbound دیگر.

### پنل باز نمی‌شود

ابتدا مطمئن شوید خود Container `running` است و برنامه روی پورت داخلی گوش می‌دهد:

```routeros
/container/shell 0
```

داخل Container:

```sh
ps
ss -lntup
exit
```

اگر برنامه سالم است، Route و سیاست دسترسی RouterOS را جداگانه بررسی کنید؛ این پروژه آن قسمت را مدیریت نمی‌کند.

### DNS داخل Container کار نمی‌کند

```routeros
/container/print detail
```

مقدار DNS، مسیر خروجی Container و دسترسی شبکه آن را بررسی کنید.

### Certificate تمدید نمی‌شود

- Mount مربوط به `/root/cert` باید متصل باشد؛
- DNS API Token باید معتبر باشد؛
- زمان RouterOS درست باشد؛
- Logهای `MIKROZIA-ACME` را بررسی کنید.

### پنل پس از تعویض نسخه خالی است

Container جدید بدون Mount صحیح `/etc/x-ui` ساخته شده است. آن را متوقف کنید و مسیر دیتابیس قبلی را متصل کنید؛ دیتابیس جدید خالی را روی Backup کپی نکنید.

## 20. محدودیت‌های شناخته‌شده RouterOS Container

- Docker Compose معمولی مستقیماً در `/container` اجرا نمی‌شود.
- `cap_add` معادل مستقیم Docker در RouterOS ندارد.
- Image شامل Fail2ban، iptables، nftables یا UFW نیست و نمی‌تواند Firewall میزبان RouterOS را تغییر دهد.
- قابلیت IP Limit وابسته به Fail2ban در این نسخه وجود ندارد؛ مدیر RouterOS مسئول پیاده‌سازی سیاست محدودسازی IP است.
- Traffic quota و Expiry مستقل از Fail2ban هستند.
- قابلیت‌های وابسته به Kernel، TProxy یا TUN باید جداگانه روی همان RouterOS آزمایش شوند.
- روش پشتیبانی‌شده این پروژه، Inboundهای معمولی TCP/UDP در فضای کاربری Xray است.

## 21. چک‌لیست تحویل

- [ ] RouterOS از کانال Stable و نسخه 7.25+ است.
- [ ] Container package دقیقاً هم‌نسخه است.
- [ ] Device Mode پس از Cold Reboot فعال است.
- [ ] حداقل 2GiB RAM و 2GiB فضای آزاد وجود دارد.
- [ ] شبکه VETH با شبکه موجود تداخل ندارد.
- [ ] Image با Tag نسخه‌دار دریافت شده است.
- [ ] `/etc/x-ui` و `/root/cert` Mount پایدار دارند.
- [ ] Container در حالت `running` است.
- [ ] نام کاربری و رمز اولیه ذخیره شده است.
- [ ] Backup دیتابیس و گواهی‌ها تهیه شده است.
- [ ] مدیر RouterOS سیاست شبکه و دسترسی را جداگانه تنظیم کرده است.

## منابع رسمی

- [MikroTik Container](https://help.mikrotik.com/docs/spaces/ROS/pages/84901929/Container)
- [MikroTik Device Mode](https://help.mikrotik.com/docs/spaces/ROS/pages/93749258/Device-mode)
- [MikroTik CHR](https://help.mikrotik.com/docs/spaces/ROS/pages/18350234/Cloud%20Hosted%20Router%20CHR)
- [RouterOS Stable releases](https://mikrotik.com/download/changelogs?channelFilter=stable)
- [Docker Hub: majlot/mikrozia](https://hub.docker.com/r/majlot/mikrozia)
