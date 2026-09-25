# Go Proxy IPv6 Pool

Go Proxy IPv6 Pool 是一个随机 IPv6 出口代理服务，支持 HTTP 代理和 SOCKS5 代理。

当前版本支持：

- 动态随机 IPv6 出口端口
- 粘性 IPv6 轮换 HTTP/SOCKS5 端口
- 固定端口绑定固定 IPv6 出口
- HTTP 代理账号密码认证
- SOCKS5 代理账号密码认证
- 固定端口 IPv6 分配持久化
- 保留旧版 `--port` / `--cidr` 启动方式

## 工作方式

程序需要一个可用的 IPv6 CIDR，例如：

```text
2001:399:8205:ae00::/64
```

动态端口会在每次出站连接时从 CIDR 中随机生成一个 IPv6，并把它作为本地出口源地址。

粘性端口会按用户名保存 IPv6。在轮换周期内，同一个用户名的连接使用同一个 IPv6，周期结束后首次建立的新连接会生成新的 IPv6。粘性端口默认按配置中的 `rotation_seconds` 轮换，也可以在用户名末尾指定周期，例如 `proxy_user-120` 表示粘性 120 秒。

固定端口会在首次启动时为每个端口随机分配一个 IPv6，并写入状态文件。之后服务重启时，同一个固定端口会继续使用同一个 IPv6。

## 配置文件

推荐使用配置文件启动。可以复制示例配置：

```bash
cp config.example.yaml config.yaml
```

示例：

```yaml
cidr: "2001:399:8205:ae00::/64"
state_file: "state.json"
verbose: false

auth:
  username: "proxy_user"
  password: "proxy_password"

whitelist:
  - "127.0.0.1"
  - "::1"
  # - "192.168.1.0/24"
  # - "2001:db8::/32"

admin:
  enabled: false
  listen: "127.0.0.1:52120"
  token: "change-this-admin-token"

dynamic:
  http_port: 52122
  socks5_port: 52123

sticky:
  http_port: 52124
  socks5_port: 52125
  rotation_seconds: 60

fixed:
  http_ports:
    - 52133
    - 52134
  socks5_ports:
    - 52135
```

字段说明：

- `cidr`：IPv6 网段，必填。
- `state_file`：固定端口 IPv6 映射的持久化文件，默认 `state.json`。
- `verbose`：是否打印更详细的 HTTP 代理日志。
- `auth.username` / `auth.password`：代理认证账号密码。两者都为空表示不启用认证；只配置其中一个会启动失败。
- `whitelist`：认证白名单，支持单个 IP 和 CIDR。白名单内客户端在配置账号密码时也可以免密使用代理。
- `admin.enabled`：是否启用管理 API。
- `admin.listen`：管理 API 监听地址，建议默认只监听 `127.0.0.1`。
- `admin.token`：管理 API Bearer token，启用管理 API 时必须配置。
- `dynamic.http_port`：动态 HTTP 代理端口。
- `dynamic.socks5_port`：动态 SOCKS5 代理端口。
- `sticky.http_port`：粘性 HTTP 代理端口，`0` 表示不启用。
- `sticky.socks5_port`：粘性 SOCKS5 代理端口，`0` 表示不启用。
- `sticky.rotation_seconds`：粘性代理默认轮换周期，单位为秒。
- `fixed.http_ports`：固定 IPv6 的 HTTP 代理端口列表。
- `fixed.socks5_ports`：固定 IPv6 的 SOCKS5 代理端口列表。

管理 API 的完整用法见 [API_DOCUMENTATION.md](API_DOCUMENTATION.md)。

## 启动

开发调试时可以直接使用源码启动。使用默认配置文件 `config.yaml`：

```bash
go run .
```

指定配置文件：

```bash
go run . --config ./config.yaml
```

## 使用二进制程序

如果你拿到的是已经编译好的二进制文件，例如：

```text
go-proxy-ipv6-pool-linux-amd64
```

先给它添加执行权限：

```bash
chmod +x go-proxy-ipv6-pool-linux-amd64
```

准备配置文件：

```bash
cp config.example.yaml config.yaml
```

然后编辑 `config.yaml`，至少把 `cidr` 改成服务器真实可用的 IPv6 CIDR。

使用默认配置文件启动：

```bash
./go-proxy-ipv6-pool-linux-amd64
```

指定配置文件启动：

```bash
./go-proxy-ipv6-pool-linux-amd64 --config ./config.yaml
```

查看二进制版本信息：

```bash
./go-proxy-ipv6-pool-linux-amd64 --version
```

也可以继续使用兼容旧版的命令行参数，只启动动态 HTTP/SOCKS5 代理：

```bash
./go-proxy-ipv6-pool-linux-amd64 --port 52122 --cidr "2001:399:8205:ae00::/64"
```

如果使用 GitHub Release 下载的文件，常见文件名为：

```text
go-proxy-ipv6-pool-linux-amd64
go-proxy-ipv6-pool-linux-arm64
```

其中：

- `linux-amd64`：用于普通 x86_64 Linux 服务器。
- `linux-arm64`：用于 ARM64 Linux 服务器。

Release 中的 `.sha256` 文件用于校验二进制是否完整，例如：

```bash
sha256sum -c go-proxy-ipv6-pool-linux-amd64.sha256
```

源码启动也可以继续使用旧版参数，只启动动态 HTTP/SOCKS5 代理：

```bash
go run . --port 52122 --cidr "2001:399:8205:ae00::/64"
```

旧版方式会启动：

- HTTP 动态代理：`52122`
- SOCKS5 动态代理：`52123`

