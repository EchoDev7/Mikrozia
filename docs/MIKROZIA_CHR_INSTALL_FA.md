# راهنمای جامع Mikrozia برای MikroTik CHR

این راهنما نصب و نگهداری نسخه سازگارشده 3x-ui با **MikroTik RouterOS Container** را از صفر توضیح می‌دهد. مخاطب آن کاربری است که با Container میکروتیک آشنایی قبلی ندارد.

## مرز مسئولیت پروژه

Mikrozia فقط این بخش‌ها را مدیریت می‌کند:

- ایمیج سازگار 3x-ui/Xray و قرارداد Mountهای پایدار Container؛
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

> این مسیر با قرارداد فعلی Container در RouterOS `7.25` هم‌راستا است. روی CHR
> x86_64 با RouterOS `7.25.1` و همین Tag به‌صورت واقعی دریافت Image، شروع و
> Restart، ICMP/HTTP داخلی، Mountهای پایدار و SQLite dump/restore جداگانه با
> موفقیت آزموده شده‌اند. تنها آزمون باقی‌ماندهٔ P1-CHR-01، Rollback میان دو Tag
> سازگار اما متفاوت است که پس از انتشار Tag بعدی انجام می‌شود.

دستورهای زیر را به ترتیب در Terminal RouterOS یا WinBox اجرا کنید. فقط اشیای
متعلق به Mikrozia ساخته می‌شوند و با `[Mikrozia:container]` علامت‌گذاری شده‌اند.

### 6.1 ساخت پوشه‌های پایدار و موقت

پوشهٔ `data` به `/etc/x-ui`، پوشهٔ `cert` به `/root/cert` و پوشهٔ `tmp` برای
فضای موقت Extract متصل می‌شوند. پوشهٔ Root نسخه‌دار را RouterOS هنگام Extract
می‌سازد. این دستورها فقط برای نصب تمیز هستند. اگر موردی از قبل وجود دارد، نصب
را دوباره Paste نکنید؛ ابتدا وضعیت را با `/file/print where name~"mikrozia"`
و `/container/print detail` بررسی کنید.

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
/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_ADMIN_USERNAME value="YOUR_ADMIN_USERNAME" comment="[Mikrozia:container] Environment"
/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_ADMIN_PASSWORD value="YOUR_STRONG_PASSWORD" comment="[Mikrozia:container] Environment"
```

> ⚠️ **هشدار حساسیت:** Environment کانتینر RouterOS یک **انبار پیکربندی** است، نه
> انبار اسرار. هر مقدار در `/container/envs` برای هر کسی که بتواند پیکربندی روتر را
> بخواند یا خروجی بگیرد قابل مشاهده است. یک رمز عبور قوی و یکتا برای مدیر انتخاب
> کنید، دسترسی به خود روتر و پشتیبان‌هایش را محدود نگه دارید و اگر پیکربندی از کنترل
> شما خارج شد، رمز را چرخش دهید. Mikrozia هرگز این مقادیر را در Log چاپ نمی‌کند و
> هرگز از طریق API برنمی‌گرداند.
>
> `MIKROZIA_ADMIN_USERNAME` و `MIKROZIA_ADMIN_PASSWORD` مرجع اصلی حساب مدیر پنل
> هستند و باید **با هم** تنظیم شوند؛ اگر فقط یکی از آن‌ها موجود باشد، پنل اجرا
> نمی‌شود و مقدار ناقص را نام می‌برد. اگر هیچ‌کدام تنظیم نشده باشند ولی حسابی از
> قبل موجود باشد، آن حساب بدون تغییر حفظ می‌شود؛ اگر هیچ حسابی هم نباشد، پنل اجرا
> نمی‌شود و هر دو مقدار را طلب می‌کند.

تغییر بعدی اطلاعات ورود:

```routeros
/container/envs/set [find where list=MIKROZIA_ENV and key=MIKROZIA_ADMIN_PASSWORD] value="NEW_STRONG_PASSWORD"
/container/restart [find where comment="[Mikrozia:container] Container"]
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

## 8. دسترسی شبکه: نمونهٔ دستی برای پنل و Subscription

