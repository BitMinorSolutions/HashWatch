# Pack Camaro-video glyph renders into a confetti sprite sheet (+ hero still), graded like the video (contrast 1.06, saturate 1.12).
from PIL import Image, ImageEnhance, ImageFilter
import sys
CELL=int(sys.argv[1]) if len(sys.argv)>1 else 128; COLS=6; N=24
def grade(im, size):
    im = im.convert('RGBA').resize((size, size), Image.LANCZOS)
    rgb, a = im.convert('RGB'), im.getchannel('A')
    rgb = ImageEnhance.Color(ImageEnhance.Contrast(rgb).enhance(1.06)).enhance(1.12)
    out = rgb.convert('RGBA'); out.putalpha(a); return out
sheet = Image.new('RGBA', (CELL*COLS, CELL*(N//COLS)), (0,0,0,0))
for i in range(N):
    sheet.alpha_composite(grade(Image.open(f'frames/{i:02d}.png'), CELL), ((i%COLS)*CELL, (i//COLS)*CELL))
sheet.save('out/btc-coin-sprites.webp', 'WEBP', quality=86, method=6, alpha_quality=90)
sheet.save('out/btc-coin-sprites.png', optimize=True)
grade(Image.open('frames/hero.png'), 256).save('out/btc-coin.webp', 'WEBP', quality=88, method=6, alpha_quality=92)