旧版方式不会配置账号密码，也不会配置固定端口。

## 使用动态代理

假设配置如下：

```yaml
auth:
  username: "proxy_user"
  password: "proxy_password"

dynamic:
  http_port: 52122
  socks5_port: 52123
```

HTTP 代理：

```bash
curl -x http://proxy_user:proxy_password@服务器IP:52122 http://ipv6.ip.sb/
```

SOCKS5 代理：

```bash
curl -x socks5://proxy_user:proxy_password@服务器IP:52123 http://ipv6.ip.sb/
```

如果没有配置 `auth.username` 和 `auth.password`，则不需要账号密码：

```bash
curl -x http://服务器IP:52122 http://ipv6.ip.sb/
curl -x socks5://服务器IP:52123 http://ipv6.ip.sb/
```

## 查询客户端 IP

HTTP 代理端口同时提供一个很小的非代理查询接口，用来查看服务端看到的客户端 IP，方便加入白名单。

查看纯文本 IP：

```bash
curl http://服务器IP:52122/ip
```

示例返回：

```text
1.2.3.4
```

查看更详细的 JSON：

```bash
curl http://服务器IP:52122/whoami
```

示例返回：

```json
{
  "ip": "1.2.3.4",
  "remote_addr": "1.2.3.4:54321"
}
```

直接访问 HTTP 代理端口根路径会返回提示：

```bash
curl http://服务器IP:52122/
```

```text
This is a proxy server. Use /ip to view your client IP.
```

`/ip` 和 `/whoami` 不要求账号密码，也不要求客户端已经在白名单内。它们只返回 TCP 连接的真实 `RemoteAddr`，不会信任客户端伪造的 `X-Forwarded-For`、`X-Real-IP` 等请求头。

## 使用粘性轮换端口

粘性端口需要配置 `auth.username` 和 `auth.password`。用户名格式为 `<auth.username>-<秒数>`，密码仍使用 `auth.password`：

```bash
curl -x http://proxy_user-120:proxy_password@服务器IP:52124 http://ipv6.ip.sb/
curl -x socks5://proxy_user-500:proxy_password@服务器IP:52125 http://ipv6.ip.sb/
```

同一粘性用户名在指定周期内复用同一个 IPv6，周期结束后自动轮换。直接使用 `proxy_user` 时使用 `sticky.rotation_seconds` 配置的默认周期。

## 使用固定 IPv6 端口

假设配置：

```yaml
fixed:
  http_ports:
    - 52133
    - 52134
  socks5_ports:
    - 52135
```

首次启动后，程序会自动生成 `state.json`：

```json
{
  "fixed_ports": {
    "52133": "2001:399:8205:ae00:1111:2222:3333:4444",
    "52134": "2001:399:8205:ae00:5555:6666:7777:8888",
    "52135": "2001:399:8205:ae00:9999:aaaa:bbbb:cccc"
  }
}
```

之后：

- 访问 `52133` 时，始终使用 `state.json` 中 `52133` 对应的 IPv6 出口。
- 访问 `52134` 时，始终使用 `state.json` 中 `52134` 对应的 IPv6 出口。
- 访问 `52135` 时，始终使用 `state.json` 中 `52135` 对应的 IPv6 出口。

示例：

```bash
curl -x http://proxy_user:proxy_password@服务器IP:52133 http://ipv6.ip.sb/
curl -x http://proxy_user:proxy_password@服务器IP:52134 http://ipv6.ip.sb/
curl -x socks5://proxy_user:proxy_password@服务器IP:52135 http://ipv6.ip.sb/
```

只要不删除或修改 `state.json`，固定端口对应的 IPv6 就会保持不变。

## 认证规则

当 `auth.username` 和 `auth.password` 都为空时：

- HTTP 代理不要求认证。
- SOCKS5 代理不要求认证。

当配置了 `auth.username` 和 `auth.password` 时：

- 白名单内客户端可以不带账号密码直接使用 HTTP 和 SOCKS5 代理。
- 非白名单客户端的 HTTP 代理请求必须携带正确的 `Proxy-Authorization`。
- 非白名单客户端的 SOCKS5 代理请求必须使用用户名密码认证。
- 动态端口和固定端口都会启用同一组账号密码。

白名单支持单个 IP 和 CIDR：

```yaml
whitelist:
  - "127.0.0.1"
  - "::1"
  - "192.168.1.0/24"
  - "2001:db8::/32"
```

白名单判断只使用 TCP 连接的 `RemoteAddr`，不会信任客户端传入的 HTTP 头。

## 固定端口数量和资源占用

固定端口数量可以配置很多，例如 200 个。空闲状态下，每个固定端口主要占用：

- 1 个监听 socket
- 1 个 goroutine
- 少量内存

真正的资源压力主要来自活跃连接数量，而不是端口数量本身。如果固定端口很多、并发也很高，建议检查系统文件描述符限制：

```bash
ulimit -n
```

必要时调高系统限制，并确保防火墙、安全组、容器端口映射已经放行这些端口。

## 注意事项

服务器必须允许使用 CIDR 内的 IPv6 作为本地出站源地址。不同系统和云厂商的网络策略不同，可能需要额外配置 IPv6 路由、IPv6 地址段或非本地地址绑定能力。

如果固定端口已经写入 `state.json`，后来修改了 `cidr`，程序会检查旧 IPv6 是否仍属于新的 CIDR；如果不属于，会自动为该端口重新分配 IPv6。

## License

MIT License，见 [LICENSE](LICENSE)。
