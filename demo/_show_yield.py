import json, sys
y = json.load(sys.stdin)
r = y["request"]
e = r["proposed_edits"][0]
print("  agent:        " + r["agent_name"])
print("  action:       " + r["action_type"])
print("  file:         " + e["file"])
print("  confidence:   " + str(r["confidence_score"]))
print("  content_hash: " + e["content_hash"])
print("  reasoning:    " + r["reasoning_trace"])
