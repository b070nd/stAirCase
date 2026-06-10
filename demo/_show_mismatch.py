import json, sys
cp = json.load(open(sys.argv[1]))
for e in cp.get("entries", []):
    if e.get("event_type") == "approval_content_mismatch":
        p = json.loads(e["payload"])
        print("    event: approval_content_mismatch  file=" + p["file"])
        print("    approved_hash=" + p["approved_hash"][:16] + "…  actual_hash=" + p["actual_hash"][:16] + "…")
