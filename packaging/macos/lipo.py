"""Minimal `lipo -create`: join thin Mach-O binaries into one universal (fat) binary."""
import struct, sys

def thin_header(b):
    magic = struct.unpack("<I", b[:4])[0]
    if magic not in (0xFEEDFACF, 0xFEEDFACE):
        sys.exit("not a thin little-endian Mach-O")
    return struct.unpack("<ii", b[4:12])  # cputype, cpusubtype

def create(out, inputs, align=14):
    bins = [open(p, "rb").read() for p in inputs]
    hdr = struct.pack(">II", 0xCAFEBABE, len(bins))
    off, arch, step = 0, [], 1 << align
    off = 8 + 20 * len(bins)
    body = []
    for b in bins:
        ct, cs = thin_header(b)
        off = (off + step - 1) // step * step
        arch.append(struct.pack(">iiIII", ct, cs & 0x00FFFFFF, off, len(b), align))
        body.append((off, b))
        off += len(b)
    with open(out, "wb") as f:
        f.write(hdr + b"".join(arch))
        for o, b in body:
            f.seek(o); f.write(b)

if __name__ == "__main__":
    create(sys.argv[1], sys.argv[2:])
