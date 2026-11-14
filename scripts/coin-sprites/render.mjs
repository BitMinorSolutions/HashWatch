import { chromium } from 'playwright'; import fs from 'fs';
const N = +(process.argv[2] || 24), OUT = process.argv[3] || 'frames'; fs.mkdirSync(OUT, { recursive: true });
const br = await chromium.launch({ executablePath: '/usr/bin/google-chrome', args: ['--use-angle=swiftshader', '--enable-unsafe-swiftshader', '--ignore-gpu-blocklist'] });
const p = await br.newPage(); p.on('pageerror', e => console.log('pageerror', e.message)); p.on('console', m => console.log('console', m.text()));
await p.goto('http://127.0.0.1:8766/index.html?px=512'); await p.waitForFunction(() => window.__ready, null, { timeout: 180000 });
for (let i = 0; i < N; i++) {
  const a = 2 * Math.PI * i / N;                       // one full end-over-end flip per cycle (like the video's tumble)
  const turn = 0.5 + 0.45 * Math.sin(a + 0.6);        // held at an oblique yaw like the video (its glyph mostly shows ~0.5-1.1 rad of turn) so faces pick up the bronze-to-gold falloff, plus wander -> glints move
  const url = await p.evaluate(([f, t]) => window.__frame(f, t, 0), [a, turn]);
  fs.writeFileSync(`${OUT}/${String(i).padStart(2, '0')}.png`, Buffer.from(url.split(',')[1], 'base64'));
}
// hero (front-facing, slight angle like the wallpaper still)
fs.writeFileSync(`${OUT}/hero.png`, Buffer.from((await p.evaluate(() => window.__frame(0.12, 0.55, 0))).split(',')[1], 'base64'));
await br.close(); console.log('ok');
