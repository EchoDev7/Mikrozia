[فارسی](/README.md) | [العربية](/README.ar_EG.md) | [中文](/README.zh_CN.md) | [Español](/README.es_ES.md) | [Русский](/README.ru_RU.md) | [Türkçe](/README.tr_TR.md) | [English (upstream history)](/README.upstream.md)

# Mikrozenia — لوحة تحكم حاوية RouterOS

Mikrozia هي حزمة من Mikrozia وXray لمشغّل **حاوية MikroTik RouterOS**. المسار الوحيد
المدعوم للتثبيت والتحديث هو صورة OCI مُصدَّرة برقم إصدار مع نقاط تثبيت دائمة. الدليل
الكامل متوفر في [README.md](/README.md) (بالفارسية).

## الإصدار الحالي

```text
echodev7/mikrozia:v1.0.8-mikrozia
```

استخدم وسمًا مُصدَّرًا برقم إصدار في بيئة الإنتاج، وليس `latest`.

## المتطلبات

- RouterOS `7.25.0` أو أحدث من قناة Stable مع حزمة Container مطابقة للإصدار؛
- Device Mode مع `container=yes` وإعادة تشغيل باردة؛
- ذاكرة RAM لا تقل عن 2GiB ومساحة حرة لا تقل عن 2GiB؛
- CHR x86_64 لمسار التثبيت للمبتدئين.

## التثبيت

نفّذ الأوامر التالية بالترتيب في Terminal RouterOS أو WinBox. صورة الإنتاج هي
الوسم المُصدَّر برقم إصدار `echodev7/mikrozia:v1.0.8-mikrozia`.

> تتبع هذه التعليمات عقد حاويات RouterOS `7.25` الحالي. لم يكتمل قبول CHR نظيف
> الخاص بـ P1-CHR-01 بعد؛ اختبر المسار كاملًا على CHR مؤقت قبل الإنتاج. لا يدّعي
> هذا المستند قبولًا فعليًا على CHR.

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
/container/add remote-image="echodev7/mikrozia:v1.0.8-mikrozia" interface=veth-mikrozia root-dir=containers/mikrozia/root-v1.0.8-mikrozia mountlists="MIKROZIA_DATA,MIKROZIA_CERT" envlist=MIKROZIA_ENV dns="1.1.1.1,8.8.8.8" memory-high=512M memory-max=1024M logging=yes start-on-boot=yes comment="[Mikrozia:container] Container"
```

> **قيم حساسة:** Environment حاوية RouterOS هو مخزن إعدادات وليس خزنة أسرار. كل قيمة
> في `/container/envs` مرئية لكل من يستطيع قراءة إعدادات الراوتر أو تصديرها.
> `MIKROZIA_ADMIN_USERNAME` و`MIKROZIA_ADMIN_PASSWORD` هما المصدر الوحيد لحساب
> المدير ويجب ضبطهما **معًا**؛ وفي وجود واحد منهما فقط ترفض اللوحة البدء. لا تكتب
> Mikrozia هاتين القيمتين في السجل ولا تعيدهما عبر API.

إذا كان أحد المجلدات موجودًا مسبقًا، فتجاوز سطر `/file/add` الخاص به. لمتابعة
الاستخراج:

```routeros
/container/print detail
/log/print follow-only where topics~"container"
```

بعد اكتمال الاستخراج:

```routeros
/container/start [find where comment="[Mikrozia:container] Container"]
```

ينشئ التثبيت فقط Bridge/VETH ونقاط التثبيت ومتغيرات البيئة والحاوية، ولا ينشئ أي
قاعدة Firewall أو NAT أو Port Forward أو Routing.

## الوصول والبيانات

- تستمع اللوحة داخل الحاوية على `http://172.16.10.2:2053/`.
- قاعدة البيانات والإعدادات على نقطة التثبيت الدائمة `/etc/x-ui`، وشهادات المستخدم على `/root/cert`.
- تتضمن الصورة عميل ACME مثبّت الإصدار: أصدر الشهادة داخل الحاوية عبر HTTP-01 (تجديد يدوي) أو Cloudflare DNS-01 (تجديد تلقائي). تبقى الشهادة وحالة الحساب على `/root/cert` بشكل دائم. لا تنشئ Mikrozia أي قواعد RouterOS للجدار الناري أو NAT أو إعادة توجيه المنافذ ولا تعدّلها.
- سياسة Firewall وNAT وPort Forward والتوجيه والتحكم في الوصول مسؤولية مدير RouterOS بالكامل؛ ولا تنشئ Mikrozia أي قاعدة شبكة أو تعدّلها.

## التحديث

اسحب الصورة الجديدة المُصدَّرة برقم إصدار، وأعد إنشاء الحاوية مع نفس نقاط التثبيت
`/etc/x-ui` و`/root/cert`، واحتفظ بالصورة السابقة حتى التحقق من الجديدة. لا يوجد
محدّث برمجيات داخل الحاوية، ويجب عدم إعادة كتابة RootFS الخاصة بالصورة من الداخل.

## المصدر الأعلى والترخيص

- Upstream: [EchoDev7/Mikrozia](https://github.com/EchoDev7/Mikrozia) (forked from MHSanaei/3x-ui v3.11.0)
- Xray: [XTLS/Xray-core](https://github.com/XTLS/Xray-core)
- License: GPL-3.0
