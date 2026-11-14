# Block-celebration coin sprites

`web/static/icons/btc-coin-sprites.webp` (6x4 sheet, 24 frames x 128px) and `btc-coin.webp` (512px still, used for the
celebration card and banner icon, and for reduced motion) are rendered from the same three.js scene as the Camaro wallpaper video
(`/workspace/btc-mac/proj/main9.js` -> `bitcoin-greencar-mac-v5.mp4`): the official bitcoin.org "B" extruded with a
10-segment bevel, `MeshPhysicalMaterial` gold `#F0BC4C` (metalness 1, roughness 0.17, clearcoat 0.4), the same key and
rim lights, and the `studio_small_09.hdr` studio environment at the same rotation, ACES tone mapping, then graded like
the video (contrast 1.06, saturation 1.12). Frames step through one end-over-end flip.

Not part of the build. To regenerate (needs three@0.170, esbuild, playwright, Pillow, and the HDR from the wallpaper project):

    npx esbuild sprite.js --bundle --format=iife --outfile=sprite.bundle.js
    cp /workspace/btc-mac/proj/hdr/studio_small_09.hdr . && python3 -m http.server 8766 --bind 127.0.0.1 &
    node render.mjs 24 frames && mkdir -p out && python3 pack.py 128
