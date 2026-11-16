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

| جزء                     | نسخه/مقدار                    |
| ----------------------- | ----------------------------- |
| RouterOS                | `7.25.1` یا Stable جدیدتر     |
| Container package       | دقیقاً هم‌نسخه RouterOS         |
| Docker image            | `majlot/mikrozia:v1-mikrozia` |
| معماری CHR              | `linux/amd64`                 |
| IP پیش‌فرض Container     | `172.16.10.2/24`              |
| Gateway پیش‌فرض          | `172.16.10.1`                 |
| پورت داخلی پنل          | `2053/TCP`                    |
| پورت داخلی Subscription | `2096/TCP`                    |

Tag `latest` برای آزمایش مناسب است؛ برای نصب واقعی همیشه Tag نسخه‌دار را استفاده کنید.

## 1. پیش‌نیازها

### منابع CHR

| منبع      |   حداقل عملی | پیشنهاد تولیدی |
| --------- | -----------: | -------------: |
| CPU       |       2 vCPU |    2 تا 4 vCPU |
| RAM       |        2 GiB |          4 GiB |
| فضای آزاد |        2 GiB | 8 GiB یا بیشتر |
| RouterOS  | 7.25+ Stable |   آخرین Stable |

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

اگر این Subnet، نام Bridge یا نام VETH قبلاً استفاده شده است، از همان مقدار یا
نام دوباره استفاده نکنید. مقادیر آزاد انتخاب کنید و آن‌ها را در همهٔ دستورهای
نصب یکسان اعمال کنید.

## 6. نصب Image نسخه‌دار

ایمیج رسمی پروژه از Docker Hub دریافت می‌شود:

```text
majlot/mikrozia:v1-mikrozia
```

> این مسیر با قرارداد فعلی Container در RouterOS `7.25` هم‌راستا است. پذیرش روی
> CHR تمیز برای P1-CHR-01 هنوز کامل نشده است؛ پیش از Production کل مسیر را روی
> یک CHR موقت آزمایش کنید. این مستند ادعای پذیرش واقعی CHR ندارد.

دستورهای زیر را به ترتیب در Terminal RouterOS یا WinBox اجرا کنید. فقط اشیای
متعلق به Mikrozia ساخته می‌شوند و با `[Mikrozia:container]` علامت‌گذاری شده‌اند.

### 6.1 ساخت پوشه‌های پایدار و موقت

پوشهٔ `data` به `/etc/x-ui`، پوشهٔ `cert` به `/root/cert` و پوشهٔ `tmp` برای
فضای موقت Extract متصل می‌شوند. پوشهٔ Root نسخه‌دار را RouterOS هنگام Extract
می‌سازد. اگر پوشه‌ای از قبل وجود دارد، خط `/file/add` مربوط به آن را اجرا نکنید.

```routeros
/file/add name=containers type=directory
/file/add name=containers/mikrozia type=directory
/file/add name=containers/mikrozia/data type=directory
/file/add name=containers/mikrozia/cert type=directory
/file/add name=containers/mikrozia/tmp type=directory
```

### 6.2 ساخت Bridge و VETH

```routeros
/interface/bridge/add name=br-mikrozia comment="[Mikrozia:container] Bridge"
/ip/address/add address=172.16.10.1/24 interface=br-mikrozia comment="[Mikrozia:container] Gateway"
/interface/veth/add name=veth-mikrozia address=172.16.10.2/24 gateway=172.16.10.1 comment="[Mikrozia:container] VETH"
/interface/bridge/port/add bridge=br-mikrozia interface=veth-mikrozia comment="[Mikrozia:container] Bridge Port"
```

### 6.3 افزودن Mountها و متغیرهای محیطی

```routeros
/container/mounts/add list=MIKROZIA_DATA src=containers/mikrozia/data dst="/etc/x-ui" comment="[Mikrozia:container] Data Mount"
/container/mounts/add list=MIKROZIA_CERT src=containers/mikrozia/cert dst="/root/cert" comment="[Mikrozia:container] Cert Mount"
/container/envs/add list=MIKROZIA_ENV key=XUI_PORT value="2053" comment="[Mikrozia:container] Environment"
/container/envs/add list=MIKROZIA_ENV key=HOME value="/root" comment="[Mikrozia:container] Environment"
```

### 6.4 تنظیم Registry و ساخت Container

`/container/config/set` فقط Registry و پوشهٔ Extract را تنظیم می‌کند. Tag
نسخه‌دار در `/container/add` ثابت است و `memory-high`، `memory-max` و
`start-on-boot` مطابق قرارداد Runtime تنظیم شده‌اند.

```routeros
/container/config/set registry-url="https://registry-1.docker.io" tmpdir=containers/mikrozia/tmp
/container/add remote-image="majlot/mikrozia:v1-mikrozia" interface=veth-mikrozia root-dir=containers/mikrozia/root-v1-mikrozia mountlists="MIKROZIA_DATA,MIKROZIA_CERT" envlist=MIKROZIA_ENV dns="1.1.1.1,8.8.8.8" memory-high=512M memory-max=1024M logging=yes start-on-boot=yes comment="[Mikrozia:container] Container"
```