Container اکنون روی `172.16.10.2` قرار دارد، اما پروژه عمداً مسیر اینترنت، NAT، Port Forward و Rule دسترسی را ایجاد نکرده است. دستورهای این بخش **فقط نمونهٔ دستی برای مدیر RouterOS** هستند. Mikrozia آن‌ها را ایجاد، تغییر یا حذف نمی‌کند.

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

### 8.1 پیش از اعمال Ruleها

ابتدا نام واقعی WAN و وجود Ruleهای قبلی را ببینید. روی CHR مرجعِ آزمون، WAN
`ether1` بود؛ این نام را کورکورانه کپی نکنید. همچنین `<...>` در دستورهای زیر
placeholder است و باید پیش از اجرا عوض شود.

```routeros
/interface/print
/ip/address/print
/ip/firewall/filter/print
/ip/firewall/nat/print
```

### 8.2 پنل فقط برای IP مدیر، Subscription عمومی

این الگو پنل `2053/TCP` را به IP عمومی مدیر محدود می‌کند و `2096/TCP` را برای
Subscription منتشر می‌کند. IP مدیر، IP عمومی اینترنت شماست، نه IP خصوصی
لپ‌تاپ و نه IP کانتینر.

اگر CHR روی Cloud است، Security Group یا Cloud Firewall ارائه‌دهنده نیز باید
همین پورت‌ها را اجازه دهد؛ NAT در RouterOS جای Rule فایروال ابری را نمی‌گیرد.

```routeros
/ip/firewall/address-list/add list=MIKROZIA_PANEL_ADMINS address=<YOUR_ADMIN_PUBLIC_IP>/32 comment="[Mikrozia:operator] Panel administrator source"
/ip/firewall/nat/add chain=dstnat in-interface=<WAN_INTERFACE> protocol=tcp src-address-list=MIKROZIA_PANEL_ADMINS dst-port=2053 action=dst-nat to-addresses=172.16.10.2 to-ports=2053 comment="[Mikrozia:operator] Panel allow-listed port forward"
/ip/firewall/nat/add chain=dstnat in-interface=<WAN_INTERFACE> protocol=tcp dst-port=2096 action=dst-nat to-addresses=172.16.10.2 to-ports=2096 comment="[Mikrozia:operator] Subscription port forward"
```

اگر زنجیرهٔ `forward` شما Rule نهایی Drop دارد، این Ruleهای allow را **قبل از
آن Rule** قرار دهید:

```routeros
/ip/firewall/filter/add chain=forward in-interface=<WAN_INTERFACE> protocol=tcp src-address-list=MIKROZIA_PANEL_ADMINS dst-address=172.16.10.2 dst-port=2053 connection-nat-state=dstnat action=accept comment="[Mikrozia:operator] Allow listed panel forward"
/ip/firewall/filter/add chain=forward in-interface=<WAN_INTERFACE> protocol=tcp dst-address=172.16.10.2 dst-port=2096 connection-nat-state=dstnat action=accept comment="[Mikrozia:operator] Allow subscription forward"
```

در یک نشست دوم، از IP مجاز `http://<ROUTER_PUBLIC_IP>:2053` را باز کنید. تا وقتی
Subscription Path واقعی نساخته‌اید، پاسخ `404 page not found` از
`http://<ROUTER_PUBLIC_IP>:2096/` عادی است و رسیدن درخواست به سرویس را نشان
می‌دهد. شمارنده‌های Rule را هم بررسی کنید:

```routeros
/ip/firewall/nat/print stats where comment~"Mikrozia:operator"
/ip/firewall/filter/print stats where comment~"Mikrozia:operator"
```

برای تغییر IP مدیر، IP جدید را ابتدا با یک `/ip/firewall/address-list/add` دوم
اضافه، در نشست دوم آزمون، و فقط سپس IP قدیمی را حذف کنید. هرگز ابتدا IP قدیمی را
جایگزین نکنید. Ruleهای Inboundهای Xray جدا هستند: برای هر پورتی که در پنل
می‌سازید، باید تصمیم مستقل RouterOS بگیرید.

## 9. مشاهده اطلاعات ورود مدیر

اطلاعات ورود مدیر پنل از **Environment کانتینر RouterOS** خوانده می‌شود. مقدار
`MIKROZIA_ADMIN_USERNAME` و `MIKROZIA_ADMIN_PASSWORD` که در بخش 6.3 تنظیم کرده‌اید،
مرجع اصلی حساب مدیر هستند و در هر بار بالا آمدن کانتینر با پنل تطبیق داده می‌شوند.

