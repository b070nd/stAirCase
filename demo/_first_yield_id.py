import json, sys
try:
    y = json.load(sys.stdin)
    print(y[0]["id"] if y else "")
except Exception:
    print("")
