#!/bin/sh
# Substitute only API_URL — leave nginx variables ($uri, $host, etc.) intact
envsubst '${API_URL}' < /etc/nginx/nginx.conf.template > /etc/nginx/conf.d/default.conf
exec nginx -g 'daemon off;'
