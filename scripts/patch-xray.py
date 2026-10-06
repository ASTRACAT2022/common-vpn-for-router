#!/usr/bin/env python3
"""Apply the narrow Common VPN compatibility patch to Xray v26.6.27.

The patch preserves upstream defaults. Only configs that explicitly request
allowInsecure bypass certificate verification. Such connections remain TLS
encrypted but do not authenticate the peer certificate.
"""
from pathlib import Path
import sys


def replace(path: str, before: str, after: str) -> None:
    target = Path(path)
    text = target.read_text()
    if text.count(before) != 1:
        raise SystemExit(f"unexpected Xray source in {path}: patch refused")
    target.write_text(text.replace(before, after))


if len(sys.argv) != 2:
    raise SystemExit("usage: patch-xray.py /path/to/Xray-core")

root = Path(sys.argv[1])
replace(
    str(root / "infra/conf/transport_internet.go"),
    '''\tif c.AllowInsecure {
\t\treturn nil, errors.PrintRemovedFeatureError(`"allowInsecure"`, `"pinnedPeerCertSha256"(pcs) and "verifyPeerCertByName"(vcn)`)
\t}''',
    '''\tif c.AllowInsecure {
\t\tif c.PinnedPeerCertSha256 != "" || c.VerifyPeerCertByName != "" {
\t\t\treturn nil, errors.New("allowInsecure cannot be combined with certificate pinning")
\t\t}
\t\t// Encode the opt-in compatibility flag in an existing protobuf field.
\t\tconfig.VerifyPeerCertByName = []string{"__commonvpn_allow_insecure_v1__"}
\t}''',
)
replace(
    str(root / "transport/internet/tls/config.go"),
    '''\tif len(c.VerifyPeerCertByName) > 0 {
\t\tconfig.InsecureSkipVerify = true
\t} else {
\t\trandCarrier.VerifyPeerCertByName = nil
\t}''',
    '''\tif len(c.VerifyPeerCertByName) == 1 && c.VerifyPeerCertByName[0] == "__commonvpn_allow_insecure_v1__" {
\t\t// Explicit allowInsecure compatibility for Common VPN subscriptions.
\t\tconfig.InsecureSkipVerify = true
\t\trandCarrier.VerifyPeerCertByName = nil
\t} else if len(c.VerifyPeerCertByName) > 0 {
\t\tconfig.InsecureSkipVerify = true
\t} else {
\t\trandCarrier.VerifyPeerCertByName = nil
\t}''',
)
replace(
    str(root / "main/version.go"),
    '''\tfor _, s := range version {
\t\tfmt.Println(s)
\t}''',
    '''\tfor _, s := range version {
\t\tfmt.Println(s)
\t}
\tfmt.Println("CommonVPN allowInsecure patch 1")''',
)
print("Common VPN Xray compatibility patch applied")
