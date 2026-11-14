"""Independent check of an ad-hoc signed .app on Linux (rcodesign's `verify` can't
handle ad-hoc/CMS-less signatures): for every Mach-O slice, recompute each page
hash in the CodeDirectory, the Info.plist (slot -1) and CodeResources (slot -3)
digests, and check every file hash listed in _CodeSignature/CodeResources."""
import hashlib, plistlib, struct, sys, os, base64

app = sys.argv[1]
exe = os.path.join(app, "Contents/MacOS", plistlib.load(open(os.path.join(app, "Contents/Info.plist"), "rb"))["CFBundleExecutable"])
data = open(exe, "rb").read()
slices = []
if struct.unpack(">I", data[:4])[0] == 0xCAFEBABE:
    for i in range(struct.unpack(">I", data[4:8])[0]):
        ct, cs, off, size, al = struct.unpack(">iiIII", data[8 + 20 * i:28 + 20 * i])
        slices.append((ct, data[off:off + size]))
else:
    slices.append((struct.unpack("<i", data[4:8])[0], data))
ok = True
def check(cond, msg):
    global ok
    print(("OK   " if cond else "FAIL ") + msg); ok &= cond
sha = lambda b: hashlib.sha256(b).digest()
info = sha(open(os.path.join(app, "Contents/Info.plist"), "rb").read())
res = sha(open(os.path.join(app, "Contents/_CodeSignature/CodeResources"), "rb").read())
for ct, b in slices:
    arch = {0x0100000C: "arm64", 0x01000007: "x86_64"}.get(ct, hex(ct))
    ncmds = struct.unpack("<I", b[16:20])[0]; p = 32; sig = None
    for _ in range(ncmds):
        cmd, sz = struct.unpack("<II", b[p:p + 8])
        if cmd == 0x1D: sig = struct.unpack("<II", b[p + 8:p + 16])
        p += sz
    check(sig is not None, f"{arch}: has LC_CODE_SIGNATURE")
    sb = b[sig[0]:sig[0] + sig[1]]
    magic, length, count = struct.unpack(">III", sb[:12])
    check(magic == 0xFADE0CC0, f"{arch}: embedded signature superblob")
    for i in range(count):
        typ, off = struct.unpack(">II", sb[12 + 8 * i:20 + 8 * i])
        if typ != 0: continue
        cd = sb[off:]
        (m, ln, ver, flags, hoff, ioff, nspec, ncode, limit, hsize, htype, plat, pgl) = struct.unpack(">IIIIIIIIIBBBB", cd[:40])
        ident = cd[ioff:cd.index(b"\0", ioff)].decode()
        check(m == 0xFADE0C02 and htype == 2, f"{arch}: CodeDirectory v{ver:#x}, sha256, identifier={ident}, flags={flags:#x} (adhoc={bool(flags & 2)})")
        pg = 1 << pgl
        bad = sum(cd[hoff + k * hsize:hoff + (k + 1) * hsize] != sha(b[k * pg:min((k + 1) * pg, limit)]) for k in range(ncode))
        check(bad == 0 and ncode == (limit + pg - 1) // pg, f"{arch}: all {ncode} page hashes match (code limit {limit})")
        slot = lambda n: cd[hoff - n * hsize:hoff - (n - 1) * hsize]
        check(nspec >= 3 and slot(1) == info, f"{arch}: Info.plist bound (slot -1)")
        check(nspec >= 3 and slot(3) == res, f"{arch}: CodeResources bound (slot -3)")
cr = plistlib.load(open(os.path.join(app, "Contents/_CodeSignature/CodeResources"), "rb"))
for f, v in cr["files2"].items():
    h = v["hash2"] if isinstance(v, dict) else v
    check(sha(open(os.path.join(app, "Contents", f), "rb").read()) == h, f"resource {f} hash matches")
print("ALL OK" if ok else "SIGNATURE CHECK FAILED"); sys.exit(0 if ok else 1)
