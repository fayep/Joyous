"""Persistent F/L names — the Gerald registry subset a static walk needs."""
from __future__ import annotations

import json
import os
from pathlib import Path

PREFIX = {"func": "F", "label": "L"}
KINDS = ("func", "label")


class Registry:
    def __init__(self, path: Path | str):
        self.path = Path(path).expanduser()
        if not self.path.is_absolute():
            self.path = self.path.resolve()
        self.data = {
            "counters": {k: 0 for k in KINDS},
            "func": {},
            "label": {},
        }
        self._defer = 0
        self._dirty = False
        self._load()

    def _load(self) -> None:
        if not self.path.exists():
            return
        with open(self.path) as f:
            loaded = json.load(f)
        for k in KINDS:
            loaded.setdefault(k, {})
        loaded.setdefault("counters", {})
        for k in KINDS:
            loaded["counters"].setdefault(k, 0)
        self.data = loaded

    def _save(self) -> None:
        if self._defer:
            self._dirty = True
            return
        self.path.parent.mkdir(parents=True, exist_ok=True)
        tmp = self.path.with_name(self.path.name + ".tmp")
        payload = json.dumps(self.data, indent=2, sort_keys=True)
        with open(tmp, "w") as f:
            f.write(payload)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, self.path)

    def deferred(self):
        return _Defer(self)

    def name_func(self, va: int, note: str = "") -> str:
        return self._name("func", va, note)

    def name_label(self, va: int, note: str = "") -> str:
        return self._name("label", va, note)

    def _name(self, kind: str, va: int, note: str) -> str:
        key = hex(va & 0xFFFFFFFF)
        table = self.data[kind]
        if key in table:
            entry = table[key]
        else:
            self.data["counters"][kind] += 1
            n = self.data["counters"][kind]
            entry = {"name": f"{PREFIX[kind]}{n}", "notes": []}
            table[key] = entry
        if note and note not in entry["notes"]:
            entry["notes"].append(note)
        self._save()
        return entry["name"]

    def set_alias(self, va: int, alias: str, source: str = "logtag") -> bool:
        entry = self.data["func"].get(hex(va & 0xFFFFFFFF))
        if not entry:
            return False
        alias = alias.strip()
        if not alias:
            return False
        pri = {"string": 2, "logtag": 1}
        new = pri.get(source, 0)
        old = pri.get(entry.get("alias_source") or "", 0)
        prev = entry.get("alias")
        if prev and new < old:
            return False
        if prev and new == old and prev != alias and source == "logtag":
            return False
        if prev == alias:
            return True
        entry["alias"] = alias
        entry["alias_source"] = source
        note = f"{source}: {alias}"
        if note not in entry["notes"]:
            entry["notes"].append(note)
        self._save()
        return True

    def alias(self, va: int) -> str | None:
        entry = self.data["func"].get(hex(va & 0xFFFFFFFF))
        if not entry:
            return None
        a = entry.get("alias")
        return str(a) if a else None

    def display(self, va: int) -> str:
        key = hex(va & 0xFFFFFFFF)
        entry = self.data["func"].get(key)
        if not entry:
            lab = self.data["label"].get(key)
            if lab:
                return f"{lab['name']}@0x{va:x}"
            return f"0x{va:x}"
        alias = entry.get("alias")
        name = alias or entry["name"]
        return f"{name}@0x{va:x}"

    def func_vas(self) -> list[int]:
        return sorted(int(k, 16) for k in self.data.get("func", {}))

    def note_call(self, caller: int, callee: int) -> None:
        entry = self.data["func"].get(hex(caller & 0xFFFFFFFF))
        if not entry:
            return
        calls = entry.setdefault("calls", [])
        h = hex(callee & 0xFFFFFFFF)
        if h not in calls:
            calls.append(h)
            self._save()


class _Defer:
    def __init__(self, reg: Registry):
        self.reg = reg

    def __enter__(self):
        self.reg._defer += 1
        return self.reg

    def __exit__(self, *exc):
        self.reg._defer -= 1
        if self.reg._defer == 0 and self.reg._dirty:
            self.reg._dirty = False
            self.reg._save()