این دستورها فقط Bridge/VETH، Mountها، Environment و Container را ایجاد می‌کنند و
هیچ تنظیم Firewall، NAT، Port Forward یا Routing ایجاد یا تغییر نمی‌دهند.

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

| سرویس               | مقصد داخلی                     |
| ------------------- | ------------------------------ |
| پنل مدیریت          | `172.16.10.2:2053/TCP`         |
| Subscription پیش‌فرض | `172.16.10.2:2096/TCP`         |
| Inboundهای Xray     | پورتی که داخل پنل تعریف می‌کنید |

## 9. مشاهده نام کاربری و رمز اولیه

در اولین Boot، پنل یک نام کاربری و رمز تصادفی می‌سازد و یک بار در Log نمایش می‌دهد:

```routeros
/log/print where topics~"container"
```

به دنبال `Username:` و `Password:` بگردید و آن‌ها را در Password Manager ذخیره کنید.

اگر Log اولیه از دست رفته است:

```routeros
/container/shell [find where comment="[Mikrozia:container] Container"]
```

سپس داخل Shell:

```sh
/app/x-ui setting -username NEW_USER -password 'NEW_STRONG_PASSWORD'
exit
```

برای اعمال تغییر تنظیمات پنل بدون ری‌استارت کل Container می‌توانید داخل Shell از `x-ui restart` (بارگذاری مجدد وب‌سرور پنل و ری‌استارت Xray) استفاده کنید، یا در RouterOS کل Container را ری‌استارت نمایید:

```routeros
/container/restart [find where comment="[Mikrozia:container] Container"]
```

> **نکته Lifecycle در RouterOS Container**: دستورهای `x-ui start`، `x-ui stop`، `x-ui enable` و `x-ui disable` داخل کانتینر هیچ تغییری در وضعیت کانتینر ایجاد نمی‌کنند و تنها دستور معادل RouterOS (از جمله `/container/start`، `/container/stop` و `/container/set ... start-on-boot=yes|no restart-policy=always|no`) را نمایش می‌دهند. دستور `x-ui restart` نیز تنها سرویس وب پنل و Xray را داخل کانتینر ری‌لود/ری‌استارت می‌کند و خود Container را خاموش یا ری‌استارت نمی‌کند.

## 10. تنظیمات اولیه پنل

پس از دسترسی شبکه‌ای به پورت داخلی پنل:

1. با اطلاعات اولیه وارد شوید.
2. رمز قوی و یکتا انتخاب کنید.
3. احراز هویت دومرحله‌ای را فعال کنید.
4. Web Base Path تصادفی تنظیم کنید.
5. تنظیمات را ذخیره و پنل را restart کنید.

## 11. دامنه و TLS

یک رکورد DNS برای IP عمومی موردنظر خود بسازید. Mikrozia هیچ گواهی‌ای را داخل کانتینر صادر یا تمدید نمی‌کند؛ گواهی را با روش مرجع گواهی خود بیرون از کانتینر تهیه و تمدید کنید.

مسیر پایدار گواهی‌ها:

```text
/root/cert/<domain>/fullchain.pem
/root/cert/<domain>/privkey.pem
```

فایل‌ها را روی Mount پایدار `/root/cert` قرار دهید و مسیر آن‌ها را از منوی `x-ui` (گزینه 14) یا با `x-ui cert` به پنل معرفی کنید. نه ابزار ACME و نه Cron تمدید در کانتینر وجود ندارد.

## 12. ساخت اولین Inbound

در پنل به **Inbounds → Add Inbound** بروید. نمونه تنظیم معمول:

| گزینه     | مقدار نمونه          |
| --------- | -------------------- |
| Protocol  | VLESS                |
| Listen IP | خالی یا `0.0.0.0`    |
| Transport | TCP                  |
| Security  | REALITY              |
| Port      | یک پورت آزاد انتخابی |

کلیدهای REALITY و UUID را در خود پنل تولید کنید. پس از ساخت Inbound، مدیر RouterOS تصمیم می‌گیرد ترافیک آن پورت چگونه به IP داخلی Container برسد.

## 13. فضای پایدار

این دو مسیر هنگام تعویض Image حفظ می‌شوند:

```text
containers/mikrozia/data/
containers/mikrozia/cert/
```

محتوای مهم:

