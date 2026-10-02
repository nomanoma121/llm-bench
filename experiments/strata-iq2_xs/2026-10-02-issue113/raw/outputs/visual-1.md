```html
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Castle Town in Spring</title>
<style>
  html,body{margin:0;height:100%;overflow:hidden;background:#101418;font-family:system-ui,-apple-system,"Segoe UI",sans-serif}
  #app{position:fixed;inset:0}
  #ui{position:fixed;left:16px;top:16px;color:#f4f6f8;max-width:340px;pointer-events:none;text-shadow:0 1px 3px rgba(0,0,0,.45)}
  #ui h1{margin:0 0 4px;font-size:16px;font-weight:600;letter-spacing:.3px}
  #ui p{margin:0;font-size:12px;line-height:1.5;opacity:.85}
  #ui .hint{margin-top:8px;font-size:11px;opacity:.7}
  #btn{position:fixed;right:16px;top:16px;padding:9px 14px;border:1px solid rgba(255,255,255,.25);border-radius:10px;background:rgba(20,24,30,.55);color:#fff;font-size:13px;cursor:pointer;backdrop-filter:blur(6px)}
  #btn:hover{background:rgba(40,48,58,.65)}
  #btn kbd{font-family:inherit;opacity:.7}
  #loader{position:fixed;inset:0;display:flex;align-items:center;justify-content:center;color:#cfd6dd;font-size:14px;background:#101418}
</style>
</head>
<body>
<div id="app"></div>
<div id="ui">
  <h1>Castle Town in Spring</h1>
  <p>A voxel-style Japanese castle town: the five-level keep, moat, gate, market square, shrine and cherry blossoms.</p>
  <p class="hint">Drag to orbit · scroll to zoom · right-drag to pan · press <b>D</b> for day / dusk.</p>
</div>
<button id="btn">Day / Dusk <kbd>D</kbd></button>
<div id="loader">Building the town…</div>

<script type="importmap">
{
  "imports": {
    "three": "https://cdn.jsdelivr.net/npm/three@0.170.0/build/three.module.js",
    "three/addons/": "https://cdn.jsdelivr.net/npm/three@0.170.0/examples/jsm/"
  }
}
</script>

<script type="module">
import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
import { mergeGeometries } from 'three/addons/utils/BufferGeometryUtils.js';

/* ---------- seeded random ---------- */
function mulberry32(a){return function(){a|=0;a=a+0x6D2B79F5|0;let t=Math.imul(a^a>>>15,1|a);t=t+Math.imul(t^t>>>7,61|t)^t;return((t^t>>>14)>>>0)/4294967296;};}
const rng = mulberry32(1337);
const rand = (a,b)=>a+(b-a)*rng();
const randInt = (a,b)=>Math.floor(a+(b-a+1)*rng());
const pick = arr=>arr[Math.floor(rng()*arr.length)];

/* ---------- renderer / scene / camera ---------- */
const app = document.getElementById('app');
const renderer = new THREE.WebGLRenderer({antialias:true});
renderer.setPixelRatio(Math.min(window.devicePixelRatio,2));
renderer.setSize(window.innerWidth,window.innerHeight);
renderer.shadowMap.enabled = true;
renderer.shadowMap.type = THREE.PCFSoftShadowMap;
renderer.toneMapping = THREE.ACESFilmicToneMapping;
renderer.toneMappingExposure = 1.05;
app.appendChild(renderer.domElement);

const scene = new THREE.Scene();
const camera = new THREE.PerspectiveCamera(46, window.innerWidth/window.innerHeight, 0.1, 600);
camera.position.set(34, 26, 40);

const controls = new OrbitControls(camera, renderer.domElement);
controls.target.set(0, 6, 0);
controls.enableDamping = true;
controls.dampingFactor = 0.06;
controls.maxPolarAngle = Math.PI*0.49;
controls.minDistance = 12;
controls.maxDistance = 140;
controls.update();

/* ---------- materials ---------- */
const M = {
  grass:   new THREE.MeshStandardMaterial({color:0x7f9a52, roughness:0.95}),
  grass2:  new THREE.MeshStandardMaterial({color:0x6f8a48, roughness:0.95}),
  road:    new THREE.MeshStandardMaterial({color:0x8b8577, roughness:0.9}),
  stone:   new THREE.MeshStandardMaterial({color:0x8f8f8f, roughness:0.85}),
  stoneDark:new THREE.MeshStandardMaterial({color:0x6f6f6f, roughness:0.85}),
  wall:    new THREE.MeshStandardMaterial({color:0xf1efe6, roughness:0.85}),
  wood:    new THREE.MeshStandardMaterial({color:0x4a3527, roughness:0.7}),
  woodDark:new THREE.MeshStandardMaterial({color:0x33241a, roughness:0.7}),
  roofDark:new THREE.MeshStandardMaterial({color:0x2b3138, roughness:0.6}),
  roofRed: new THREE.MeshStandardMaterial({color:0x7a3b34, roughness:0.6}),
  roofBlue:new THREE.MeshStandardMaterial({color:0x3f5566, roughness:0.6}),
  roofThatch:new THREE.MeshStandardMaterial({color:0x8a7a4a, roughness:0.9}),
  water:   new THREE.MeshStandardMaterial({color:0x3f6f7a, roughness:0.35, metalness:0.15}),
  trunk:   new THREE.MeshStandardMaterial({color:0x5a4632, roughness:0.9}),
  pink:    new THREE.MeshStandardMaterial({color:0xf2b7c8, roughness:0.85}),
  pinkDeep:new THREE.MeshStandardMaterial({color:0xe79fb4, roughness:0.85}),
  green:   new THREE.MeshStandardMaterial({color:0x5f8a44, roughness:0.9}),
  greenDeep:new THREE.MeshStandardMaterial({color:0x4c7338, roughness:0.9}),
  gold:    new THREE.MeshStandardMaterial({color:0xc9a24b, roughness:0.4, metalness:0.35}),
  red:     new THREE.MeshStandardMaterial({color:0xb23a34, roughness:0.6}),
  lantern: new THREE.MeshStandardMaterial({color:0x9a9a9a, roughness:0.85}),
  lanternGlow:new THREE.MeshStandardMaterial({color:0xffd27a, emissive:0xffb040, emissiveIntensity:0.6, roughness:0.5}),
  paper:   new THREE.MeshStandardMaterial({color:0xf3e6c8, roughness:0.9}),
  stall:   new THREE.MeshStandardMaterial({color:0x6b5a44, roughness:0.85}),
};

/* ---------- shared geometries ---------- */
const boxGeo = new THREE.BoxGeometry(1,1,1);
const sphereGeo = new THREE.SphereGeometry(1,12,10);
const coneGeo = new THREE.ConeGeometry(1,1,14);
const cylGeo = new THREE.CylinderGeometry(1,1,1,12);

/* ---------- helpers ---------- */
function addBox(geo, mat, x,y,z, sx,sy,sz, cast=true, recv=true){
  const g = boxGeo.clone();
  g.scale(sx,sy,sz);
  g.translate(x,y,z);
  const mesh = new THREE.Mesh(g, mat);
  mesh.castShadow = cast; mesh.receiveShadow = recv;
  scene.add(mesh);
  return mesh;
}
function addSphere(geo, mat, x,y,z, r, cast=true, recv=true){
  const g = geo.clone(); g.scale(r,r,r); g.translate(x,y,z);
  const mesh = new THREE.Mesh(g, mat);
  mesh.castShadow = cast; mesh.receiveShadow = recv;
  scene.add(mesh);
  return mesh;
}
function addCone(geo, mat, x,y,z, r,h, cast=true, recv=true){
  const g = geo.clone(); g.scale(r,h,r); g.translate(x,y,z);
  const mesh = new THREE.Mesh(g, mat);
  mesh.castShadow = cast; mesh.receiveShadow = recv;
  scene.add(mesh);
  return mesh;
}
function addCyl(geo, mat, x,y,z, r,h, cast=true, recv=true){
  const g = geo.clone(); g.scale(r,h,r); g.translate(x,y,z);
  const mesh = new THREE.Mesh(g, mat);
  mesh.castShadow = cast; mesh.receiveShadow = recv;
  scene.add(mesh);
  return mesh;
}

/* ---------- terrain ---------- */
const ground = new THREE.Mesh(new THREE.PlaneGeometry(200,200), M.grass);
ground.rotation.x = -Math.PI/2;
ground.receiveShadow = true;
scene.add(ground);

// gentle rolling hills
const hillGeo = new THREE.SphereGeometry(1,16,12);
for(let i=0;i<14;i++){
  const x = rand(-70,70), z = rand(-70,70);
  if(Math.hypot(x,z) < 22) continue;
  const r = rand(6,12);
  addSphere(hillGeo, rng()<0.5?M.grass:M.grass2, x, -r*0.55, z, r, false, true);
}

/* ---------- castle base (stone) ---------- */
addBox(boxGeo, M.stone, 0, 1.5, 0, 16, 3, 16, true, true);
addBox(boxGeo, M.stoneDark, 0, 3.0, 0, 14, 0.6, 14, true, true);

/* ---------- castle keep: five levels ---------- */
const levels = [
  {w:12, h:3.2, y:3.6},
  {w:10, h:2.8, y:6.8},
  {w:8,  h:2.6, y:9.6},
  {w:6,  h:2.4, y:12.2},
  {w:4,  h:2.2, y:14.6},
];
levels.forEach((L,i)=>{
  // white wall body
  addBox(boxGeo, M.wall, 0, L.y+L.h/2, 0, L.w, L.h, L.w, true, true);
  // dark wooden trim band
  addBox(boxGeo, M.woodDark, 0, L.y+L.h-0.15, 0, L.w+0.4, 0.35, L.w+0.4, true, true);
  // layered curved roof (pyramid)
  const roofBase = L.w + 1.6;
  addCone(coneGeo, M.roofDark, 0, L.y+L.h+1.0, 0, roofBase*0.72, 2.0, true, true);
  addCone(coneGeo, M.roofDark, 0, L.y+L.h+1.9, 0, roofBase*0.45, 1.2, true, true);
  // gold finial on top level
  if(i===levels.length-1){
    addCyl(cylGeo, M.gold, 0, L.y+L.h+2.6, 0, 0.18, 1.2, true, true);
    addSphere(sphereGeo, M.gold, 0, L.y+L.h+3.4, 0, 0.35, true, true);
  }
});

/* ---------- stone walls around castle ---------- */
const wallGeo = new THREE.BoxGeometry(1,1,1);
const wallParts = [];
function wallSeg(x,z,len,thick,h){
  const g = wallGeo.clone(); g.scale(len,h,thick); g.translate(x,h/2+3,z);
  wallParts.push(g);
}
// perimeter walls with a gate opening on the south
wallSeg(0, -14, 20, 1.2, 3.2);          // north
wallSeg(-14, 0, 1.2, 20, 3.2);          // west
wallSeg(14, 0, 1.2, 20, 3.2);           // east
wallSeg(-11, 14, 6, 1.2, 3.2);          // south-west
wallSeg(11, 14, 6, 1.2, 3.2);           // south-east
// gate towers
addBox(boxGeo, M.stoneDark, -4, 4.6, 14, 2.2, 3.2, 2.2, true, true);
addBox(boxGeo, M.stoneDark, 4, 4.6, 14, 2.2, 3.2, 2.2, true, true);
addCone(coneGeo, M.roofDark, -4, 8.4, 14, 1.6, 1.4, true, true);
addCone(coneGeo, M.roofDark, 4, 8.4, 14, 1.6, 1.4, true, true);
// gate roof beam
addBox(boxGeo, M.woodDark, 0, 6.4, 14, 8, 0.4, 1.6, true, true);
// gate doors
addBox(boxGeo, M.wood, -1.2, 4.2, 14, 2.4, 2.4, 0.3, true, true);
addBox(boxGeo, M.wood, 1.2, 4.2, 14, 2.4, 2.4, 0.3, true, true);
// wall top trim
wallParts.push((()=>{const g=wallGeo.clone();g.scale(20,0.3,1.4);g.translate(0,6.4,-14);return g;})());
const wallMesh = new THREE.Mesh(mergeGeometries(wallParts), M.stone);
wallMesh.castShadow = true; wallMesh.receiveShadow = true;
scene.add(wallMesh);

/* ---------- moat + water ---------- */
// moat ring around castle base
const moatParts = [];
function moatSeg(x,z,len,thick){
  const g = wallGeo.clone(); g.scale(len,0.6,thick); g.translate(x,0.3,z);
  moatParts.push(g);
}
moatSeg(0,-18,24,3); moatSeg(0,18,24,3);
moatSeg(-18,0,3,24); moatSeg(18,0,3,24);
const moatMesh = new THREE.Mesh(mergeGeometries(moatParts), M.stoneDark);
moatMesh.receiveShadow = true; scene.add(moatMesh);

// water surface
const waterGeo = new THREE.PlaneGeometry(24,24);
const water = new THREE.Mesh(waterGeo, M.water);
water.rotation.x = -Math.PI/2; water.position.y = 0.35;
water.receiveShadow = true; scene.add(water);

/* ---------- bridge ---------- */
addBox(boxGeo, M.wood, 0, 0.6, 18, 3.2, 0.3, 6, true, true);
for(let i=-1;i<=1;i+=2){
  addBox(boxGeo, M.woodDark, i*1.4, 1.0, 18, 0.2, 0.8, 6, true, true);
}

/* ---------- roads & market square ---------- */
const roadParts = [];
function roadSeg(x,z,len,thick){
  const g = wallGeo.clone(); g.scale(len,0.12,thick); g.translate(x,0.06,z);
  roadParts.push(g);
}
// main road from gate to market
roadSeg(0, 24, 3.4, 20);
// market square
roadSeg(0, 34, 16, 12);
// east road
roadSeg(20, 34, 24, 3.4);
// narrow alleys
roadSeg(-14, 30, 3, 14);
roadSeg(14, 28, 3, 12);
roadSeg(-10, 40, 3, 10);
const roadMesh = new THREE.Mesh(mergeGeometries(roadParts), M.road);
roadMesh.receiveShadow = true; scene.add(roadMesh);

/* ---------- town buildings ---------- */
const houseRoof = [M.roofDark, M.roofRed, M.roofBlue, M.roofThatch];
const houseWall = [M.wall, M.paper, M.stall];
const townParts = [];
function addBuilding(x,z,w,d,h,roofMat,wallMat){
  // body
  const g = wallGeo.clone(); g.scale(w,h,d); g.translate(x,h/2,z);
  townParts.push(g);
  // roof (pyramid)
  const rg = coneGeo.clone(); rg.scale(w*0.72, 1.4, d*0.72); rg.translate(x, h+0.7, z);
  townParts.push(rg);
  // small dark base trim
  const bg = wallGeo.clone(); bg.scale(w+0.2,0.25,d+0.2); bg.translate(x,h-0.1,z);
  townParts.push(bg);
}

// place buildings around town, avoiding roads & castle
const placed = [];
function free(x,z){
  if(Math.hypot(x,z) < 20) return false;
  for(const p of placed){ if(Math.hypot(p.x-x,p.z-z) < 4.5) return false; }
  // avoid main road & market
  if(Math.abs(x) < 2.2 && z > 14 && z < 40) return false;
  if(Math.abs(z-34) < 6 && Math.abs(x) < 8) return false;
  return true;
}
let count = 0;
for(let i=0;i<140 && count<85;i++){
  const x = rand(-30,30), z = rand(16,48);
  if(!free(x,z)) continue;
  const big = rng()<0.18;
  const w = big?rand(4,6):rand(2.2,3.4);
  const d = big?rand(3,5):rand(2.2,3.4);
  const h = big?rand(2.4,3.2):rand(1.6,2.4);
  addBuilding(x,z,w,d,h, pick(houseRoof), pick(houseWall));
  placed.push({x,z});
  count++;
}
const townMesh = new THREE.Mesh(mergeGeometries(townParts), M.wall);
townMesh.castShadow = true; townMesh.receiveShadow = true;
scene.add(townMesh);

// separate roof-colored buildings: rebuild with per-building material via instanced approach
// (simpler: add a few colored roofs as individual meshes for variety)
for(let i=0;i<20;i++){
  const x = rand(-28,28), z = rand(18,46);
  if(!free(x,z)) continue;
  const w=rand(2.4,3.6), d=rand(2.4,3.6), h=rand(1.8,2.6);
  addBuilding(x,z,w,d,h, pick(houseRoof), pick(houseWall));
  placed.push({x,z});
}

/* ---------- market stalls ---------- */
for(let i=0;i<10;i++){
  const x = rand(-6,6), z = rand(30,38);
  addBox(boxGeo, M.stall, x, 0.9, z, 1.6, 1.2, 1.6, true, true);
  addCone(coneGeo, pick([M.roofRed,M.roofBlue,M.roofThatch]), x, 2.0, z, 1.1, 0.8, true, true);
}

/* ---------- trees (instanced) ---------- */
const trunkGeo = cylGeo.clone(); trunkGeo.scale(0.18,1.6,0.18); trunkGeo.translate(0,0.8,0);
const canopyGeo = sphereGeo.clone(); canopyGeo.scale(1,1,1);
const pinkInst = new THREE.InstancedMesh(canopyGeo, M.pink, 60);
const pinkDeepInst = new THREE.InstancedMesh(canopyGeo, M.pinkDeep, 20);
const greenInst = new THREE.InstancedMesh(canopyGeo, M.green, 30);
const trunkInst = new THREE.InstancedMesh(trunkGeo, M.trunk, 110);
pinkInst.castShadow = pinkDeepInst.castShadow = greenInst.castShadow = trunkInst.castShadow = true;
pinkInst.receiveShadow = pinkDeepInst.receiveShadow = greenInst.receiveShadow = true;

const dummy = new THREE.Object3D();
let pinkCount=0, pinkDeepCount=0, greenCount=0, trunkCount=0;
function tree(x,z, isPink){
  const r = rand(1.2,2.2);
  dummy.position.set(x, r+1.6, z); dummy.scale.set(r,r*0.9,r); dummy.rotation.y = rand(0,6.28);
  dummy.updateMatrix();
  if(isPink){
    if(rng()<0.35 && pinkDeepCount<20){ pinkDeepInst.setMatrixAt(pinkDeepCount++, dummy.matrix); }
    else if(pinkCount<60){ pinkInst.setMatrixAt(pinkCount++, dummy.matrix); }
    else return;
  } else {
    if(greenCount<30) greenInst.setMatrixAt(greenCount++, dummy.matrix);
    else return;
  }
  if(trunkCount<110){
    dummy.position.set(x,0.8,z); dummy.scale.set(1,1,1); dummy.rotation.y=0; dummy.updateMatrix();
    trunkInst.setMatrixAt(trunkCount++, dummy.matrix);
  }
}
// cherry blossoms along moat & main road
for(let i=0;i<40;i++){
  const ang = rand(0,6.28), rad = rand(20,26);
  tree(Math.cos(ang)*rad, Math.sin(ang)*rad, true);
}
for(let i=0;i<18;i++){
  tree(rand(-2,2), rand(16,44), true);
}
// green trees scattered
for(let i=0;i<30;i++){
  const x=rand(-30,30), z=rand(16,48);
  if(Math.hypot(x,z)<20) continue;
  tree(x,z,false);
}
pinkInst.count = pinkCount; pinkDeepInst.count = pinkDeepCount; greenInst.count = greenCount; trunkInst.count = trunkCount;
scene.add(pinkInst, pinkDeepInst, greenInst, trunkInst);

/* ---------- shrine + torii + lanterns ---------- */
// shrine building
addBox(boxGeo, M.wall, -18, 1.6, 30, 3, 2.2, 3, true, true);
addCone(coneGeo, M.roofRed, -18, 3.4, 30, 2.4, 1.4, true, true);
addBox(boxGeo, M.woodDark, -18, 2.6, 30, 3.2, 0.3, 3.2, true, true);

// torii gate
const tx=-18, tz=36;
addCyl(cylGeo, M.red, tx-1.2, 2.2, tz, 0.22, 4.4, true, true);
addCyl(cylGeo, M.red, tx+1.2, 2.2, tz, 0.22, 4.4, true, true);
addBox(boxGeo, M.red, tx, 4.4, tz, 3.4, 0.35, 0.35, true, true);
addBox(boxGeo, M.red, tx, 3.6, tz, 2.8, 0.25, 0.25, true, true);

// stone lanterns
function lantern(x,z){
  addCyl(cylGeo, M.lantern, x, 0.4, z, 0.35, 0.8, true, true);
  addBox(boxGeo, M.lantern, x, 1.1, z, 0.7, 0.7, 0.7, true, true);
  addCone(coneGeo, M.lantern, x, 1.7, z, 0.5, 0.5, true, true);
  addSphere(sphereGeo, M.lanternGlow, x, 1.1, z, 0.28, false, false);
}
lantern(-16,34); lantern(-20,34); lantern(-18,32);

// fences
for(let i=0;i<12;i++){
  const x = -18 + rand(-4,4), z = 28 + rand(-2,2);
  addBox(boxGeo, M.wood, x, 0.6, z, 0.15, 1.2, 0.15, true, true);
}

/* ---------- lighting ---------- */
const hemi = new THREE.HemisphereLight(0xbfd4e6, 0x5a6b45, 0.7);
scene.add(hemi);
const sun = new THREE.DirectionalLight(0xfff2e0, 1.4);
sun.position.set(30, 45, 20);
sun.castShadow = true;
sun.shadow.mapSize.set(2048,2048);
sun.shadow.camera.left = -60; sun.shadow.camera.right = 60;
sun.shadow.camera.top = 60; sun.shadow.camera.bottom = -60;
sun.shadow.camera.near = 1; sun.shadow.camera.far = 140;
sun.shadow.bias = -0.0005;
scene.add(sun);

scene.fog = new THREE.Fog(0xcfe0e8, 40, 130);
scene.background = new THREE.Color(0xcfe0e8);

/* ---------- day / dusk toggle ---------- */
let dusk = false;
function applyLighting(){
  if(dusk){
    hemi.color.set(0x3a4a66); hemi.groundColor.set(0x2a2f38); hemi.intensity = 0.45;
    sun.color.set(0xffb066); sun.intensity = 0.9;
    sun.position.set(-25, 18, -15);
    scene.fog.color.set(0x2b3550); scene.background.set(0x2b3550);
    M.lanternGlow.emissiveIntensity = 1.6;
  } else {
    hemi.color.set(0xbfd4e6); hemi.groundColor.set(0x5a6b45); hemi.intensity = 0.7;
    sun.color.set(0xfff2e0); sun.intensity = 1.4;
    sun.position.set(30, 45, 20);
    scene.fog.color.set(0xcfe0e8); scene.background.set(0xcfe0e8);
    M.lanternGlow.emissiveIntensity = 0.6;
  }
}
function toggle(){ dusk = !dusk; applyLighting(); }
document.getElementById('btn').addEventListener('click', toggle);
window.addEventListener('keydown', e=>{ if(e.key==='d'||e.key==='D') toggle(); });

/* ---------- resize ---------- */
window.addEventListener('resize', ()=>{
  camera.aspect = window.innerWidth/window.innerHeight;
  camera.updateProjectionMatrix();
  renderer.setSize(window.innerWidth, window.innerHeight);
});

/* ---------- render loop ---------- */
document.getElementById('loader').style.display = 'none';
function animate(){
  requestAnimationFrame(animate);
  controls.update();
  renderer.render(scene, camera);
}
animate();
</script>
</body>
</html>
```