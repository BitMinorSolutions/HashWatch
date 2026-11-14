"""Zip a staging folder like `ditto -c -k --keepParent`: Unix permissions, exec bits
and symlinks are kept (create_system=Unix, mode in external_attr), dirs included.
Usage: python3 zipdir.py OUT.zip STAGING_DIR   (entries are relative to STAGING_DIR)
"""
import os, stat, sys, time, zipfile

def add(z, path, arc):
    st = os.lstat(path)
    info = zipfile.ZipInfo(arc + ("/" if stat.S_ISDIR(st.st_mode) else ""), time.localtime(st.st_mtime)[:6])
    info.create_system = 3
    info.external_attr = (st.st_mode & 0xFFFF) << 16
    if stat.S_ISDIR(st.st_mode):
        info.external_attr |= 0x10  # MS-DOS directory flag
        z.writestr(info, b"")
    elif stat.S_ISLNK(st.st_mode):
        z.writestr(info, os.readlink(path))
    else:
        info.compress_type = zipfile.ZIP_DEFLATED
        with open(path, "rb") as f:
            z.writestr(info, f.read(), compresslevel=9)

def main(out, root):
    with zipfile.ZipFile(out, "w") as z:
        for dp, dns, fns in os.walk(root):
            dns.sort()
            rel = os.path.relpath(dp, root)
            if rel != ".":
                add(z, dp, rel)
            for f in sorted(fns):
                p = os.path.join(dp, f)
                add(z, p, os.path.normpath(os.path.join(rel, f)))

if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
