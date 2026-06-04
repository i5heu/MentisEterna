#!/bin/sh
set -eu

: "${PROXY_BASIC_AUTH_USERNAME:?PROXY_BASIC_AUTH_USERNAME is required}"
: "${PROXY_BASIC_AUTH_PASSWORD:?PROXY_BASIC_AUTH_PASSWORD is required}"

HASHED_PASSWORD=$(caddy hash-password --plaintext "$PROXY_BASIC_AUTH_PASSWORD")

# Deterministic session token derived from credentials (sha256 so the raw
# password never appears on disk or in a cookie). Changing credentials will
# invalidate any cookie issued under the old pair.
COOKIE_VALUE=$(printf '%s:%s:mentisetterna-session' \
    "$PROXY_BASIC_AUTH_USERNAME" "$PROXY_BASIC_AUTH_PASSWORD" | sha256sum | cut -d' ' -f1)

cat > /etc/caddy/Caddyfile <<'CADDYFILE'
{
    servers {
        trusted_proxies static private_ranges
    }
}

:8080 {
    # Short-circuit: valid proxy session cookie → no basic-auth prompt.
    @hasSession {
        expression {http.request.cookie.mentis_proxy_session} == "COOKIE_PLACEHOLDER"
    }

    handle @hasSession {
        reverse_proxy mentis:8080 {
            header_down Set-Cookie `; Secure` ""
        }
    }

    handle {
        # The 'realm' must be defined on the directive line.
        # Note: 'basic_auth' is the standard v2 syntax (basicauth is just an alias).
        basic_auth * bcrypt "MentisEterna" {
            USER_PLACEHOLDER HASH_PLACEHOLDER
        }

        # Execution only reaches this line if basic_auth passes successfully!
        header Set-Cookie "mentis_proxy_session=COOKIE_PLACEHOLDER; Path=/; HttpOnly; SameSite=Lax; Max-Age=2592000"

        reverse_proxy mentis:8080 {
            header_down Set-Cookie `; Secure` ""
        }
    }
}
CADDYFILE

sed -i \
    -e "s|COOKIE_PLACEHOLDER|${COOKIE_VALUE}|g" \
    -e "s|USER_PLACEHOLDER|${PROXY_BASIC_AUTH_USERNAME}|g" \
    -e "s|HASH_PLACEHOLDER|${HASHED_PASSWORD}|g" \
    /etc/caddy/Caddyfile

exec caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
