#!/usr/bin/env python3
"""Generate the certificates for spike S4.

public-ca     stands in for a real public CA; clients trust it; signs the real server cert
test-ca       Chaos Gateway's own test CA; trusted only by "dev firmware" clients
leafs signed by test-ca for broker.example.com: valid, expired, notyet, wronghost
selfsigned    self-signed broker.example.com
"""
import datetime as dt, os, sys
from cryptography import x509
from cryptography.x509.oid import NameOID, ExtendedKeyUsageOID
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec

OUT = sys.argv[1] if len(sys.argv) > 1 else "certs"
os.makedirs(OUT, exist_ok=True)
now = dt.datetime.now(dt.timezone.utc)


def key():
    return ec.generate_private_key(ec.SECP256R1())


def save(name, k, c):
    with open(f"{OUT}/{name}.key", "wb") as f:
        f.write(k.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8,
                                serialization.NoEncryption()))
    with open(f"{OUT}/{name}.crt", "wb") as f:
        f.write(c.public_bytes(serialization.Encoding.PEM))


def ca(name):
    k = key()
    n = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, name)])
    c = (x509.CertificateBuilder().subject_name(n).issuer_name(n).public_key(k.public_key())
         .serial_number(x509.random_serial_number())
         .not_valid_before(now - dt.timedelta(days=1)).not_valid_after(now + dt.timedelta(days=3650))
         .add_extension(x509.BasicConstraints(ca=True, path_length=None), critical=True)
         .add_extension(x509.KeyUsage(True, False, False, False, False, True, True, False, False), critical=True)
         .sign(k, hashes.SHA256()))
    save(name, k, c)
    return k, c


def leaf(name, host, issuer, nb, na):
    k = key()
    ik, ic = issuer if issuer else (k, None)
    subj = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, host)])
    c = (x509.CertificateBuilder().subject_name(subj).issuer_name(ic.subject if ic else subj)
         .public_key(k.public_key()).serial_number(x509.random_serial_number())
         .not_valid_before(nb).not_valid_after(na)
         .add_extension(x509.SubjectAlternativeName([x509.DNSName(host)]), critical=False)
         .add_extension(x509.ExtendedKeyUsage([ExtendedKeyUsageOID.SERVER_AUTH]), critical=False)
         .sign(ik, hashes.SHA256()))
    save(name, k, c)


pub = ca("public-ca")
test = ca("test-ca")
day = dt.timedelta(days=1)
leaf("real-server", "broker.example.com", pub, now - day, now + 365 * day)
leaf("valid", "broker.example.com", test, now - day, now + 365 * day)
leaf("expired", "broker.example.com", test, now - 400 * day, now - 30 * day)
leaf("notyet", "broker.example.com", test, now + 30 * day, now + 400 * day)
leaf("wronghost", "other.example.net", test, now - day, now + 365 * day)
leaf("selfsigned", "broker.example.com", None, now - day, now + 365 * day)
# bundle for "dev firmware" clients: trust public CA + test CA
open(f"{OUT}/devfw-bundle.crt", "wb").write(open(f"{OUT}/public-ca.crt", "rb").read() +
                                            open(f"{OUT}/test-ca.crt", "rb").read())
print("certs written to", OUT)
