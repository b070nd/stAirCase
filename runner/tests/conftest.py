"""Expose the modules embedded in the Go binary under the names the runtime
imports them by (staircase_runner.*), so tests exercise exactly what ships."""
import importlib.util
import sys
import types
from pathlib import Path

RUNTIME_DIR = Path(__file__).resolve().parents[2] / "src" / "internal" / "runtime"

_pkg = types.ModuleType("staircase_runner")
_pkg.__path__ = []
sys.modules["staircase_runner"] = _pkg
for _name in ("recording", "ipc"):
    _spec = importlib.util.spec_from_file_location(
        f"staircase_runner.{_name}", RUNTIME_DIR / f"{_name}_embed.py")
    _mod = importlib.util.module_from_spec(_spec)
    sys.modules[_spec.name] = _mod
    _spec.loader.exec_module(_mod)
    setattr(_pkg, _name, _mod)
