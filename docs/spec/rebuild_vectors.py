#!/usr/bin/env python3
"""Rebuild the git trees of the conformance vectors in rebuild-vectors/, from
the rules in certificate-v1.md ("Rebuilding a commit") alone, independently of
stAirCase's own code. Standard library only.

    python3 docs/spec/rebuild_vectors.py
"""
import base64
import binascii
import hashlib
import json
import pathlib
import posixpath
import sys

import vectorreport

MAX_NEW_FILE = 200 * 1024
MAX_BINARY_FILE = 2 * 1024 * 1024
NEW_FILE, DELETE_FILE = "(new file)", "(delete file)"


class Refused(Exception):
    pass


def clean_path(rel: str) -> str:
    if rel == "" or rel.startswith("/"):
        raise Refused("must be relative to the project root: " + rel)
    clean = posixpath.normpath(rel)
    if clean in (".", "..") or clean.startswith("../"):
        raise Refused("outside the project root: " + rel)
    if any(part.casefold() == ".git" for part in clean.split("/")):
        raise Refused("repository internals: " + rel)
    return clean


def all_crlf(b: bytes) -> bool:
    n = b.count(b"\r\n")
    return n > 0 and b.count(b"\r") == n and b.count(b"\n") == n


def derive(files: dict, proposal: list) -> dict:
    """Apply one proposal's edits to files (path -> (bytes, mode) or None for a
    deleted file); return the changed paths only."""
    nxt = {}

    def current(p):
        return nxt[p] if p in nxt else files.get(p)

    for e in proposal:
        p = clean_path(e["file"])
        search, replace = e.get("search_block", ""), e.get("replace_block", "")
        b64, mode = e.get("content_b64", ""), e.get("mode", "")
        if (b64 or mode) and search != NEW_FILE:
            raise Refused("content_b64 and mode belong to a whole new file: " + p)
        if mode not in ("", "100644", "100755"):
            raise Refused("mode must be 100644 or 100755, not %r: %s" % (mode, p))
        if search == NEW_FILE:
            data = replace.encode()
            if b64:
                if replace != "":
                    raise Refused("a change has either replace_block or content_b64, not both: " + p)
                try:
                    data = base64.b64decode(b64, validate=True)
                except (binascii.Error, ValueError):
                    raise Refused("content_b64 is not base64: " + p)
                try:
                    data.decode("utf-8")
                    raise Refused("the content is valid UTF-8 text: send it as replace_block: " + p)
                except UnicodeDecodeError:
                    pass
                if len(data) > MAX_BINARY_FILE:
                    raise Refused("file is too large: " + p)
            elif len(data) > MAX_NEW_FILE:
                raise Refused("new file is too large: " + p)
            cur = current(p)
            nxt[p] = (data, mode or (cur[1] if cur else "100644"))
        elif search == DELETE_FILE:
            if current(p) is None:
                raise Refused("cannot delete a file that does not exist: " + p)
            nxt[p] = None
        else:
            cur = current(p)
            if cur is None:
                raise Refused("cannot edit a file that does not exist: " + p)
            src = cur[0]
            s = search.replace("\r\n", "\n").encode()
            r = replace.replace("\r\n", "\n").encode()
            if b"\r" not in src:
                pass
            elif all_crlf(src):
                s, r = s.replace(b"\n", b"\r\n"), r.replace(b"\n", b"\r\n")
            else:
                src = src.replace(b"\r\n", b"\n").replace(b"\r", b"\n")
            if s not in src:
                raise Refused("search_block not found in the approved content: " + p)
            nxt[p] = (src.replace(s, r, 1), cur[1])
    return nxt


def git_object(kind: str, body: bytes) -> bytes:
    return hashlib.sha1(b"%s %d\0" % (kind.encode(), len(body)) + body).digest()


def tree_id(files: dict) -> str:
    """The git tree id of a flat path -> (bytes, mode) mapping."""
    def build(prefix: str) -> bytes:
        entries = {}
        for path, (data, mode) in files.items():
            if not path.startswith(prefix):
                continue
            rest = path[len(prefix):]
            head, _, tail = rest.partition("/")
            if tail:
                entries[head] = ("40000", None)
            else:
                entries[head] = (mode, git_object("blob", data))
        body = b""
        for name in sorted(entries, key=lambda n: n + ("/" if entries[n][0] == "40000" else "")):
            mode, oid = entries[name]
            if mode == "40000":
                oid = build(prefix + name + "/")
            body += mode.encode() + b" " + name.encode() + b"\0" + oid
        return git_object("tree", body)
    return build("").hex()


def rebuild(vector: dict) -> str:
    files = {f["path"]: (f["content"].encode(), f["mode"]) for f in vector["base"]}
    for proposal in vector["proposals"]:
        for path, state in derive(files, proposal).items():
            if state is None:
                files.pop(path, None)
            else:
                files[path] = state
    return tree_id(files)


def main() -> int:
    results = []
    for f in sorted(pathlib.Path(__file__).with_name("rebuild-vectors").glob("*.json")):
        v = json.loads(f.read_text())
        try:
            got, why = rebuild(v), ""
        except Refused as e:
            got, why = None, str(e)
        if v.get("error"):
            ok = got is None and v["error"] in why
        else:
            ok = got == v["tree"]
        results.append((f.stem, ok))
        print("%s %s: %s" % ("ok  " if ok else "FAIL", f.stem, got[:12] if got else "refused (%s)" % why))
    return vectorreport.finish("rebuild_vectors", results, 25)


if __name__ == "__main__":
    sys.exit(main())
