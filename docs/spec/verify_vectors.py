#!/usr/bin/env python3
"""Decide the conformance vectors of certificate-v1.md, independently of
stAirCase's own code: written from the specification alone (section 5, steps
1-4, 6 and 7; person signatures need ssh-keygen and are not in the vectors).

    python3 docs/spec/verify_vectors.py        # needs: pip install cryptography
"""
import base64
import json
import pathlib
import sys

import vectorreport

from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey

PAYLOAD_TYPE = "application/vnd.in-toto+json"
STATEMENT = "https://in-toto.io/Statement/v1"
PREDICATE = "https://github.com/b070nd/stAirCase/blob/master/docs/adr/0002-change-certificate.md#v1"


def pae(payload_type: str, payload: bytes) -> bytes:
    t = payload_type.encode()
    return b"DSSEv1 %d %s %d %s" % (len(t), t, len(payload), payload)


def verify(envelope: dict, public_key: bytes, commit: str, min_cal: int) -> int:
    """Return the accepted level, or raise ValueError with the reason."""
    if envelope.get("payloadType") != PAYLOAD_TYPE:  # step 1
        raise ValueError("payload type")
    payload = base64.b64decode(envelope["payload"], validate=True)  # step 2
    key = Ed25519PublicKey.from_public_bytes(public_key)
    signed = False
    for s in envelope.get("signatures") or []:
        try:
            key.verify(base64.b64decode(s["sig"]), pae(envelope["payloadType"], payload))
            signed = True
            break
        except (InvalidSignature, ValueError):
            continue
    if not signed:
        raise ValueError("signature")
    st = json.loads(payload)  # step 3
    subject = st.get("subject") or []
    if st.get("_type") != STATEMENT or st.get("predicateType") != PREDICATE or len(subject) != 1 \
            or subject[0].get("name") != "commit" or not subject[0].get("digest", {}).get("gitCommit"):
        raise ValueError("not a change certificate")
    if subject[0]["digest"]["gitCommit"] != commit:  # step 4
        raise ValueError("is about commit")
    p = st["predicate"]
    level = p["cal"]  # step 5, without person signatures
    if not isinstance(level, int) or isinstance(level, bool) or not 1 <= level <= 3:  # step 3
        raise ValueError("assurance level")
    if level < min_cal:  # step 6
        raise ValueError("below the required %d" % min_cal)
    for c in p.get("checks") or []:  # step 7
        if c["exitCode"] != 0:
            raise ValueError("failed")
    return level


def main() -> int:
    results = []
    for f in sorted(pathlib.Path(__file__).with_name("vectors").glob("*.json")):
        v = json.loads(f.read_text())
        try:
            got, why = verify(v["envelope"], base64.b64decode(v["publicKey"]), v["commit"], v["minCal"]), ""
        except ValueError as e:
            got, why = None, str(e)
        ok = (got is not None) == v["valid"] and (not v["valid"] or got == v["cal"])
        results.append((f.stem, ok))
        print("%s %s: %s" % ("ok  " if ok else "FAIL", f.stem, "level %d" % got if got is not None else "refused (%s)" % why))
    return vectorreport.finish("verify_vectors", results, 13)


if __name__ == "__main__":
    sys.exit(main())
