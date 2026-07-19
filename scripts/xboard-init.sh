#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
deploy_dir="$repo_root/deploy/xboard"
config_file="$deploy_dir/server.yaml"
secret_file="$deploy_dir/secrets/xboard_token"

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf '错误：缺少命令 %s\n' "$1" >&2
    exit 1
  fi
}

prompt_required() {
  local prompt="$1"
  local value=""
  while [[ -z "$value" ]]; do
    read -r -p "$prompt" value
  done
  printf '%s' "$value"
}

prompt_email() {
  local email=""
  while true; do
    email="$(prompt_required 'ACME 邮箱：')"
    if [[ "$email" =~ ^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+$ ]]; then
      printf '%s' "$email"
      return
    fi
    printf '错误：邮箱格式无效，请输入真实邮箱地址。\n' >&2
  done
}

remove_empty_placeholder_dir() {
  local path="$1"
  if [[ ! -d "$path" ]]; then
    return
  fi
  if rmdir "$path" 2>/dev/null; then
    printf '已清理 Docker 创建的空占位目录：%s\n' "$path"
    return
  fi
  printf '错误：%s 应为文件，但当前是非空目录；请备份并手工清理。\n' "$path" >&2
  exit 1
}

require_command docker
if ! docker compose version >/dev/null 2>&1; then
  printf '错误：未检测到 Docker Compose v2（docker compose）。\n' >&2
  exit 1
fi

printf 'Hysteria × Xboard 初始化\n'
printf '========================\n'
printf '域名必须已解析到本机；TCP 80 与 UDP 443 必须可从公网访问。\n\n'

panel_url="$(prompt_required 'Xboard 面板地址（例如 https://panel.example.com）：')"
panel_url="${panel_url%/}"
if [[ ! "$panel_url" =~ ^https://[^/?#]+$ ]]; then
  printf '错误：面板地址必须是纯 HTTPS origin，不能包含路径、query 或 fragment。\n' >&2
  exit 1
fi

node_id="$(prompt_required 'Xboard 节点 ID/code：')"
if [[ ! "$node_id" =~ ^[A-Za-z0-9._:-]+$ ]]; then
  printf '错误：节点 ID 只能包含字母、数字、点、下划线、冒号和连字符。\n' >&2
  exit 1
fi

domain="$(prompt_required 'Hysteria 服务域名（例如 hy.example.com）：')"
if [[ ! "$domain" =~ ^[A-Za-z0-9.-]+$ ]] || [[ "$domain" != *.* ]]; then
  printf '错误：域名格式无效。\n' >&2
  exit 1
fi

email="$(prompt_email)"

read -r -s -p 'Xboard server token / 旧配置 apiKey（输入时不会显示）：' token
printf '\n'
if [[ -z "$token" ]]; then
  printf '错误：token 不能为空。\n' >&2
  exit 1
fi

remove_empty_placeholder_dir "$config_file"
remove_empty_placeholder_dir "$secret_file"
mkdir -p "$deploy_dir/data/acme" "$deploy_dir/secrets"
umask 077
printf '%s\n' "$token" > "$secret_file"
chmod 600 "$secret_file"

cat > "$config_file" <<EOF
listen: :443

acme:
  domains:
    - "$domain"
  email: "$email"
  ca: letsencrypt
  type: http
  dir: /var/lib/hysteria/acme

auth:
  type: xboard

xboard:
  baseURL: "$panel_url"
  tokenFile: /run/secrets/xboard_token
  nodeID: "$node_id"
  apiMode: legacy
  timeout: 8s
  users:
    cacheFile: /var/lib/hysteria/xboard-users.json
    pullInterval: 30s
    maxStale: 6h
  traffic:
    spoolFile: /var/lib/hysteria/xboard-traffic.db
    flushInterval: 1s
    batchInterval: 1m
    reportInterval: 5s

masquerade:
  type: 404
EOF
chmod 600 "$config_file"

(
  cd "$repo_root"
  docker compose config --quiet
)

printf '\n初始化完成。\n'
printf '配置：%s\n' "$config_file"
printf 'token：%s（权限 0600，不会提交到 Git）\n' "$secret_file"
printf '\n下一步：\n'
printf '  cd %q\n' "$repo_root"
printf '  docker compose pull\n'
printf '  docker compose up -d\n'
printf '  docker compose logs -f hysteria\n'
