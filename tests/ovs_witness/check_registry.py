#!/usr/bin/env python3
"""Keep C/Go registry metadata tied to the actual pinned upstream schema."""
import json
import hashlib
from pathlib import Path
import re
import sys
import os
version = os.environ["OVS_WITNESS_VERSION"]
assert version in ["3.3.9", "3.7.1", "4.0.0"]
extension = Path("extensions/ovs-witness") / version
profile_path = Path("internal/ovswitness") / ("build-profile.json" if version == "3.3.9" else "build-profile-" + version + ".json")

schema = json.loads((Path(sys.argv[1]) / "vswitchd/vswitch.ovsschema").read_text())
columns = json.loads((extension / "input-columns.json").read_text())
types = json.loads(Path("internal/ovswitness/input-types.json").read_text())
c = (extension / "overlay/vswitchd/ovs-webui-input-columns.inc").read_text()
base = lambda v: v if isinstance(v, str) else v["type"]
for table, suffix in [("Open_vSwitch", "root"), ("Bridge", "bridge"), ("Port", "port"), ("Interface", "iface")]:
    names = columns[table]
    assert names == sorted(names) and set(names) == set(types[table])
    literal = re.search("webui_" + suffix + r"_columns\[\] = \{(.*?)\};", c, re.S)[1]
    assert re.findall(r'"([a-z_]+)"', literal) == names
    for name in names:
        col = schema["tables"][table]["columns"][name]
        assert not col.get("ephemeral")
        t = col["type"]
        expected = {"key": t, "value": "void", "min": 1, "max": 1} if isinstance(t, str) else {
            "key": base(t["key"]), "value": base(t["value"]) if "value" in t else "void", "min": t.get("min", 1), "max": t.get("max", 1)}
        assert types[table][name] == expected, (table, name)
        assert expected["key"] != "real" and expected["value"] != "real"
print("Pinned C/Go input registry: 52 fields verified")
profile = json.loads(profile_path.read_text())
for name, expected in profile["files"].items():
    assert hashlib.sha256(Path(name).read_bytes()).hexdigest() == expected, name
pin = json.loads((extension / "upstream.json").read_text())
assert profile["upstream_commit"] == pin["commit"] and profile["archive_sha256"] == pin["archive_sha256"] and profile["runtime"] == version
assert profile["configure"] == ["--enable-ssl", "--disable-libcapng", "--disable-shared", "--enable-static", "--enable-ovs-webui-witness"]
assert profile["cflags"] == "-O1 -g -Werror" and profile["libs"] == "-lm"
assert schema == json.loads(Path("internal/provider/ovsdb/testdata/vswitch-" + version + ".ovsschema").read_text())
print("Reviewed build profile and input bytes verified")
