[فارسی](/README.md) | [العربية](/README.ar_EG.md) | [中文](/README.zh_CN.md) | [Español](/README.es_ES.md) | [Русский](/README.ru_RU.md) | [Türkçe](/README.tr_TR.md) | [English (upstream history)](/README.upstream.md)

# Mikrozia — RouterOS Container için kontrol paneli

Mikrozia, Mikrozia ve Xray'i **MikroTik RouterOS Container** için paketler. Desteklenen
tek kurulum ve güncelleme yolu, kalıcı bağlama noktalarıyla birlikte sürümlenmiş bir
OCI imajıdır. Tam kılavuz [README.md](/README.md) dosyasındadır (Farsça).

## Geçerli sürüm

```text
echodev7/mikrozia:v1-mikrozia
```

Üretimde `latest` yerine sürümlenmiş bir etiket kullanın.

## Gereksinimler

- Stable kanalından RouterOS `7.25.0` veya üstü ve aynı sürümde Container paketi;
- `container=yes` ile Device Mode ve soğuk yeniden başlatma;
- en az 2 GiB RAM ve 2 GiB boş alan;
- başlangıç kurulum yolu için CHR x86_64.

## Kurulum

Komutları RouterOS Terminal veya WinBox içinde sırayla çalıştırın. Üretim imajı
sürümlenmiş `echodev7/mikrozia:v1-mikrozia` etiketidir.

> Bu talimatlar mevcut RouterOS `7.25` konteyner sözleşmesine uyar. P1-CHR-01 için
> temiz CHR kabulü henüz tamamlanmadı; üretimden önce tüm akışı geçici bir CHR
> üzerinde doğrulayın. Bu belge gerçek CHR kabulü iddia etmez.

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

> **Hassas değerler:** RouterOS Container Environment bir sır kasası değil, bir
> yapılandırma deposudur. `/container/envs` içindeki her değer, yönlendirici
> yapılandırmasını okuyabilen veya dışa aktarabilen herkes tarafından görülebilir.
> `MIKROZIA_ADMIN_USERNAME` ve `MIKROZIA_ADMIN_PASSWORD` yönetici hesabının tek
> kaynağıdır ve **birlikte** tanımlanmalıdır; yalnızca biri tanımlıysa panel başlamaz.
> Mikrozia bu değerleri loglara yazmaz ve API üzerinden döndürmez.

Bir dizin zaten varsa o dizinin `/file/add` satırını atlayın. Çıkarmayı izlemek
için:

```routeros
/container/print detail
/log/print follow-only where topics~"container"
```

Çıkarma tamamlandıktan sonra:

```routeros
/container/start [find where comment="[Mikrozia:container] Container"]
```

Kurulum yalnızca Bridge/VETH, bağlama noktaları, ortam değişkenleri ve konteyneri
oluşturur; firewall, NAT, port yönlendirme veya routing kuralı oluşturmaz.

## Erişim ve veriler

- Panel, konteyner içinde `http://172.16.10.2:2053/` adresinde dinler.
- Veritabanı ve ayarlar kalıcı `/etc/x-ui` bağlamasında, kullanıcı sertifikaları
  `/root/cert` üzerindedir.
- Image içinde sürümü sabitlenmiş bir ACME istemcisi bulunur: sertifikayı konteyner
  içinde HTTP-01 (elle yenileme) veya Cloudflare DNS-01 (otomatik yenileme) ile alın.
  Sertifika ve hesap durumu `/root/cert` üzerinde kalıcıdır. Mikrozia hiçbir zaman
  RouterOS firewall, NAT veya port yönlendirme kuralı oluşturmaz veya değiştirmez.
- Firewall, NAT, port yönlendirme, yönlendirme ve erişim denetimi politikası
  tamamen RouterOS yöneticisine aittir; Mikrozia hiçbir ağ kuralı oluşturmaz veya
  değiştirmez.

## Güncelleme

Yeni sürümlenmiş imajı çekin, konteyneri aynı `/etc/x-ui` ve `/root/cert`
bağlamalarıyla yeniden oluşturun ve yeni sürüm doğrulanana kadar önceki imajı
saklayın. Konteyner içinde yazılım güncelleyici yoktur ve imajın RootFS'i içeriden
yeniden yazılmamalıdır.

## Upstream ve lisans

- Upstream: [EchoDev7/Mikrozia (forked from MHSanaei/3x-ui v3.11.0)](https://github.com/EchoDev7/Mikrozia (forked from MHSanaei/3x-ui v3.11.0))
- Xray: [XTLS/Xray-core](https://github.com/XTLS/Xray-core)
- License: GPL-3.0