نمایش مقادیر:

```routeros
/container/envs/print where list=MIKROZIA_ENV
```

> ⚠️ این خروجی شامل **رمز عبور به‌صورت متن ساده** است. آن را در جای امن ذخیره کنید و
> از کپی‌کردن آن به پیام‌رسان‌ها، ایمیل یا لاگ‌های مشترک خودداری کنید. به یاد داشته
> باشید Environment کانتینر یک انبار پیکربندی است و هر کسی که بتواند پیکربندی روتر
> را بخواند، آن‌ها را می‌بیند.

رفتار پنل در موقعیت‌های مختلف:

| وضعیت Environment                    | رفتار                                                       |
| ------------------------------------ | ----------------------------------------------------------- |
| هر دو متغیر تنظیم شده                | حساب مدیر روی هر استارت دقیقاً با آن‌ها تطبیق داده می‌شود.     |
| فقط یکی از آن‌ها تنظیم شده            | پنل اجرا نمی‌شود و نام متغیر ناقص را اعلام می‌کند.             |
| هیچ‌کدام تنظیم نشده، حساب موجود است   | حساب موجود بدون تغییر حفظ می‌شود و فقط یک یادآوری Log می‌شود. |
| هیچ‌کدام تنظیم نشده، حسابی وجود ندارد | پنل اجرا نمی‌شود و هر دو متغیر را طلب می‌کند.                 |

> **رمز تصادفیِ یک‌بارمصرف دیگر وجود ندارد.** رمز و نام کاربری هرگز به‌صورت متن ساده
> در Log چاپ نمی‌شوند و از طریق API هم برگردانده نمی‌شوند.

تا زمانی که Environment مالک حساب است، تغییر رمز از داخل پنل یا از Shell قدیمی
(`x-ui setting -password …`) رد می‌شود تا دو منبع حقیقت رقیب ایجاد نشود. برای تغییر
رمز، متغیر محیطی را تغییر داده و کانتینر را ری‌استارت کنید:

```routeros
/container/envs/set [find where list=MIKROZIA_ENV and key=MIKROZIA_ADMIN_PASSWORD] value="NEW_STRONG_PASSWORD"
/container/restart [find where comment="[Mikrozia:container] Container"]
```

برای اعمال تغییر تنظیمات پنل بدون ری‌استارت کل Container می‌توانید داخل Shell از `x-ui restart` (بارگذاری مجدد وب‌سرور پنل و ری‌استارت Xray) استفاده کنید، یا در RouterOS کل Container را ری‌استارت نمایید:

```routeros
/container/restart [find where comment="[Mikrozia:container] Container"]
```

> **نکته Lifecycle در RouterOS Container**: دستورهای `x-ui start`، `x-ui stop`، `x-ui enable` و `x-ui disable` داخل کانتینر هیچ تغییری در وضعیت کانتینر ایجاد نمی‌کنند و تنها دستور معادل RouterOS (از جمله `/container/start`، `/container/stop` و `/container/set ... start-on-boot=yes|no restart-policy=always|no`) را نمایش می‌دهند. دستور `x-ui restart` نیز تنها سرویس وب پنل و Xray را داخل کانتینر ری‌لود/ری‌استارت می‌کند و خود Container را خاموش یا ری‌استارت نمی‌کند.

## 10. تنظیمات اولیه پنل

پس از دسترسی شبکه‌ای به پورت داخلی پنل:

1. با اطلاعات Environment وارد شوید.
2. (اختیاری) احراز هویت دومرحله‌ای را فعال کنید.
3. Web Base Path تصادفی تنظیم کنید.
4. تنظیمات را ذخیره و پنل را restart کنید.

> اگر Environment مالک حساب مدیر است، تغییر نام کاربری یا رمز از داخل پنل رد
> می‌شود؛ آن‌ها را از همان‌جا که تعریف کرده‌اید تغییر دهید.

## 11. دامنه و TLS

یک رکورد DNS برای IP عمومی موردنظر خود بسازید. Mikrozia یک کلاینت ACME با نسخهٔ
Pin‌شده داخل Image دارد؛ بنابراین می‌توانید گواهی را **داخل کانتینر** صادر کنید و
پنل خودش HTTPS را ارائه می‌دهد.

