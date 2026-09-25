# Go Proxy IPv6 Pool 管理 API 文档

管理 API 用于运行时查看状态、维护白名单、批量添加 fixed 端口，以及重置 fixed 端口的 IPv6。管理 API 是独立端口，默认建议只监听本机地址。

## 启用管理 API

在 `config.yaml` 中配置：

```yaml
admin:
  enabled: true
  listen: "127.0.0.1:52120"
  token: "change-this-admin-token"
```

字段说明：

- `admin.enabled`：是否启用管理 API。
- `admin.listen`：管理 API 监听地址，建议默认使用 `127.0.0.1:52120`。
- `admin.token`：管理 API 鉴权 token，必须设置为足够随机的长字符串。

如果需要远程访问，可以改为：

```yaml
admin:
  enabled: true
  listen: "0.0.0.0:52120"
  token: "change-this-admin-token"
```

远程暴露时请务必配合防火墙或安全组限制来源 IP。

## 鉴权

所有管理 API 都必须携带：

```http
Authorization: Bearer <admin.token>
```

示例：

```bash
curl -H "Authorization: Bearer change-this-admin-token" \
  http://127.0.0.1:52120/api/status
```

未携带或 token 错误会返回：

```json
{"error":"unauthorized"}
```

## GET /api/status

查看服务运行状态。

```bash
curl -H "Authorization: Bearer change-this-admin-token" \
  http://127.0.0.1:52120/api/status
```

响应示例：

```json
{
  "version": "version=v1.0.2 commit=... build_time=...",
  "cidr": "2001:bc8:710:ab41::/64",
  "config": "/root/proxy/config.yaml",
  "state": "/root/proxy/state.json",
  "auth_enabled": true,
  "whitelist_enabled": true,
  "whitelist": ["127.0.0.1", "::1"],
  "admin_listen": "127.0.0.1:52120",
  "dynamic": {
    "http_port": 53001,
    "socks5_port": 53002
  },
  "sticky": {
    "http_port": 53005,
    "socks5_port": 53006,
    "rotation_seconds": 60
  },
  "fixed_count": 2,
  "fixed_ports": [
    {
      "port": 53003,
      "type": "http",
      "ip": "2001:bc8:710:ab41:b844:9dd3:7daf:3e99",
      "running": true
    },
    {
      "port": 53004,
      "type": "socks5",
      "ip": "2001:bc8:710:ab41:87ca:e719:4ed8:20c0",
      "running": true
    }
  ]
}
```

## GET /api/fixed-ports

查看所有 fixed 端口。

```bash
curl -H "Authorization: Bearer change-this-admin-token" \
  http://127.0.0.1:52120/api/fixed-ports
```

响应示例：

```json
{
  "fixed_ports": [
    {
      "port": 53003,
      "type": "http",
      "ip": "2001:bc8:710:ab41:b844:9dd3:7daf:3e99",
      "running": true
    },
    {
      "port": 53004,
      "type": "socks5",
      "ip": "2001:bc8:710:ab41:87ca:e719:4ed8:20c0",
      "running": true
    }
  ]
}
```

## POST /api/fixed-ports

批量添加 fixed 端口。支持逐个添加，也支持范围添加。

请求示例：

```bash
curl -X POST \
  -H "Authorization: Bearer change-this-admin-token" \
  -H "Content-Type: application/json" \
  -d '{
    "ports": [
      {"port": 53012, "type": "socks5"},
      {"port": 53020, "type": "http"}
    ],
    "ranges": [
      {"start": 53100, "end": 53110, "type": "socks5"}
    ]
  }' \
  http://127.0.0.1:52120/api/fixed-ports
```

`type` 支持：

- `http`
- `socks5`

成功后会：

- 为每个新 fixed 端口分配一个 IPv6。
- 写入 `state.json`。
- 写入 `config.yaml`。
- 立即启动对应端口监听。

响应示例：

```json
{
  "added": [
    {
      "port": 53012,
      "type": "socks5",
      "ip": "2001:bc8:710:ab41:1111:2222:3333:4444",
      "running": true
    }
  ]
}
```

如果端口已存在、端口被占用、端口与动态端口冲突，或者类型非法，会返回 `400`。

## POST /api/fixed-ports/{port}/reset-ip

重置某个 fixed 端口的 IPv6。新连接会立刻使用新 IPv6，已有连接不受影响。

```bash
curl -X POST \
  -H "Authorization: Bearer change-this-admin-token" \
  http://127.0.0.1:52120/api/fixed-ports/53004/reset-ip
```

响应示例：

```json
{
  "port": 53004,
  "type": "socks5",
  "old_ip": "2001:bc8:710:ab41:87ca:e719:4ed8:20c0",
  "new_ip": "2001:bc8:710:ab41:aaaa:bbbb:cccc:dddd"
}
```

该接口会更新：

- 运行时 fixed IP store。
- `state.json`。

该接口不会修改端口类型，也不会重启监听器。

## POST /api/whitelist/add

添加白名单。支持单个 IP 和 CIDR。

```bash
curl -X POST \
  -H "Authorization: Bearer change-this-admin-token" \
  -H "Content-Type: application/json" \
  -d '{
    "entries": [
      "1.2.3.4",
      "192.168.1.0/24",
      "2001:db8::/32"
    ]
  }' \
  http://127.0.0.1:52120/api/whitelist/add
```

成功后会：

- 更新运行时白名单。
- 写入 `config.yaml`。

响应示例：

```json
{
  "whitelist": [
    "1.2.3.4",
    "192.168.1.0/24",
    "2001:db8::/32"
  ]
}
```

## POST /api/whitelist/delete

删除白名单条目。请求内容必须和配置中的条目文本一致。

```bash
curl -X POST \
  -H "Authorization: Bearer change-this-admin-token" \
  -H "Content-Type: application/json" \
  -d '{
    "entries": [
      "1.2.3.4",
      "192.168.1.0/24"
    ]
  }' \
  http://127.0.0.1:52120/api/whitelist/delete
```

成功后会：

- 更新运行时白名单。
- 写入 `config.yaml`。

响应示例：

```json
{
  "whitelist": [
    "2001:db8::/32"
  ]
}
```

## 错误格式

错误响应统一为：

```json
{"error":"错误信息"}
```

常见状态码：

- `400`：请求参数错误、端口冲突、端口被占用、IP/CIDR 非法。
- `401`：管理 token 错误或缺失。
- `404`：接口不存在。
- `405`：HTTP 方法不允许。

## 安全建议

- 管理 API 默认建议只监听 `127.0.0.1`。
- 如果必须监听公网地址，请使用强随机 `admin.token`。
- 管理 API token 不要和代理账号密码相同。
- 不要把 `admin.token` 写进公开日志、截图或 issue。
