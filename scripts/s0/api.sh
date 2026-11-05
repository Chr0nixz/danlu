#!/usr/bin/env bash
# 在服务器上用临时设备令牌访问本机服务，保留原地址的 HTTPS 校验。
s0_open_api() {
  local info fields credentials
  info=$("$DANLU" info --json) || return 1
  fields=$(python3 -c 'import json,sys,urllib.parse
d=json.load(sys.stdin)
base=urllib.parse.urlsplit(d["base_url"])
print(d["base_url"], base.hostname, base.port, urllib.parse.urlsplit(d["url"]).path.lstrip("/"), d["tls_mode"], d["data_dir"], sep="\n")' <<< "$info") || return 1
  local -a parsed
  mapfile -t parsed <<< "$fields"
  S0_BASE_URL=${parsed[0]}
  S0_PUBLIC_HOST=${parsed[1]}
  S0_ENTRANCE=${parsed[3]}
  S0_CURL=(--noproxy '*' --connect-to "::127.0.0.1:${parsed[2]}" --max-time 10)
  if [ "${parsed[4]}" = local-ca ]; then
    S0_CURL+=(--cacert "${parsed[5]}/tls/ca.crt")
  fi

  credentials=$("$DANLU" token create --name "$1" --json) || return 1
  fields=$(python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["id"], d["token"])' <<< "$credentials") || return 1
  read -r S0_TOKEN_ID S0_TOKEN <<< "$fields"
  [ -n "$S0_TOKEN_ID" ] && [ -n "$S0_TOKEN" ]
}

s0_api() {
  local path=$1
  shift
  # 令牌经 stdin 传给 curl，避免出现在进程命令行和采集报告里。
  printf 'Authorization: Bearer %s\n' "$S0_TOKEN" | curl -fsS "${S0_CURL[@]}" -H @- "$S0_BASE_URL/api/$path" "$@"
}

s0_close_api() {
  if [ -n "${S0_TOKEN_ID:-}" ]; then
    "$DANLU" token revoke "$S0_TOKEN_ID" >/dev/null || echo "请手动吊销临时令牌 #$S0_TOKEN_ID" >&2
  fi
}
