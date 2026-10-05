# Pebble's HTTPS certificate

`https-cert.pem` and `https-key.pem` are Pebble's own test certificate for its
HTTPS listener (SANs `localhost`, `pebble`, `127.0.0.1`), copied from
`test/certs/localhost/` of github.com/letsencrypt/pebble v2.10.1. They are
public test material, not a secret: `pebble_test.go` trusts the certificate
when it talks to the Pebble it starts. The issued certificates and their
chain come from Pebble's own in-memory CA, read from its management port.
