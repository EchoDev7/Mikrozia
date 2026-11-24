[فارسی](/README.md) | [العربية](/README.ar_EG.md) | [中文](/README.zh_CN.md) | [Español](/README.es_ES.md) | [Русский](/README.ru_RU.md) | [Türkçe](/README.tr_TR.md) | [English (upstream history)](/README.upstream.md)

# Mikrozia — Panel de control para contenedor RouterOS

Mikrozia empaqueta Mikrozia y Xray para el **contenedor de MikroTik RouterOS**. La
única vía de instalación y actualización compatible es una imagen OCI versionada
con montajes persistentes. La guía completa está en [README.md](/README.md) (en
persa).

## Versión actual

```text
echodev7/mikrozia:v1.0.8-mikrozia
```

En producción use una etiqueta versionada, no `latest`.

## Requisitos

- RouterOS `7.25.0` o posterior del canal Stable con el paquete Container de la
  misma versión;
- Device Mode con `container=yes` y un reinicio en frío;
- al menos 2 GiB de RAM y 2 GiB de espacio libre;
- CHR x86_64 para la ruta de instalación para principiantes.

## Instalación

Ejecute estos comandos en orden en Terminal RouterOS o WinBox. La imagen de
producción es la etiqueta versionada `echodev7/mikrozia:v1.0.8-mikrozia`.

> Estas instrucciones siguen el contrato actual de contenedores RouterOS `7.25`.
> La aceptación en un CHR limpio para P1-CHR-01 aún no se ha completado; valide
> todo el flujo en un CHR desechable antes de producción. Este documento no
> declara una aceptación real en CHR.

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

> **Valores sensibles:** el Environment del contenedor de RouterOS es un almacén de
> configuración, no un almacén de secretos. Cualquier valor de `/container/envs` es
> visible para quien pueda leer o exportar la configuración del router.
> `MIKROZIA_ADMIN_USERNAME` y `MIKROZIA_ADMIN_PASSWORD` son la única fuente de la
> cuenta de administrador y deben definirse **juntos**; si solo existe una de las dos,
> el panel no arranca. Mikrozia no escribe estos valores en los registros ni los
> devuelve por la API.

Si un directorio ya existe, omita su línea `/file/add`. Para seguir la extracción:

```routeros
/container/print detail
/log/print follow-only where topics~"container"
```

Cuando termine la extracción:

```routeros
/container/start [find where comment="[Mikrozia:container] Container"]
```

La instalación solo crea Bridge/VETH, los montajes, las variables de entorno y el
contenedor; no crea reglas de firewall, NAT, port forwarding ni routing.

## Acceso y datos

- El panel escucha dentro del contenedor en `http://172.16.10.2:2053/`.
- La base de datos y la configuración viven en el montaje persistente `/etc/x-ui`;
  los certificados del usuario, en `/root/cert`.
- La imagen incluye un cliente ACME con versión fijada: emita el certificado dentro
  del contenedor con HTTP-01 (renovación manual) o Cloudflare DNS-01 (renovación
  automática). El certificado y el estado de la cuenta quedan persistentes en
  `/root/cert`. Mikrozia nunca crea ni modifica reglas de firewall, NAT o reenvío de
  puertos de RouterOS.
- La política de firewall, NAT, port forwarding, routing y control de acceso es
  responsabilidad exclusiva del administrador de RouterOS; Mikrozia no crea ni
  modifica reglas de red.

## Actualización

Descargue la nueva imagen versionada, recree el contenedor con los mismos montajes
`/etc/x-ui` y `/root/cert`, y conserve la imagen anterior hasta verificar la nueva.
No existe un actualizador de software dentro del contenedor y el RootFS de la
imagen no debe reescribirse desde dentro.

## Upstream y licencia

- Upstream: [EchoDev7/Mikrozia](https://github.com/EchoDev7/Mikrozia) (forked from MHSanaei/3x-ui v3.11.0)
- Xray: [XTLS/Xray-core](https://github.com/XTLS/Xray-core)
- License: GPL-3.0
