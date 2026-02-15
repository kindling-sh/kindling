#!/bin/sh
# Substitute only GATEWAY_URL — leave nginx variables ($uri, $host, etc.) intact
envsubst '${GATEWAY_URL}' < /etc/nginx/nginx.conf.template > /etc/nginx/conf.d/default.conf
exec nginx -g 'daemon off;'
