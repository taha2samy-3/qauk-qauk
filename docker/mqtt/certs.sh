#!/usr/bin/env sh
# Dev CA, broker certificate and a client certificate for the mTLS listener
# (task mqtt:certs). Keys stay local (git-ignored). NOT for production.
set -eu
D="$(cd "$(dirname "$0")" && pwd)/certs"
mkdir -p "$D"
cd "$D"
[ -f ca.crt ] && [ -f client.crt ] && { echo "certs already in $D"; exit 0; }
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 3650 \
  -subj "/CN=quack-dev-mqtt-ca" -keyout ca.key -out ca.crt 2>/dev/null
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=localhost" \
  -keyout server.key -out server.csr 2>/dev/null
printf "subjectAltName=DNS:localhost,DNS:mosquitto,IP:127.0.0.1\n" > server.ext
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days 3650 -extfile server.ext -out server.crt 2>/dev/null
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=quack-gateway" \
  -keyout client.key -out client.csr 2>/dev/null
openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days 3650 -out client.crt 2>/dev/null
# a second CA nobody trusts, to test that an unknown client is refused
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 3650 \
  -subj "/CN=untrusted" -keyout rogue.key -out rogue.crt 2>/dev/null
rm -f ./*.csr ./*.srl server.ext
chmod 644 ./*
echo "dev certificates written to $D"
