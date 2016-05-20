#!/usr/bin/env bash

mkdir -p ca server client

# Certification Authority certificate
openssl genrsa -aes256 -out ca/ca.key 4096
chmod 400 ca/ca.key
openssl req -new -x509 -sha256 -days 730 -key ca/ca.key -out ca/ca.crt
chmod 444 ca/ca.crt

# The Certificate Signing Request (CSR)
openssl genrsa -out server/server.key 2048
chmod 400 server/server.key
openssl req -new -key server/server.key -sha256 -out server/server.csr

# The server certificate
openssl x509 -req -days 365 -sha256 -in server/server.csr -CA ca/ca.crt -CAkey ca/ca.key -set_serial 1 -out server/server.crt
chmod 444 server/server.crt
openssl verify -CAfile ca/ca.crt server/server.crt

# The client certificate
openssl genrsa -out client/client.key 2048
openssl req -new -key client/client.key -out client/client.csr
openssl x509 -req -days 365 -sha256 -in client/client.csr -CA ca/ca.crt -CAkey ca/ca.key -set_serial 2 -out client/client.crt
