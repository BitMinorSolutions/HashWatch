"""Tiny read-only HFS+ catalog reader (for DMG builds and checks on Linux).
list_image(path) -> (volume, entries); entry = dict(path, cnid, parent, kind, mode, size, flags, link)
CLI: python3 hfsinfo.py image.hfs   (raw HFS+ image, e.g. from `dmg extract x.dmg raw.img`)
"""
import datetime, stat, struct, sys

MAC_EPOCH = datetime.datetime(1904, 1, 1, tzinfo=datetime.timezone.utc)

def _fork(b, off):
    size, clump, blocks = struct.unpack(">QII", b[off:off + 16])
    ext = [struct.unpack(">II", b[off + 16 + 8 * i:off + 24 + 8 * i]) for i in range(8)]
    return size, [e for e in ext if e[1]]

def list_image(path):
    f = open(path, "rb")
    data = f.read()
    base = 0
    if data[1024:1026] not in (b"H+", b"HX"):  # raw disk with a partition map: find the HFS+ header
        i = data.find(b"H+\x00\x04")
        while i != -1 and (i - 1024) % 512:
            i = data.find(b"H+\x00\x04", i + 1)
        if i == -1:
            raise SystemExit("no HFS+ volume found")
        base = i - 1024
    vh = data[base + 1024:base + 1536]
    create = struct.unpack(">I", vh[16:20])[0]
    bs = struct.unpack(">I", vh[40:44])[0]
    vol = dict(signature=vh[:2].decode(), create_raw=create, create=MAC_EPOCH + datetime.timedelta(seconds=create),
               block_size=bs, files=struct.unpack(">I", vh[32:36])[0], folders=struct.unpack(">I", vh[36:40])[0],
               journaled=bool(struct.unpack(">I", vh[4:8])[0] & 0x2000), base=base)
    size, ext = _fork(vh, 272)
    cat = b"".join(data[base + s * bs:base + (s + n) * bs] for s, n in ext)[:size]
    node_size = struct.unpack(">H", cat[32:34])[0]
    first_leaf = struct.unpack(">I", cat[24:28])[0]
    recs, names = [], {}
    node = first_leaf
    while node:
        nd = cat[node * node_size:(node + 1) * node_size]
        flink, blink, kind, height, nrec = struct.unpack(">IIbBH", nd[:12])
        offs = [struct.unpack(">H", nd[node_size - 2 * (i + 1):node_size - 2 * i])[0] for i in range(nrec)]
        for o in offs:
            klen = struct.unpack(">H", nd[o:o + 2])[0]
            parent = struct.unpack(">I", nd[o + 2:o + 6])[0]
            nlen = struct.unpack(">H", nd[o + 6:o + 8])[0]
            name = nd[o + 8:o + 8 + 2 * nlen].decode("utf-16-be")
            r = o + 2 + klen
            r += r & 1
            rt = struct.unpack(">h", nd[r:r + 2])[0]
            if rt in (1, 2):
                cnid = struct.unpack(">I", nd[r + 8:r + 12])[0]
                mode = struct.unpack(">H", nd[r + 42:r + 44])[0]
                flags = struct.unpack(">H", nd[r + 56:r + 58])[0]
                e = dict(name=name, parent=parent, cnid=cnid, kind="dir" if rt == 1 else "file", mode=mode, flags=flags,
                         create=struct.unpack(">I", nd[r + 12:r + 16])[0], size=0, link=None)
                if rt == 2:
                    e["type"], e["creator"] = nd[r + 48:r + 52], nd[r + 52:r + 56]
                    fsize, fext = _fork(nd, r + 88)
                    e["size"] = fsize
                    if stat.S_ISLNK(mode):
                        e["link"] = b"".join(data[base + s * bs:base + (s + n) * bs] for s, n in fext)[:fsize].decode()
                recs.append(e)
                names[cnid] = (parent, name)
        node = flink
    def full(cnid):
        parts = []
        while cnid in names and cnid != 2:
            parent, name = names[cnid]
            parts.append(name)
            cnid = parent
        return "/" + "/".join(reversed(parts))
    for e in recs:
        e["path"] = full(e["cnid"])
    return vol, sorted(recs, key=lambda e: e["path"])

if __name__ == "__main__":
    vol, entries = list_image(sys.argv[1])
    print(f"volume: HFS+ ({vol['signature']}), block {vol['block_size']}, files {vol['files']}, folders {vol['folders']}, journaled {vol['journaled']}")
    for e in entries:
        m = stat.filemode(e["mode"]) if e["mode"] else "----------"
        extra = f" -> {e['link']}" if e["link"] else ""
        fl = f" finderflags={e['flags']:#06x}" if e["flags"] else ""
        print(f"{m} {e['size']:>10} cnid={e['cnid']:<4} {e['path']}{extra}{fl}")
