# ![Hysteria 2](logo.svg)

[![License][1]][2] [![Release][3]][4] [![Telegram][5]][6] [![Discussions][7]][8]

[1]: https://img.shields.io/badge/license-MIT-blue
[2]: LICENSE.md
[3]: https://img.shields.io/github/v/release/apernet/hysteria?style=flat-square
[4]: https://github.com/apernet/hysteria/releases
[5]: https://img.shields.io/badge/chat-Telegram-blue?style=flat-square
[6]: https://t.me/hysteria_github
[7]: https://img.shields.io/github/discussions/apernet/hysteria?style=flat-square
[8]: https://github.com/apernet/hysteria/discussions

<h2 style="text-align: center;">Hysteria is a powerful, lightning-fast, and censorship-resistant proxy.</h2>

### [Get Started](https://v2.hysteria.network/)

### [中文文档](https://v2.hysteria.network/zh/)

### [Hysteria 1.x (legacy)](https://v1.hysteria.network/)

---

## Hysteria × Xboard 快速部署

这个 fork 基于官方 Hysteria，增加了 Xboard 用户同步、本地鉴权、持久化流量批次和幂等上报。协议、客户端和 `core` 数据面保持官方实现。

> [!IMPORTANT]
> 可靠流量上报依赖配套的 Xboard V2 batch-ledger 补丁。必须先完成面板 migration、queue worker/Horizon 和 scheduler，再启动节点。仅启动本容器而不升级面板时，用户同步可以成功，但旧面板不会返回有效的流量 ACK。

### 最简单的方式：Compose 本地构建

前置条件：

- Linux 服务器已安装 Docker 与 Docker Compose v2；
- 一个域名已通过 A/AAAA 记录解析到这台服务器；
- 公网可以访问服务器的 TCP 80 和 UDP 443；
- 已在 Xboard 创建对应的 Hysteria 节点并取得 server token；
- 已部署[配套 Xboard 后端补丁](integrations/xboard/README.md)。

```bash
git clone --branch feature/xboard https://github.com/Dsd1001/hysteria-xboard.git
cd hysteria-xboard

./scripts/xboard-init.sh
docker compose up -d --build
docker compose logs -f hysteria
```

初始化脚本只会询问：

1. Xboard 面板地址，例如 `https://panel.example.com`；
2. Xboard 节点 ID/code；
3. Hysteria 服务域名；
4. ACME 邮箱；
5. Xboard server token（输入时不会回显）。

脚本会生成：

```text
deploy/xboard/server.yaml
deploy/xboard/secrets/xboard_token
deploy/xboard/data/
```

token、运行配置、ACME 证书、用户缓存和流量 spool 都已加入 `.gitignore`，不会误提交到 GitHub。token 文件权限为 `0600`。

查看状态：

```bash
docker compose ps
docker compose logs --tail=200 hysteria
```

重启或更新：

```bash
git pull --ff-only
docker compose up -d --build
```

停止服务但保留数据：

```bash
docker compose down
```

不要删除 `deploy/xboard/data/`。其中包含用户缓存、ACME 证书和未确认的流量批次。

### 使用 GitHub Container Registry 镜像

本分支包含 `.github/workflows/xboard-docker.yml`，会为 amd64/arm64 构建：

```text
ghcr.io/dsd1001/hysteria-xboard:latest
ghcr.io/dsd1001/hysteria-xboard:feature-xboard
```

首次发布后需要在 GitHub Packages 中把镜像可见性设为 Public。之后可以跳过本地构建：

```bash
docker pull ghcr.io/dsd1001/hysteria-xboard:latest
HYSTERIA_IMAGE=ghcr.io/dsd1001/hysteria-xboard:latest \
  docker compose up -d --no-build
```

### 手动配置

- ACME 配置模板：[`deploy/xboard/server.acme.yaml.example`](deploy/xboard/server.acme.yaml.example)
- 自有证书配置模板：[`examples/xboard/server.yaml`](examples/xboard/server.yaml)
- 详细架构、故障和回滚说明：[`docs/xboard.md`](docs/xboard.md)

### 安全与可靠性边界

- Xboard token 通过 Bearer Header 发送，不进入 URL；
- 鉴权热路径只访问本地不可变用户快照；
- 面板故障时保留 last-known-good 用户缓存；
- 流量由内存 collector 定期写入 bbolt spool，收到 `accepted` / `already_processed` 后才删除；
- 强制断电仍存在最多约一个 `flushInterval`（默认 1 秒）的内存计费窗口；
- 第一阶段不提供用户级动态限速、在线 IP 上报或独立 `/healthz`。

---

<div class="feature-grid">
  <div>
    <h3>🛠️ Jack of all trades</h3>
    <p>Wide range of modes including SOCKS5, HTTP Proxy, TCP/UDP Forwarding, Linux TProxy, TUN - with more features being added constantly.</p>
  </div>

  <div>
    <h3>⚡ Blazing fast</h3>
    <p>Powered by a customized QUIC protocol, Hysteria is designed to deliver unparalleled performance over unreliable and lossy networks.</p>
  </div>

  <div>
    <h3>✊ Censorship resistant</h3>
    <p>The protocol masquerades as standard HTTP/3 traffic, making it very difficult for censors to detect and block without widespread collateral damage.</p>
  </div>
  
  <div>
    <h3>💻 Cross-platform</h3>
    <p>We have builds for every major platform and architecture. Deploy anywhere & use everywhere. Not to mention the long list of 3rd party apps.</p>
  </div>

  <div>
    <h3>🔗 Easy integration</h3>
    <p>With built-in support for custom authentication, traffic statistics & access control, Hysteria is easy to integrate into your infrastructure.</p>
  </div>
  
  <div>
    <h3>🤗 Chill and supportive</h3>
    <p>We have well-documented specifications and code for developers to contribute and/or build their own apps. And a helpful community, too.</p>
  </div>
</div>

---

**If you find Hysteria useful, consider giving it a ⭐️!**
