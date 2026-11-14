// Renders the Camaro-video gold Bitcoin glyph (same geometry, material, lights, HDR studio env and env rotation as
// /workspace/btc-mac/proj/main9.js, which made bitcoin-greencar-mac-v5.mp4) at fixed tumble angles for confetti sprites.
import * as THREE from 'three';
import { SVGLoader } from 'three/examples/jsm/loaders/SVGLoader.js';
import { RGBELoader } from 'three/examples/jsm/loaders/RGBELoader.js';
import { toCreasedNormals, mergeVertices } from 'three/examples/jsm/utils/BufferGeometryUtils.js';
const Q = new URLSearchParams(location.search), S = +(Q.get("px") || 512), GH = S * +(Q.get('gk') || 0.78);
const D = 'M46.103,27.444c0.637-4.258-2.605-6.547-7.038-8.074l1.438-5.768l-3.511-0.875l-1.4,5.616c-0.923-0.23-1.871-0.447-2.813-0.662l1.41-5.653l-3.509-0.875l-1.439,5.766c-0.764-0.174-1.514-0.346-2.242-0.527l0.004-0.018l-4.842-1.209l-0.934,3.75c0,0,2.605,0.597,2.55,0.634c1.422,0.355,1.679,1.296,1.636,2.042l-1.638,6.571l-2.302,9.23c-0.174,0.432-0.615,1.08-1.609,0.834c0.035,0.051-2.552-0.637-2.552-0.637l-1.743,4.019l4.569,1.139c0.85,0.213,1.683,0.436,2.503,0.646l-1.453,5.834l3.507,0.875l1.439-5.772c0.958,0.26,1.888,0.5,2.798,0.726l-1.434,5.745l3.511,0.875l1.453-5.823c5.987,1.133,10.489,0.676,12.384-4.739c1.527-4.36-0.076-6.875-3.226-8.515C44.236,32.013,45.92,30.763,46.103,27.444z M38.08,38.693c-1.085,4.36-8.426,2.003-10.806,1.412l1.928-7.729C31.582,32.97,39.215,34.149,38.08,38.693z M39.166,27.381c-0.99,3.966-7.1,1.951-9.082,1.457l1.748-7.01C33.814,22.322,40.203,23.244,39.166,27.381z';
const svg = new SVGLoader().parse(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><path transform="translate(0,64) scale(1,-1)" d="${D}"/></svg>`);
const shapes = SVGLoader.createShapes(svg.paths[0]);
const R = new THREE.WebGLRenderer({ canvas: document.getElementById('c'), antialias: true, alpha: true, preserveDrawingBuffer: true, premultipliedAlpha: false });
R.setPixelRatio(1); R.setSize(S, S, false); R.setClearColor(0, 0); R.outputColorSpace = THREE.SRGBColorSpace;
R.toneMapping = THREE.ACESFilmicToneMapping; R.toneMappingExposure = 1.0;
const scene = new THREE.Scene();
const FOV = 22, DIST = (S / 2) / Math.tan(FOV * Math.PI / 360);
const cam = new THREE.PerspectiveCamera(FOV, 1, 10, DIST * 3); cam.position.z = DIST;
let geo;
{ const g0 = new THREE.ExtrudeGeometry(shapes, { depth: 1, bevelEnabled: false }); g0.computeBoundingBox();
  const hh = g0.boundingBox.max.y - g0.boundingBox.min.y, depth = 0.20 * hh, bt = 0.045 * hh, bs = 0.022 * hh;
  geo = new THREE.ExtrudeGeometry(shapes, { depth: depth - 2 * bt, bevelEnabled: true, bevelThickness: bt, bevelSize: bs, bevelOffset: 0, bevelSegments: 10, curveSegments: 72 });
  geo.deleteAttribute('uv'); geo.deleteAttribute('normal');
  geo.computeBoundingBox(); const c = new THREE.Vector3(); geo.boundingBox.getCenter(c); geo.translate(-c.x, -c.y, -c.z);
  const s = GH / (geo.boundingBox.max.y - geo.boundingBox.min.y); geo.scale(s, s, s);
  geo = toCreasedNormals(mergeVertices(geo, 1e-4), THREE.MathUtils.degToRad(35));
  const p = geo.attributes.position, n = geo.attributes.normal; geo.computeBoundingBox(); const zm = geo.boundingBox.max.z;
  for (let i = 0; i < p.count; i += 3) { const z0 = p.getZ(i), z1 = p.getZ(i + 1), z2 = p.getZ(i + 2);
    if (Math.abs(Math.abs(z0) - zm) < 1e-3 && Math.abs(z1 - z0) < 1e-3 && Math.abs(z2 - z0) < 1e-3) for (let k = 0; k < 3; k++) n.setXYZ(i + k, 0, 0, Math.sign(z0)); }
  n.needsUpdate = true; }
const gold = new THREE.MeshPhysicalMaterial({ color: new THREE.Color('#F0BC4C'), metalness: 1.0, roughness: 0.17, clearcoat: 0.4, clearcoatRoughness: 0.08, envMapIntensity: 1.0 });
const mesh = new THREE.Mesh(geo, gold); scene.add(mesh);
const key = new THREE.DirectionalLight(0xffd9a0, 2.2); key.position.set(-0.8, 1.6, 1.4); scene.add(key);
const rim = new THREE.DirectionalLight(0xbfd8ff, 2.6); rim.position.set(1.5, 0.6, -1.0); scene.add(rim);
window.__frame = (flip, turn, roll) => { mesh.rotation.set(flip, turn, roll, 'ZXY'); R.render(scene, cam); return R.domElement.toDataURL('image/png'); };
new RGBELoader().setDataType(THREE.FloatType).load('studio_small_09.hdr', (tex) => {
  const HC = 20, dd = tex.image.data;
  for (let k = 0; k < dd.length; k += 4) { const m = Math.max(dd[k], dd[k + 1], dd[k + 2]); if (m > HC) { const f = (HC + Math.log(m / HC)) / m; dd[k] *= f; dd[k + 1] *= f; dd[k + 2] *= f; } } tex.needsUpdate = true;
  tex.mapping = THREE.EquirectangularReflectionMapping;
  const pm = new THREE.PMREMGenerator(R); scene.environment = pm.fromEquirectangular(tex).texture; tex.dispose();
  scene.environmentRotation = new THREE.Euler(0.3, 4.0, 0); scene.environmentIntensity = 1.0;
  window.__ready = true;
});