دو روش پشتیبانی‌شده:

| روش                            | نیاز به پورت عمومی                     | تمدید  |
| ------------------------------ | -------------------------------------- | ------ |
| **HTTP-01 (دستی)**             | بله، موقتاً TCP/80 باید به کانتینر برسد | دستی   |
| **Cloudflare DNS-01 (خودکار)** | خیر                                    | خودکار |

> ⚠️ Mikrozia هرگز Ruleهای فایروال، NAT، Port-Forward، Routing، Address-List یا
> Address-List امنیت ابری RouterOS را ایجاد، تغییر، فعال یا غیرفعال نمی‌کند. برای
> روش HTTP-01، دسترسی عمومی TCP/80 را **خودتان** و موقتاً در RouterOS فراهم کنید.

از منوی `x-ui` گزینهٔ **14 — TLS Certificates** یا از CLI استفاده کنید:

```bash
# HTTP-01 (دستی)
x-ui acme check --http --domain panel.example.com
x-ui acme issue --http --domain panel.example.com

# Cloudflare DNS-01 (خودکار) — توکن از ورودی پنهان یا عمداً از Environment خوانده می‌شود
x-ui acme cloudflare setup --domain panel.example.com
x-ui acme issue --dns --domain panel.example.com
```

مسیر پایدار گواهی‌ها روی Mount پایدار `/root/cert`:

```text
/root/cert/<domain>/fullchain.pem
/root/cert/<domain>/privkey.pem
/root/cert/.acme.sh/          # حساب ACME، وضعیت هر دامنه و وضعیت تمدید
```

Mikrozia از داخل کانتینر نمی‌تواند دسترس‌پذیری عمومی HTTP-01 را بررسی کند.
اپراتور RouterOS باید موقتاً TCP 80 را از اینترنت عمومی در دسترس قرار دهد.

پس از صدور موفق و تغییر واقعی جفت گواهی، Mikrozia هر چهار مسیر TLS پنل و اشتراک
را در یک تراکنش به‌روزرسانی و پنل، شنوندهٔ اشتراک و Xray را دقیقاً یک‌بار Reload
می‌کند. خطاهای نصب، Validation، ذخیرهٔ تنظیمات یا Reload گزارش می‌شوند و جفت و
تنظیمات قبلی بازیابی می‌شوند. Inboundهای TLS در صورت نیاز همان مسیرها را دستی
ارجاع می‌دهند. یک گواهی موجود را هم می‌توانید با
`x-ui cert -webCert … -webCertKey …` درون‌ریزی کنید؛ برای جایگزینی دستی فایل‌ها
بیرون از جریان ACME، `x-ui restart` را اجرا کنید.

توکن Cloudflare با کمترین امتیاز (Zone → DNS → Edit و Zone → Zone → Read، فقط یک
Zone) به‌صورت `0600` روی Mount پایدار ذخیره می‌شود، هرگز در Log چاپ نمی‌شود، هرگز
به‌صورت متن ساده از API برگردانده نمی‌شود و هرگز در لایه‌های Image نوشته نمی‌شود.
مقادیر RouterOS Environment برای هر کسی که Config را بخواند یا Export کند قابل
مشاهده‌اند؛ برای ورودی عمداً غیرتعاملی فقط `--stdin` صریح یک‌خطی یا Environment
پشتیبانی می‌شود. تمدید DNS-01 در پس‌زمینهٔ داخلی هر ۱۲ ساعت بررسی می‌شود و
Listenerها فقط در صورت تغییر واقعی گواهی ری‌لود می‌شوند.

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

## 14. Backup و Restore بدون از دست‌دادن اطلاعات

### 14.1 Backup آنلاین و سازگار SQLite (روش پیشنهادی)

در Terminal RouterOS وارد Shell کانتینر شوید:

```routeros
/container/shell [find where comment="[Mikrozia:container] Container"]
```

داخل Shell، یک نام یکتا با تاریخ انتخاب و فقط همین دستور را اجرا کنید:

```sh
x-ui migrate-db --dump /etc/x-ui/x-ui-backup-YYYY-MM-DD.sql
exit
```

