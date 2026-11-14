"""Render the Hashwatch logo as a macOS-style app icon (1024 grid) and pack Hashwatch.icns.

Same artwork as web/static/icons (hexagon + pulse) redrawn as vectors at 4x and
downsampled, on Apple's icon grid (824 px tile with transparent margin + shadow).
Usage: python3 make_icon.py OUT.icns [preview.png]   (needs Pillow)
"""
import io, math, struct, sys
from PIL import Image, ImageDraw, ImageFilter

SS = 4                      # supersampling
N = 1024 * SS
TILE, OFF, RAD = 824 * SS, 100 * SS, 185 * SS
ORANGE, CREAM = (247, 147, 26, 255), (255, 236, 210, 255)

def lerp(a, b, t): return tuple(int(a[i] + (b[i] - a[i]) * t) for i in range(len(a)))

def tile():
    # vertical gradient + warm radial glow, clipped to the rounded square
    bg = Image.new("RGBA", (TILE, TILE))
    top, bot = (25, 27, 33, 255), (13, 14, 17, 255)
    g = Image.new("RGBA", (1, 256))
    for y in range(256): g.putpixel((0, y), lerp(top, bot, y / 255))
    bg = g.resize((TILE, TILE))
    glow = Image.new("RGBA", (TILE, TILE), (0, 0, 0, 0))
    d = ImageDraw.Draw(glow)
    r = int(TILE * 0.30)
    c = TILE // 2
    d.ellipse([c - r, c - r, c + r, c + r], fill=(150, 85, 20, 120))
    glow = glow.filter(ImageFilter.GaussianBlur(TILE * 0.09))
    bg = Image.alpha_composite(bg, glow)
    # logo, in 512-icon units scaled to the tile
    s = TILE / 512
    lg = ImageDraw.Draw(bg)
    R, w = 150 * s, int(22 * s)
    pts = [(c + R * math.cos(math.radians(a)), c + R * math.sin(math.radians(a))) for a in range(-90, 270, 60)]
    lg.line(pts + [pts[0], pts[1]], fill=ORANGE, width=w, joint="curve")
    for p in pts: lg.ellipse([p[0] - w / 2, p[1] - w / 2, p[0] + w / 2, p[1] + w / 2], fill=ORANGE)
    pulse = [(129, 256), (208, 256), (232, 200), (262, 318), (289, 234), (304, 256), (382, 256)]
    pulse = [(c + (x - 256) * s, c + (y - 256) * s) for x, y in pulse]
    pw = int(20 * s)
    lg.line(pulse, fill=CREAM, width=pw, joint="curve")
    for p in pulse: lg.ellipse([p[0] - pw / 2, p[1] - pw / 2, p[0] + pw / 2, p[1] + pw / 2], fill=CREAM)
    # subtle top highlight edge
    mask = Image.new("L", (TILE, TILE), 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, TILE - 1, TILE - 1], RAD, fill=255)
    rim = Image.new("RGBA", (TILE, TILE), (0, 0, 0, 0))
    ImageDraw.Draw(rim).rounded_rectangle([0, 0, TILE - 1, TILE - 1], RAD, outline=(255, 255, 255, 28), width=2 * SS)
    bg = Image.alpha_composite(bg, rim)
    bg.putalpha(mask)
    return bg

def icon():
    out = Image.new("RGBA", (N, N), (0, 0, 0, 0))
    sh = Image.new("RGBA", (N, N), (0, 0, 0, 0))
    ImageDraw.Draw(sh).rounded_rectangle([OFF, OFF + 12 * SS, OFF + TILE, OFF + TILE + 12 * SS], RAD, fill=(0, 0, 0, 110))
    sh = sh.filter(ImageFilter.GaussianBlur(14 * SS))
    out = Image.alpha_composite(out, sh)
    out.alpha_composite(tile(), (OFF, OFF))
    return out.resize((1024, 1024), Image.LANCZOS)

def png(im, size):
    b = io.BytesIO(); im.resize((size, size), Image.LANCZOS).save(b, "PNG", optimize=True); return b.getvalue()

def icns(im):
    # PNG-backed entries (macOS 10.7+): icp4/icp5 16/32, ic07..ic10 128..1024, ic11..ic14 @2x
    entries = [("icp4", 16), ("icp5", 32), ("icp6", 64), ("ic07", 128), ("ic08", 256), ("ic09", 512), ("ic10", 1024),
               ("ic11", 32), ("ic12", 64), ("ic13", 256), ("ic14", 512)]
    body = b"".join(t.encode() + struct.pack(">I", 8 + len(d)) + d for t, d in ((t, png(im, s)) for t, s in entries))
    return b"icns" + struct.pack(">I", 8 + len(body)) + body

if __name__ == "__main__":
    im = icon()
    open(sys.argv[1], "wb").write(icns(im))
    if len(sys.argv) > 2: im.save(sys.argv[2])
