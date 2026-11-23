[فارسی](/README.md) | [العربية](/README.ar_EG.md) | [中文](/README.zh_CN.md) | [Español](/README.es_ES.md) | [Русский](/README.ru_RU.md) | [Türkçe](/README.tr_TR.md) | [English (upstream history)](/README.upstream.md)

# Mikrozia — 面向 RouterOS 容器的控制面板

Mikrozia 将 Mikrozia 与 Xray 打包为 **MikroTik RouterOS 容器** 产品。唯一受支持的
安装与更新方式是带版本号的 OCI 镜像加持久化挂载。完整指南见
[README.md](/README.md)（波斯语）。

## 当前版本

```text
echodev7/mikrozia:v1-mikrozia
```

生产环境请使用带版本号的标签，而不是 `latest`。

## 要求

- Stable 渠道的 RouterOS `7.25.0` 或更高版本，以及相同版本的 Container 软件包；
- 启用 `container=yes` 的 Device Mode，并执行冷重启；
- 至少 2GiB 内存和 2GiB 可用空间；
- 初学者安装路径使用 CHR x86_64。

## 安装

请在 RouterOS Terminal 或 WinBox 中按顺序执行以下命令。生产镜像是版本化标签
`echodev7/mikrozia:v1-mikrozia`。

> 这些说明遵循当前 RouterOS `7.25` 容器契约。P1-CHR-01 的干净 CHR 验收尚未
> 完成；请先在一次性 CHR 上验证完整流程，再用于生产。本文不声称已完成真实
> CHR 验收。

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

> **敏感值提示：** RouterOS Container Environment 是配置存储，而不是密钥存储。
> `/container/envs` 中的任何值，对任何可以读取或导出路由器配置的人都是可见的。
> `MIKROZIA_ADMIN_USERNAME` 与 `MIKROZIA_ADMIN_PASSWORD` 是管理员账号的唯一来源，
> 必须**同时**设置；若只存在其中一个，面板将拒绝启动。Mikrozia 不会把这些值写入日志，
> 也不会通过 API 返回。

如果目录已存在，请跳过对应的 `/file/add` 行。查看解压进度：

```routeros
/container/print detail
/log/print follow-only where topics~"container"
```

镜像提取（Extract）完成后：

```routeros
/container/start [find where comment="[Mikrozia:container] Container"]
```

安装只创建 Bridge/VETH、挂载、环境变量和容器；不会创建 firewall、NAT、
端口转发或路由规则。

## 访问与数据

- 面板在容器内监听 `http://172.16.10.2:2053/`。
- 数据库与设置位于持久化挂载 `/etc/x-ui`，用户证书位于 `/root/cert`。
- 镜像内置版本锁定的 ACME 客户端，可在容器内签发证书：HTTP-01（手动续期）或 Cloudflare DNS-01（自动续期）。证书与账户状态持久化在 `/root/cert`。Mikrozia 绝不创建或修改 RouterOS 防火墙、NAT 或端口转发规则。
- 防火墙、NAT、端口转发、路由和访问控制策略完全由 RouterOS 管理员负责；Mikrozia
  从不创建或修改任何网络规则。

## 更新

拉取新的版本化镜像，使用相同的 `/etc/x-ui` 与 `/root/cert` 挂载重新创建容器，并在
验证新版本之前保留旧镜像。容器内没有软件更新器，且不得从容器内部重写镜像 RootFS。

## 上游与许可证

- Upstream: [EchoDev7/Mikrozia (forked from MHSanaei/3x-ui v3.11.0)](https://github.com/EchoDev7/Mikrozia (forked from MHSanaei/3x-ui v3.11.0))
- Xray: [XTLS/Xray-core](https://github.com/XTLS/Xray-core)
- License: GPL-3.0
