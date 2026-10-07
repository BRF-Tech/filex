#!/usr/bin/env bash
# Test certificates for the realenv run, made fresh each run in $1 (run.sh
# calls it inside the runner image, which has openssl):
#
#   ca.pem / ca.key          the run's own CA ("a tenant's own CA")
#   ldap.crt / ldap.key      OpenLDAP's ldaps:// certificate, signed by it
#   dhparam.pem              for OpenLDAP: the RFC 7919 ffdhe2048 group
#                            (stack/ffdhe2048.pem). Not `openssl dhparam
#                            -dsaparam`: OpenSSL 3 writes that as X9.42
#                            parameters, which OpenLDAP's GnuTLS refuses
#                            ("Add TLS config" fails with status 80)
#   own-proxy.crt / .key     the certificate a tenant brings for its own
#                            domain (files.acme-proxy.test), signed by it
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
out=${1:?usage: certs.sh <dir>}
mkdir -p "$out"
cd "$out"

openssl req -x509 -newkey rsa:2048 -nodes -sha256 -days 3 \
  -subj "/CN=filex realenv test CA" \
  -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" \
  -keyout ca.key -out ca.pem 2>/dev/null

leaf() {
  local name=$1 kind=$2 san=$3
  if [ "$kind" = ec ]; then
    openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=${san%%,*}" \
      -keyout "$name.key" -out "$name.csr" 2>/dev/null
  else
    openssl req -newkey rsa:2048 -nodes -subj "/CN=${san%%,*}" \
      -keyout "$name.key" -out "$name.csr" 2>/dev/null
  fi
  local sans=""
  local IFS=,
  for n in $san; do sans="${sans:+$sans,}DNS:$n"; done
  printf 'subjectAltName=%s\nextendedKeyUsage=serverAuth\nbasicConstraints=CA:FALSE\n' "$sans" > "$name.ext"
  openssl x509 -req -sha256 -days 2 -in "$name.csr" -CA ca.pem -CAkey ca.key -CAcreateserial \
    -extfile "$name.ext" -out "$name.crt" 2>/dev/null
  rm -f "$name.csr" "$name.ext"
}

leaf ldap rsa "ldap.example.test,ldap-private.test"
leaf own-proxy ec "files.acme-proxy.test"
cp "$here/ffdhe2048.pem" dhparam.pem
chmod 644 ./*.pem ./*.crt ./*.key
ls -1
