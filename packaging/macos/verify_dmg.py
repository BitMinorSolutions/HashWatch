"""Check a DMG built by make_dmg.py, on Linux: UDIF trailer + UDZO chunks, HFS+ contents and
modes, Applications symlink, volume icon flag, .DS_Store layout + background alias, app signature.
Usage: python3 verify_dmg.py X.dmg   (env DMG=libdmg-hfsplus dmg, SEVENZ=7z; needs ds_store + mac_alias)
"""
import collections, os, plistlib, stat, struct, subprocess, sys, tempfile
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hfsinfo
from ds_store import DSStore
from mac_alias import Alias

ok = True
def check(c, m):
    global ok
    print(("OK   " if c else "FAIL ") + m); ok &= bool(c)

path = sys.argv[1]
b = open(path, "rb").read()
k = b[-512:]
check(k[:4] == b"koly" and struct.unpack(">I", k[4:8])[0] == 4, "UDIF trailer (koly v4)")
xo, xl = struct.unpack(">QQ", k[0xD8:0xE8])
pl = plistlib.loads(b[xo:xo + xl])
types, parts = collections.Counter(), []
for blk in pl["resource-fork"]["blkx"]:
    parts.append(blk["Name"])
    d = blk["Data"]
    for i in range(struct.unpack(">I", d[200:204])[0]):
        types[struct.unpack(">I", d[204 + 40 * i:208 + 40 * i])[0]] += 1
check(types[0x80000005] > 0 and not (set(types) - {0x80000005, 0, 1, 2, 0x7FFFFFFE, 0xFFFFFFFF}),
      f"UDZO: zlib chunks only ({types[0x80000005]} zlib, other types {sorted(hex(t) for t in types if t != 0x80000005)})")
check(any("Apple_HFS" in p for p in parts), "partition map has an HFS+ partition: " + ", ".join(parts))

with tempfile.TemporaryDirectory() as t:
    raw = os.path.join(t, "raw.img")
    subprocess.run([os.environ.get("DMG", "dmg"), "extract", path, raw], check=True, stdout=subprocess.DEVNULL)
    vol, ents = hfsinfo.list_image(raw)
    by = {e["path"]: e for e in ents}
    apps = [p for p in by if p.count("/") == 1 and p.endswith(".app")]
    check(len(apps) == 1, f"one app at the root: {apps}")
    app = apps[0]
    exe = by.get(app + "/Contents/MacOS/HashWatch")
    check(exe and stat.S_ISREG(exe["mode"]) and exe["mode"] & 0o111 == 0o111, f"{app}/Contents/MacOS/HashWatch is -rwxr-xr-x ({stat.filemode(exe['mode']) if exe else 'missing'})")
    for f in ("/Contents/Info.plist", "/Contents/_CodeSignature/CodeResources", "/Contents/Resources/HashWatch.icns"):
        e = by.get(app + f); check(e and e["mode"] & 0o444 == 0o444 and not e["mode"] & 0o111, f"{app}{f} is -rw-r--r--")
    a = by.get("/Applications")
    check(a and stat.S_ISLNK(a["mode"]) and a["link"] == "/Applications", "Applications -> /Applications symlink")
    check(by["/"]["flags"] & 0x0400, "volume root has kHasCustomIcon (uses .VolumeIcon.icns)")
    check("/.VolumeIcon.icns" in by and "/.background/background.tiff" in by and "/.DS_Store" in by, ".VolumeIcon.icns, .background/background.tiff, .DS_Store present")
    out = os.path.join(t, "x")
    subprocess.run([os.environ.get("SEVENZ", "7z"), "x", "-snl", "-o" + out, path], check=True, stdout=subprocess.DEVNULL)
    root = os.path.join(out, os.listdir(out)[0])
    print("     7z sees volume folder:", os.path.basename(root))
    with DSStore.open(os.path.join(root, ".DS_Store"), "r") as d:
        icvp = d["."]["icvp"]; bwsp = d["."]["bwsp"]
        check(d[app[1:]]["Iloc"] == (170, 170) and d["Applications"]["Iloc"] == (490, 170), "icon positions: app (170,170), Applications (490,170)")
        for f in ("Read me first.txt", "TESTING.md"):
            if "/" + f in by:
                print(f"     {f} at {d[f]['Iloc']}")
        check(icvp["iconSize"] == 112 and icvp["backgroundType"] == 2, "icon view, 112 px icons, picture background")
        print("     window:", bwsp["WindowBounds"], "toolbar", bwsp["ShowToolbar"], "sidebar", bwsp["ShowSidebar"])
        al = Alias.from_bytes(icvp["backgroundImageAlias"])
        bg, bgd = by["/.background/background.tiff"], by["/.background"]
        check(al.target.cnid == bg["cnid"] and al.target.folder_cnid == bgd["cnid"] and al.target.posix_path == "/.background/background.tiff",
              f"background alias -> cnid {al.target.cnid} (file is {bg['cnid']}), folder {al.target.folder_cnid}, volume '{al.volume.name}'")
    r = subprocess.run([sys.executable, os.path.join(os.path.dirname(__file__), "verify_sig.py"), os.path.join(root, app[1:])], capture_output=True, text=True)
    check(r.returncode == 0, "code signature of the app inside the DMG: " + r.stdout.strip().splitlines()[-1])
print("ALL OK" if ok else "DMG CHECK FAILED"); sys.exit(0 if ok else 1)
