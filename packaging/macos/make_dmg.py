"""Build a drag-to-Applications DMG on Linux (or macOS) without mounting anything.

  staging dir -> mkfs.hfsplus image -> libdmg-hfsplus `hfsplus addall` -> root custom-icon flag
  -> .DS_Store (window, icon positions, background alias pointing at the real CNIDs)
  -> `dmg build` (UDZO, zlib).

Usage: python3 make_dmg.py --app X.app --volname NAME --background bg.tiff --icon vol.icns --out X.dmg [--extra FILE ...]
Tools (env): MKFS_HFSPLUS, HFSPLUS, DMG  (needs Python packages ds_store + mac_alias)
"""
import argparse, datetime, os, shutil, subprocess, sys, tempfile
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hfsinfo
from ds_store import DSStore
from mac_alias import Alias, VolumeInfo, TargetInfo, ALIAS_KIND_FILE, ALIAS_FIXED_DISK

WIN_W, WIN_H = 660, 520       # content area, matches the background
APP_XY, APPS_XY = (170, 170), (490, 170)
EXTRA_Y = 420                 # optional docs row (Read me first.txt, TESTING.md)

def run(*a):
    subprocess.run(a, check=True, stdout=subprocess.DEVNULL)

def main():
    ap = argparse.ArgumentParser()
    for k in ("app", "volname", "background", "icon", "out"):
        ap.add_argument("--" + k, required=True)
    ap.add_argument("--extra", action="append", default=[], help="file to show in the bottom row (repeatable)")
    a = ap.parse_args()
    mkfs = os.environ.get("MKFS_HFSPLUS", "mkfs.hfsplus")
    hfs = os.environ.get("HFSPLUS", "hfsplus")
    dmg = os.environ.get("DMG", "dmg")
    work = tempfile.mkdtemp()
    try:
        stage = os.path.join(work, "stage")
        os.makedirs(os.path.join(stage, ".background"))
        app_name = os.path.basename(a.app.rstrip("/"))
        shutil.copytree(a.app, os.path.join(stage, app_name), symlinks=True)
        os.symlink("/Applications", os.path.join(stage, "Applications"))
        shutil.copy(a.background, os.path.join(stage, ".background", "background.tiff"))
        shutil.copy(a.icon, os.path.join(stage, ".VolumeIcon.icns"))
        for x in a.extra:
            shutil.copy(x, os.path.join(stage, os.path.basename(x)))
            os.chmod(os.path.join(stage, os.path.basename(x)), 0o644)
        for f in (".background/background.tiff", ".VolumeIcon.icns"):
            os.chmod(os.path.join(stage, f), 0o644)
        os.chmod(os.path.join(stage, ".background"), 0o755)

        used = sum(os.lstat(os.path.join(dp, f)).st_size for dp, _, fs in os.walk(stage) for f in fs)
        img = os.path.join(work, "image.hfs")
        with open(img, "wb") as f:
            f.truncate((used * 12 // 10 + 8 * 1024 * 1024) // 4096 * 4096)
        run(mkfs, "-v", a.volname, img)
        run(hfs, img, "addall", stage, "--symlinks", "clone_link")
        run(hfs, img, "attr", "/", "C")                   # root folder: kHasCustomIcon -> .VolumeIcon.icns
        run(hfs, img, "attr", "/.background", "V")        # invisible (belt and braces; it's a dot folder)
        run(hfs, img, "attr", "/.VolumeIcon.icns", "V")

        vol, entries = hfsinfo.list_image(img)
        by = {e["path"]: e for e in entries}
        bgdir, bg = by["/.background"], by["/.background/background.tiff"]
        mac = lambda raw: hfsinfo.MAC_EPOCH + datetime.timedelta(seconds=raw)
        alias = Alias(version=2,
            volume=VolumeInfo(a.volname, vol["create"], b"H+", ALIAS_FIXED_DISK, 0, b"\0\0", posix_path="/Volumes/" + a.volname),
            target=TargetInfo(ALIAS_KIND_FILE, "background.tiff", bgdir["cnid"], bg["cnid"], mac(bg["create"]),
                              b"\0\0\0\0", b"\0\0\0\0", folder_name=".background", cnid_path=[bgdir["cnid"]],
                              carbon_path=f"{a.volname}:.background:\0background.tiff", posix_path="/.background/background.tiff"))
        ds = os.path.join(work, "DS_Store")
        with DSStore.open(ds, "w+") as d:
            d["."]["vSrn"] = ("long", 1)
            d["."]["bwsp"] = {"ShowStatusBar": False, "ShowToolbar": False, "ShowPathbar": False, "ShowSidebar": False,
                               "ContainerShowSidebar": False, "ShowTabView": False, "PreviewPaneVisibility": False,
                               "SidebarWidth": 0, "WindowBounds": "{{200, 160}, {%d, %d}}" % (WIN_W, WIN_H + 28)}
            d["."]["icvp"] = {"viewOptionsVersion": 1, "backgroundType": 2, "backgroundImageAlias": alias.to_bytes(),
                               "backgroundColorRed": 0.06, "backgroundColorGreen": 0.06, "backgroundColorBlue": 0.07,
                               "gridOffsetX": 0.0, "gridOffsetY": 0.0, "gridSpacing": 100.0, "arrangeBy": "none",
                               "showIconPreview": True, "showItemInfo": False, "labelOnBottom": True,
                               "textSize": 13.0, "iconSize": 112.0, "scrollPositionX": 0.0, "scrollPositionY": 0.0}
            d["."]["icvl"] = ("type", b"icnv")
            d[app_name]["Iloc"] = APP_XY
            d["Applications"]["Iloc"] = APPS_XY
            n = len(a.extra)
            for i, x in enumerate(a.extra):
                d[os.path.basename(x)]["Iloc"] = (int(WIN_W / 2 + (i - (n - 1) / 2) * 170), EXTRA_Y)
        run(hfs, img, "add", ds, "/.DS_Store")
        run(hfs, img, "attr", "/.DS_Store", "V")
        if os.path.exists(a.out):
            os.remove(a.out)
        run(dmg, "-c", "zlib", "-l", "9", "build", img, a.out)  # UDZO
    finally:
        shutil.rmtree(work)

if __name__ == "__main__":
    main()
