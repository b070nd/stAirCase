"""The summary the three independent conformance scripts share: how many vectors
ran, and a verdict that is never a vacuous pass. A missing or emptied inventory,
or one smaller than the floor recorded in the script, fails; so does any vector
that is decided differently. Not part of the specification: the logic being
checked lives in each script, written from the documents alone.

    python3 docs/spec/<script>.py [--json results.json]
"""
import json
import sys


def finish(script, results, minimum):
    """results: [(name, ok)]; returns the process exit status."""
    failed = [n for n, ok in results if not ok]
    print("%s: %d vector(s) decided, %d differ" % (script, len(results), len(failed)))
    problem = None
    if not results:
        problem = "no vectors were found: an empty inventory proves nothing"
    elif len(results) < minimum:
        problem = "%d vector(s) is fewer than the %d this script requires: lower the floor only if vectors were removed on purpose" % (len(results), minimum)
    if problem:
        print("FAIL " + problem)
    if "--json" in sys.argv:
        path = sys.argv[sys.argv.index("--json") + 1]
        with open(path, "w") as f:
            json.dump({"script": script, "vectors": [{"name": n, "ok": ok} for n, ok in results],
                       "count": len(results), "failed": failed, "minimum": minimum,
                       "ok": not failed and problem is None}, f, indent=2)
            f.write("\n")
    return 1 if failed or problem else 0
