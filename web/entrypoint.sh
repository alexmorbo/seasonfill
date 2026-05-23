#!/bin/sh
# Seasonfill web — render nginx.conf.template with env vars and launch nginx.
#
# Why envsubst (and not Helm-style templating or a Go binary):
#   - nginx-unprivileged-alpine ships with /bin/sh + a small toolbelt
#     once we add gettext (provides envsubst).
#   - We only substitute a handful of well-known variables. Whitelisting
#     them prevents envsubst from accidentally clobbering nginx config
#     directives that happen to contain `$` (e.g. `$host`, `$remote_addr`).
#
# The whitelist below is the SINGLE source of truth for variables the
# template may reference. Add new ones explicitly — `envsubst` with no
# argument list would substitute EVERY `$NAME` in the file, which would
# corrupt nginx's own variables.
set -eu

: "${SEASONFILL_BACKEND_URL:=http://backend:8080}"

TEMPLATE=/etc/nginx/templates/seasonfill.conf.template
TARGET=/etc/nginx/conf.d/seasonfill.conf

if [ ! -f "$TEMPLATE" ]; then
    echo "entrypoint: missing template at $TEMPLATE" >&2
    exit 1
fi

# Whitelist of substitutable vars. Nginx vars like $host/$remote_addr
# stay literal because they are not listed here.
export SEASONFILL_BACKEND_URL
envsubst '${SEASONFILL_BACKEND_URL}' < "$TEMPLATE" > "$TARGET"

echo "entrypoint: rendered $TARGET with SEASONFILL_BACKEND_URL=$SEASONFILL_BACKEND_URL"

exec nginx -g 'daemon off;'