خروجی موفق باید `Dumped /etc/x-ui/x-ui.db -> ...` باشد. فایل Backup در WinBox →
**Files** با نام `containers/mikrozia/data/x-ui-backup-YYYY-MM-DD.sql` دیده
می‌شود؛ آن را فوراً روی یک دستگاه دیگر دانلود کنید. این Dump هنگام فعال‌بودن
SQLite هم Snapshot سازگار می‌گیرد. فایل `/etc/x-ui/x-ui.db` را در حال اجرا
مستقیماً کپی نکنید.

از پوشهٔ `containers/mikrozia/cert/` نیز پس از هر تغییر گواهی یک نسخهٔ خارج از
روتر نگه دارید؛ کلید خصوصی را در محل امن و رمزگذاری‌شده نگه‌داری کنید.

### 14.2 اعتبارسنجی Restore، بدون لمس دیتابیس زنده

این آزمایش را پیش از روز حادثه انجام دهید. در Shell همان Container:

```sh
x-ui migrate-db --restore /etc/x-ui/x-ui-backup-YYYY-MM-DD.sql --out /etc/x-ui/x-ui-restore-check.db
exit
```

خروجی موفق باید `Restored ...` باشد. دستور عمداً فایل زنده را تغییر نمی‌دهد و
اگر فایل مقصد از قبل وجود داشته باشد متوقف می‌شود. پس از تأیید موفقیت، فایل
`x-ui-restore-check.db` را از WinBox → **Files** حذف کنید تا فضای Mount اشغال
نشود.

### 14.3 Restore واقعی بعد از خرابی یا مهاجرت

برای جایگزینی دیتابیس زنده، از **Panel Dashboard → Backup & Restore → Restore**
استفاده کنید و همان فایل `.sql` را Upload کنید. این مسیر از تراکنش بازیابی
Mikrozia استفاده می‌کند: پیش از اعمال Snapshot سازگار می‌گیرد، Xray را متوقف و
بعد از جایگزینی معتبر دوباره راه‌اندازی می‌کند؛ در خطا به دیتابیس قبل بازمی‌گردد.
هنگام Upload گزینهٔ نگهداری تنظیمات میزبان/پنل فعلی را با دقت انتخاب کنید.

پیش از Restore واقعی این کارها اجباری‌اند:

1. یک Backup آنلاین تازه با بخش 14.1 بگیرید و دانلود کنید.
2. از پنل در یک نشست دوم یا دسترسی Shell مطمئن شوید.
3. فایل Backup را از نسخهٔ اصلی (major) سازگار انتخاب کنید.
4. تا پایان پیام موفقیت، Container را Stop/Restart یا Image را تعویض نکنید.

اگر برنامه دربارهٔ `.mikrozia-sqlite-restore-*` یا
`.mikrozia-sqlite-restore-state.json` خطا داد، آن‌ها را دستی پاک نکنید؛ Logها و
فایل‌ها را حفظ کنید و ابتدا وضعیت را بررسی کنید.

## 15. به‌روزرسانی به نسخه جدید

هر Release باید ابتدا روی Docker Hub منتشر شده باشد. برای ارتقا:

1. Release Notes و Tag جدید را بررسی کنید.
2. از `data` و `cert` Backup بگیرید.
3. Container قدیمی را Stop کنید.
4. یک Container جدید با Tag نسخه‌دار و `root-dir` جدید بسازید.
5. همان Mountهای پایدار را متصل کنید.
6. نسخه جدید را Start و بررسی کنید.
7. فقط پس از اطمینان، درباره حذف RootFS قدیمی تصمیم بگیرید.

### 15.1 Runbook تأییدشدهٔ ارتقا (نمونهٔ واقعی `v1.0.2-mikrozia`)

ترتیب زیر روی یک CHR واقعی اجرا و تأیید شده است. هیچ دستوری در آن Mount پایدار
را حذف، بازسازی یا تغییر نمی‌دهد و هیچ قاعدهٔ Firewall/NAT/Routing اضافه نمی‌کند.

**Preflight (فقط‌خواندنی):** وضعیت `container`، `veth`، `mounts`، `envs`،
`ip address`، `ip service` و نسخهٔ RouterOS را ثبت کنید. مرز دو چیز را روشن کنید:

- حذف **Container** فقط تعریف اجرا (و `root-dir` نسخه‌ای) را برمی‌دارد.
- حذف **Data** یعنی از بین بردن `src` مربوط به Mountهای `MIKROZIA_DATA` و
  `MIKROZIA_CERT`؛ این کار در ارتقا هرگز انجام نمی‌شود.

