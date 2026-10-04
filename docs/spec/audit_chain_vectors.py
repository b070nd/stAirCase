#!/usr/bin/env python3
"""Recompute the audit chain hashes of the conformance vectors in
audit-chain-vectors.json from the rules in docs/audit.md ("The hash chain")
alone, independently of stAirCase's own code. Standard library only.

    python3 docs/spec/audit_chain_vectors.py
"""
import hashlib
import json
import pathlib
import struct
import sys


def v1(entry, prev):
    return hashlib.sha256((entry["payload"] + prev + entry["git_commit_hash"]).encode()).hexdigest()


def v2(entry, prev):
    h = hashlib.sha256(b"staircase-chain-v2\n")
    for field in (entry["event_type"], entry["payload"], prev, entry["git_commit_hash"]):
        raw = field.encode()
        h.update(struct.pack(">Q", len(raw)) + raw)
    return h.hexdigest()


def chain_ok(entries):
    prev = ""
    for e in entries:
        version = e.get("hash_version", 1) or 1
        if version not in (1, 2):
            return False
        if (v1 if version == 1 else v2)(e, prev) != e["event_hash"]:
            return False
        prev = e["event_hash"]
    return True


def main():
    path = pathlib.Path(__file__).with_name("audit-chain-vectors.json")
    failures = 0
    for name, vec in json.loads(path.read_text()).items():
        ok = chain_ok(vec["entries"]) == vec["valid"]
        failures += not ok
        print("%s %s" % ("ok  " if ok else "FAIL", name))
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
