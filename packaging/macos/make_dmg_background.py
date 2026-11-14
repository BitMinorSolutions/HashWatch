"""Render the DMG window background (660x400 pt) at 1x and 2x and pack both into one
HiDPI TIFF (like `tiffutil -cathidpicheck`: 72 dpi + 144 dpi representations).
Usage: python3 make_dmg_background.py OUT.tiff "App name" [preview-prefix]   (needs Pillow)
Layout must match make_dmg.py: app icon centred at (170,170), Applications at (490,170), docs row at y=420.
"""
import math, struct, sys
from PIL import Image, ImageDraw, ImageFilter, ImageFont

W, H = 660, 520
import re
# The tagline's single source of truth is app.Tagline in internal/app/config.go.
TAGLINE = re.search(r'const Tagline = "([^"]*)"', open(__file__.rsplit("/", 3)[0] + "/internal/app/config.go").read()).group(1)
FONT = __file__.rsplit("/", 3)[0] + "/web/static/vendor/Geist.ttf"

def font(px, wght):
    f = ImageFont.truetype(FONT, px)
    try:
        f.set_variation_by_axes([wght])
    except Exception:
        pass
    return f

def render(scale, name, tagline=TAGLINE):
    w, h = W * scale, H * scale
    s = lambda v: int(round(v * scale))
    g = Image.new("RGB", (1, 256))
    for y in range(256):
        t = y / 255
        g.putpixel((0, y), tuple(int(a + (b - a) * t) for a, b in zip((24, 26, 32), (12, 13, 16))))
    im = g.resize((w, h)).convert("RGBA")
    # warm glow behind the arrow + faint cool glow corners
    glow = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    d = ImageDraw.Draw(glow)
    d.ellipse([s(170), s(45), s(490), s(295)], fill=(170, 95, 20, 70))
    glow = glow.filter(ImageFilter.GaussianBlur(s(70)))
    im = Image.alpha_composite(im, glow)
    # subtle hexagon lattice, fading out toward the bottom
    hexes = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    hd = ImageDraw.Draw(hexes)
    R = 26
    for row in range(-1, 12):
        for col in range(-1, 16):
            cx = col * R * math.sqrt(3) + (row % 2) * R * math.sqrt(3) / 2
            cy = row * R * 1.5
            pts = [(s(cx + R * math.cos(math.radians(a))), s(cy + R * math.sin(math.radians(a)))) for a in range(-90, 270, 60)]
            a = max(0, int(16 * (1 - cy / (H * 0.9))))
            if a:
                hd.line(pts + [pts[0]], fill=(255, 190, 110, a), width=max(1, scale))
    im = Image.alpha_composite(im, hexes)
    # arrow from app to Applications
    ar = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    ad = ImageDraw.Draw(ar)
    y, x0, x1 = 170, 262, 398
    for i in range(10):  # dotted shaft with a gradient
        t = i / 9
        cx = x0 + (x1 - 26 - x0) * t
        r = 2.6
        ad.ellipse([s(cx - r), s(y - r), s(cx + r), s(y + r)], fill=(247, 147, 26, int(70 + 150 * t)))
    head = [(x1, y), (x1 - 18, y - 13), (x1 - 18, y + 13)]
    ad.line([(s(x1 - 17), s(y - 12)), (s(x1), s(y)), (s(x1 - 17), s(y + 12))], fill=(247, 147, 26, 235), width=s(4.2), joint="curve")
    for p in [(x1 - 17, y - 12), (x1, y), (x1 - 17, y + 12)]:
        ad.ellipse([s(p[0] - 2.1), s(p[1] - 2.1), s(p[0] + 2.1), s(p[1] + 2.1)], fill=(247, 147, 26, 235))
    shadow = ar.filter(ImageFilter.GaussianBlur(s(6)))
    im = Image.alpha_composite(im, shadow)
    im = Image.alpha_composite(im, ar)
    # text
    td = ImageDraw.Draw(im)
    t1 = f"Drag {name} to Applications"
    f1, f2 = font(s(17), 560), font(s(12.5), 420)
    tw = td.textlength(t1, font=f1)
    td.text(((w - tw) / 2, s(262)), t1, font=f1, fill=(238, 232, 224, 235))
    t2 = "Then open it from Applications."
    tw = td.textlength(t2, font=f2)
    td.text(((w - tw) / 2, s(289)), t2, font=f2, fill=(238, 232, 224, 120))
    # thin divider + docs row label, then the tagline at the very bottom
    td.line([(s(250), s(330)), (s(410), s(330))], fill=(238, 232, 224, 28), width=max(1, scale))
    f3 = font(s(11), 420)
    t3 = "Before you start"
    tw = td.textlength(t3, font=f3)
    td.text(((w - tw) / 2, s(342)), t3, font=f3, fill=(238, 232, 224, 95))
    f4 = font(s(10.5), 450)
    tw = td.textlength(tagline, font=f4)
    td.text(((w - tw) / 2, s(500)), tagline, font=f4, fill=(247, 170, 80, 120))
    return im.convert("RGB")

def hidpi_tiff(out, im1, im2):
    """Two-page TIFF; page 1 = 1x @72 dpi, page 2 = 2x @144 dpi (Finder picks by resolution)."""
    im1.save(out, format="TIFF", compression="tiff_lzw", save_all=True, append_images=[im2], dpi=(72, 72))
    b = bytearray(open(out, "rb").read())
    le = b[:2] == b"II"
    E = "<" if le else ">"
    off = struct.unpack(E + "I", b[4:8])[0]
    page = 0
    while off:
        n = struct.unpack(E + "H", b[off:off + 2])[0]
        for i in range(n):
            e = off + 2 + 12 * i
            tag, typ, cnt, val = struct.unpack(E + "HHII", b[e:e + 12])
            if tag in (282, 283) and typ == 5:  # X/YResolution RATIONAL
                dpi = 72 if page == 0 else 144
                b[val:val + 8] = struct.pack(E + "II", dpi, 1)
        off = struct.unpack(E + "I", b[off + 2 + 12 * n:off + 6 + 12 * n])[0]
        page += 1
    open(out, "wb").write(bytes(b))

if __name__ == "__main__":
    out, name = sys.argv[1], sys.argv[2]
    im1, im2 = render(1, name), render(2, name)
    hidpi_tiff(out, im1, im2)
    if len(sys.argv) > 3:
        im1.save(sys.argv[3] + "@1x.png"); im2.save(sys.argv[3] + "@2x.png")