**پشتیبان غیرمخرب:**

```routeros
/system/backup/save name=mikrozia-pre-upgrade
```

**Stop و ساخت Container جدید با Tag ثابت:**

```routeros
/container/stop [find where comment="[Mikrozia:container] Container"]
/container/add remote-image="majlot/mikrozia:v1.0.2-mikrozia" interface=veth-mikrozia root-dir=containers/mikrozia/root-v1.0.2-mikrozia mountlists="MIKROZIA_DATA,MIKROZIA_CERT" envlist=MIKROZIA_ENV dns="1.1.1.1,8.8.8.8" memory-high=512M memory-max=1024M shm-size=64M logging=yes start-on-boot=yes check-certificate=yes comment="[Mikrozia:container] Container"
```

تا پایان Extract (`/log/print follow-only where topics~"container"`) صبر کنید.
سپس فقط تعریف نسخهٔ قبلی را حذف و نسخهٔ جدید را Start کنید:

```routeros
/container/remove [find where name="mikrozia:v1.0.1-mikrozia"]
/container/start [find where name="mikrozia:v1.0.2-mikrozia"]
/container/print
```

> از Tag شناور (`v1-mikrozia`) برای نصب یا ارتقای پذیرش‌شده استفاده نکنید؛ Tag
> نسخه‌دار ثابت، تنها مرجع قابل بازتولید است.

### 15.2 اتصال فقط‌خواندنی به RouterOS API (داخلی)

Prerequisites: یک کاربر اختصاصی با حداقل Policy و سرویس `api` محدود به آدرس داخلی
VETH. هیچ `api-ssl`، REST، قاعدهٔ Firewall یا دسترسی WAN لازم نیست.

```routeros
/user/group/add name=mikrozia-api policy=read,api
/user/add name=mikrozia-api group=mikrozia-api password=<STRONG_RANDOM_SECRET>
/ip/service/set [find name=api] disabled=no address=<CONTAINER_VETH_IP>
```

Secret را روی خط فرمان یا در Log چاپ نکنید؛ می‌توانید مقدار را در همان یک خط
تولید و مستقیم در Environment بنویسید تا در خروجی نمایش داده نشود. سپس سه متغیر
زیر را به `MIKROZIA_ENV` اضافه کنید (mount/env مشترک است و بین نسخه‌ها منتقل می‌شود):

```routeros
/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_ROUTEROS_API_HOST value="<ROUTER_INTERNAL_GATEWAY_IP>"
/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_ROUTEROS_API_USERNAME value="mikrozia-api"
/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_ROUTEROS_API_PASSWORD value=<STRONG_RANDOM_SECRET>
```

Host باید یک IPv4 خصوصی تحت‌اللفظی باشد (آدرس داخلی سمت روتر، همان Gateway
VETH)؛ Hostname، loopback و آدرس عمومی پذیرفته نمی‌شود. تنها سرویس مصرف‌شده
TCP/8728 است. برای اعمال تنظیمات فقط همان Container را restart کنید، نه RouterOS:

```routeros
/container/restart [find where name="mikrozia:v1.0.2-mikrozia"]
```

### 15.3 Checklist پذیرش داشبورد پس از ارتقا

1. ورود به پنل انجام می‌شود و نسخهٔ نمایشی `Mikrozia v1.0.2` است.
2. نوار بالا `RouterOS API: Connected` را نشان می‌دهد (نه `connecting` یا
   `authentication_failed`).
3. `CPU` و `RAM` مصرف زندهٔ RouterOS و مقدار Avg/Peak را نشان می‌دهند.
4. خط `Container` در CPU/RAM فقط وقتی داده دارد پر می‌شود؛ `—` یعنی مقدار
   container-scoped در دسترس نیست، نه صفر.
5. `Clients`، `Top Inbounds`، `Storage` و `Uptime` بدون خطا نمایش داده می‌شوند.
6. `Overall Speed` ممکن است `0 B/s` باشد؛ `Unavailable` تنها با دلیل ثبت‌شده
   مجاز است. در RouterOS Container نام اینترفیس داخل فضای نام `veth-…` است و
   داشبورد این نام را تنها در Scope اثبات‌شدهٔ container می‌شمارد؛ در غیر این
   صورت `netIO` را `Unavailable` اعلام می‌کند تا ترافیک حدسی نشان ندهد.