- `data/x-ui.db`: کاربران، Inboundها، تنظیمات و آمار؛
- `cert/`: کلید خصوصی و Certificateهای ارائه‌شده توسط کاربر.

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
git tag v1-mikrozia
git push origin v1-mikrozia
```

Workflow قبل از Push این کارها را انجام می‌دهد:

1. تست‌های سازگاری Mikrozia؛
2. بررسی Syntax اسکریپت‌ها؛
3. اطمینان از نبود دستورات Firewall در Installer/Generator؛
4. Build، Start و Smoke Test قرارداد Runtime برای هر سه معماری؛
5. Build چندمعماری و انتشار Tag نسخه‌دار و `latest`؛
6. ثبت OCI provenance، SBOM و Digest.

معماری‌های منتشرشده:

```text
linux/amd64
linux/arm64/v8
linux/arm/v7
```

هر Tag نسخه‌ای مستقل است و نباید پس از انتشار بازنویسی شود. انتشار فقط از روی Tag نسخه‌دار یا Dispatch صریح همان نسخه انجام می‌شود و Push روی `main` یا Pull Request Image‌ای منتشر نمی‌کند.

تنها Artifact انتشار، Image نسخه‌دار OCI است. برای نسخه‌های Mikrozia هیچ Tarball باینری، Package سیستم‌عامل یا Installer مبتنی بر systemd منتشر نمی‌شود.

## 17. سفارشی‌سازی مقادیر نصب

اگر نام Bridge/VETH، Subnet، پورت داخلی یا مسیر ذخیره‌سازی پیش‌فرض با تنظیمات
RouterOS شما تداخل دارد، مقادیر مرتبط را در همهٔ دستورهای بخش ۶ یکسان تغییر
دهید: Bridge، VETH، آدرس‌های IP، پوشه‌ها، مبدأ Mountها، `mountlists` و
`root-dir`. Commentهای `[Mikrozia:container]` را حفظ کنید تا دستورهای Lifecycle
و حذف بتوانند اشیای متعلق به Mikrozia را پیدا کنند. از Subnet یا نام متداخل
استفاده نکنید و هیچ Rule شبکه‌ای به Mikrozia نسبت داده نشود.

## 18. حذف امن

پیش از حذف، از `data` و `cert` Backup بگیرید. سپس این دستورها را به ترتیب اجرا
کنید تا فقط اشیای متعلق به Mikrozia حذف شوند:

```routeros
/container/stop [find where comment="[Mikrozia:container] Container"]
/container/remove [find where comment="[Mikrozia:container] Container"]
/container/mounts/remove [find where comment="[Mikrozia:container] Data Mount"]
/container/mounts/remove [find where comment="[Mikrozia:container] Cert Mount"]
/container/envs/remove [find where comment="[Mikrozia:container] Environment"]
/interface/bridge/port/remove [find where comment="[Mikrozia:container] Bridge Port"]
/ip/address/remove [find where comment="[Mikrozia:container] Gateway"]
/interface/veth/remove [find where comment="[Mikrozia:container] VETH"]
/interface/bridge/remove [find where comment="[Mikrozia:container] Bridge"]
```

این دستورها Container object، Mount/Environment definition و شبکهٔ اختصاصی آن را
حذف می‌کنند؛ دیتابیس، Certificate و تمام تنظیمات Firewall/NAT دست‌نخورده می‌مانند.

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

### Certificate منقضی می‌شود

- Mount مربوط به `/root/cert` باید متصل باشد؛
- فایل‌های تمدیدشده باید همان مسیرهای قبلی را بازنویسی کنند؛
- پس از تمدید، دستور `x-ui restart` را اجرا کنید؛
- تمدید گواهی بر عهدهٔ کاربر و بیرون از کانتینر است.

### پنل پس از تعویض نسخه خالی است

Container جدید بدون Mount صحیح `/etc/x-ui` ساخته شده است. آن را متوقف کنید و مسیر دیتابیس قبلی را متصل کنید؛ دیتابیس جدید خالی را روی Backup کپی نکنید.

### مشاهده لاگ‌های پنل و کانتینر

- **لاگ‌های برنامه پنل**: از بخش **Logs** در رابط وب پنل (بر پایه بافر درون‌حافظه‌ای) یا دستور `x-ui` (منوی 11) قابل مشاهده است؛ در RouterOS Container از systemd/`journalctl` استفاده نمی‌شود.
- **لاگ‌های `stdout`/`stderr` کانتینر در RouterOS**:

```routeros
/container/set [find where comment="[Mikrozia:container] Container"] logging=yes
/log/print follow-only where topics~"container"
```

- **حریم خصوصی لاگ دسترسی Xray**: لاگ دسترسی Xray می‌تواند IP مبدأ کلاینت‌ها، مقصدها و ایمیل حساب‌ها را ثبت کند؛ پیش از فعال‌سازی یا اشتراک‌گذاری، دسترسی به پنل را محدود و سیاست نگهداری لاگ را بررسی کنید.

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
- [MikroTik Log](https://help.mikrotik.com/docs/spaces/ROS/pages/328094/Log)
- [MikroTik Device Mode](https://help.mikrotik.com/docs/spaces/ROS/pages/93749258/Device-mode)
- [MikroTik CHR](https://help.mikrotik.com/docs/spaces/ROS/pages/18350234/Cloud%20Hosted%20Router%20CHR)
- [RouterOS Stable releases](https://mikrotik.com/download/changelogs?channelFilter=stable)
- [Docker Hub: majlot/mikrozia](https://hub.docker.com/r/majlot/mikrozia)
