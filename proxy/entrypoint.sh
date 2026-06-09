#!/bin/sh
set -eu

: "${PROXY_BASIC_AUTH_USERNAME:?PROXY_BASIC_AUTH_USERNAME is required}"
: "${PROXY_BASIC_AUTH_PASSWORD:?PROXY_BASIC_AUTH_PASSWORD is required}"
: "${PUBLIC_BASE_URL:?PUBLIC_BASE_URL is required}"
: "${MAX_UPLOAD_BYTES:=67108864}"

case "$PUBLIC_BASE_URL" in
    https://*)
        SITE_ADDRESS="$PUBLIC_BASE_URL"
        ;;
    http://localhost*|http://127.0.0.1*|http://[::1]*)
        SITE_ADDRESS=":80"
        ;;
    http://*)
        echo "proxy: PUBLIC_BASE_URL must use https:// for non-localhost deployments" >&2
        exit 1
        ;;
    *)
        echo "proxy: PUBLIC_BASE_URL must include an explicit scheme, e.g. https://notes.example.com" >&2
        exit 1
        ;;
esac

HASHED_PASSWORD=$(caddy hash-password --plaintext "$PROXY_BASIC_AUTH_PASSWORD")

cat > /etc/caddy/Caddyfile <<'CADDYFILE'
{
    servers {
        trusted_proxies static private_ranges
    }
}

SITE_ADDRESS_PLACEHOLDER {
    basic_auth * bcrypt "MentisEterna" {
        USER_PLACEHOLDER HASH_PLACEHOLDER
    }

    request_body {
        max_size MAX_UPLOAD_BYTES_PLACEHOLDER
    }

    reverse_proxy mentis:8080
}
CADDYFILE

sed -i \
    -e "s|SITE_ADDRESS_PLACEHOLDER|${SITE_ADDRESS}|g" \
    -e "s|USER_PLACEHOLDER|${PROXY_BASIC_AUTH_USERNAME}|g" \
    -e "s|HASH_PLACEHOLDER|${HASHED_PASSWORD}|g" \
    -e "s|MAX_UPLOAD_BYTES_PLACEHOLDER|${MAX_UPLOAD_BYTES}|g" \
    /etc/caddy/Caddyfile

exec caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