7. تب `RouterOS Container` در Logs فقط وضعیت API را نشان می‌دهد و خواندن Log
   کانتینر از RouterOS همچنان deferred است.
8. `Restart`/`Stop` را در پذیرش اجرا نکنید؛ فقط وجود و حالت منوها بررسی شود.
9. در Log پنل هیچ خطای credential/API، loop یا retry غیرعادی نباشد.
10. کاربران، Inboundها، تنظیمات، گواهی و دادهٔ Subscription باقی مانده باشند
    (`/etc/x-ui/x-ui.db` و `/root/cert` از همان Mountهای قبلی خوانده می‌شوند).

### 15.4 فعال‌سازی اختیاری `Overall Speed` در RouterOS Container

اگر بند 6 بالا `Unavailable` نشان داد، می‌توانید نام دقیق اینترفیس شبکهٔ همین
Container را صریح اعلام کنید (هیچ دستور جدید RouterOS API یا firewall لازم نیست):

```routeros
/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_CONTAINER_INTERFACE value="<CONTAINER_INTERFACE_NAME>" comment="[Mikrozia:container] Environment"
```

- مقدار، فقط **نام** اینترفیسی است که داخل namespace همین Container دیده
  می‌شود (در نصب این راهنما `veth-mikrozia`)، با الگوی
  `^[A-Za-z0-9._-]{1,63}$`. نام `lo`/`lo0` و هر مقدار نامعتبر رد می‌شود.
- خالی یا تنظیم‌نشده یعنی رفتار قبلی؛ هیچ تغییری در جمع‌آوری پیش‌فرض رخ نمی‌دهد.
- این مقدار فقط وقتی به‌کار می‌رود که با قواعد پیش‌فرض **هیچ** اینترفیس
  قابل‌شمارشی نمانده باشد. روی میزبان با NIC فیزیکی (Scope نامشخص) اثری ندارد،
  پس دوبار‌شماری `veth*` ممکن نیست.
- اگر اینترفیس با آن نام پیدا نشود، `netIO` باز هم `Unavailable` می‌ماند؛
  fallback به `lo`، WAN یا مجموع RouterOS وجود ندارد.
- جهت داده: `Upload` = TX و `Download` = RX همان اینترفیس. با restart شدن
  Container شمارنده‌ها صفر می‌شوند و نمونهٔ بعدی از baseline جدید محاسبه
  می‌شود؛ نرخ منفی یا جهش ساخته نمی‌شود.
- پس از افزودن env فقط همان Container را restart کنید، نه RouterOS.

### 15.5 آماده‌سازی لاگ کانتینر و Restart Container برای `v1.0.3-mikrozia`

این قابلیت‌ها تا زمانی که Image `v1.0.3-mikrozia` نصب و روی CHR پذیرفته نشده است، در
نسخهٔ فعلی `v1.0.2-mikrozia` فعال نیستند. نخست روی CHR، comment کانتینر را
با `/container/print` به‌صورت **فیلدهای انتخابی** بررسی کنید؛ از `print detail`
بدون محدودیت فیلد استفاده نکنید، چون envهای حاوی رمز را نمایش می‌دهد.

برای خواندن لاگ در RouterOS 7.25.1، `MIKROZIA_ROS_CONTAINER_COMMENT` را برابر
comment یکتای همان کانتینر در `MIKROZIA_ENV` تنظیم کنید. نام بلند
`MIKROZIA_ROUTEROS_CONTAINER_COMMENT` در این RouterOS به فرایند کانتینر تحویل
نمی‌شود. پنل با حساب فقط‌خواندنی موجود، ابتدا
`/container/print` و سپس `/container/log/print` را می‌خواند و فقط رکوردهایی را
نمایش می‌دهد که فیلد `container` آن‌ها با `name` کانتینر شناسایی‌شده برابر است.
اگر `logging=no` باشد، فعال‌کردن آن تصمیم جداگانهٔ اپراتور در RouterOS است؛
پنل تنظیمات لاگ را تغییر نمی‌دهد.

برای فعال‌سازی **اختیاری** Restart Container، حساب RouterOS API جداگانه‌ای
با Policyهای لازم برای خواندن و اجرای `/container/restart` بسازید و نام/رمز آن
را در `MIKROZIA_ROS_RESTART_USERNAME` و
`MIKROZIA_ROS_RESTART_PASSWORD` قرار دهید. Policyهای `read,write,api`
پیش‌نیاز مورد انتظارند، اما RouterOS دسترسی را به همین یک فرمان محدود نمی‌کند؛
دسترسی API را فقط از IP داخلی Container مجاز کنید و مجوز حساب را روی CHR
آزمایش کنید. از حساب `admin` یا همان حساب فقط‌خواندنی استفاده نکنید. رمز را
در خروجی دستور، اسکرین‌شات یا مستندات ثبت نکنید.

پس از نصب Image بعدی، پیش از نخستین Restart عملیاتی این موارد را بررسی کنید:

1. `RouterOS API: Connected` و تب لاگ، رکوردهای همان کانتینر را مطابق WinBox
   نشان می‌دهند؛ `logging=no` به‌روشنی نمایش داده می‌شود.
2. گزینهٔ `Restart Container` فقط با هویت یکتا، وضعیت `running` و حساب عملیاتی
   مجزا فعال است؛ لغو Dialog هیچ فرمانی ارسال نمی‌کند.
3. پس از تأیید، فقط یک درخواست ارسال می‌شود. اگر اتصال پنل پیش از پاسخ قطع شد،
   نتیجه «نامعلوم» است و درخواست خودکار تکرار نمی‌شود. پس از بازگشت پنل، نسخه، Xray،
   داده‌های `/etc/x-ui` و گواهی‌های `/root/cert` بررسی شوند.
4. `Container History` برای CPU و RAM فقط نمونه‌های cgroup خود Container را
   نشان می‌دهد؛ نبود داده، مصرف میزبان را با برچسب Container جایگزین نمی‌کند.

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
/container/shell [find where comment="[Mikrozia:container] Container"]
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
- تمدید موفق ACME خودکار Reload می‌شود؛ فقط اگر فایل‌ها را دستی و بیرون از ACME
  جایگزین کرده‌اید `x-ui restart` را اجرا کنید؛
- با روش DNS-01 تمدید خودکار است؛ با روش HTTP-01 تمدید دستی و با همان دستور صدور انجام می‌شود:
  `x-ui acme issue --http --domain <domain>`؛
- وضعیت تمدید را با `x-ui acme status` بررسی کنید.

### پنل پس از تعویض نسخه خالی است

Container جدید بدون Mount صحیح `/etc/x-ui` ساخته شده است. آن را متوقف کنید و مسیر دیتابیس قبلی را متصل کنید؛ دیتابیس جدید خالی را روی Backup کپی نکنید.

### مشاهده لاگ‌های پنل و کانتینر

- **لاگ‌های برنامه پنل**: از بخش **Logs** در رابط وب پنل (بر پایه بافر درون‌حافظه‌ای) یا دستور `x-ui` (منوی 11) قابل مشاهده است؛ در RouterOS Container از systemd/`journalctl` استفاده نمی‌شود.
- **لاگ‌های `stdout`/`stderr` کانتینر در RouterOS**:

```routeros
/container/set [find where comment="[Mikrozia:container] Container"] logging=yes
/container/log/print detail
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
- [ ] `MIKROZIA_ADMIN_USERNAME` و `MIKROZIA_ADMIN_PASSWORD` تنظیم و ذخیره شده‌اند.
- [ ] Backup دیتابیس و گواهی‌ها تهیه شده است.
- [ ] مدیر RouterOS سیاست شبکه و دسترسی را جداگانه تنظیم کرده است.

## منابع رسمی

- [MikroTik Container](https://help.mikrotik.com/docs/spaces/ROS/pages/84901929/Container)
- [MikroTik Log](https://help.mikrotik.com/docs/spaces/ROS/pages/328094/Log)
- [MikroTik Device Mode](https://help.mikrotik.com/docs/spaces/ROS/pages/93749258/Device-mode)
- [MikroTik CHR](https://help.mikrotik.com/docs/spaces/ROS/pages/18350234/Cloud%20Hosted%20Router%20CHR)
- [RouterOS Stable releases](https://mikrotik.com/download/changelogs?channelFilter=stable)
- [Docker Hub: majlot/mikrozia](https://hub.docker.com/r/majlot/mikrozia)
