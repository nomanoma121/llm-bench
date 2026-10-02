We need answer user wants single index.html code block only, no after. Need produce complete working page. Need think carefully. Need ensure final only html code block. Need likely 500-900 lines. Need include import map, Three.js, OrbitControls. Seeded random 1337. Scene: stylized Japanese castle town spring, voxel-style blocks but real proportions, layered detail. Five-level castle keep on raised stone base center, each level smaller, curved layered roofs, white walls dark wooden trim. Stone walls, moat water around castle, bridge, main gate. Town 60-100 small buildings south/east: varied houses shops warehouses, roads, alleys, market square. 40-60 cherry blossom pink concentrated along moat/main road, green trees. Shrine torii, stone lanterns, fences props. Gentle terrain castle above town. Soft spring daylight shadows, fog, default camera. Interaction OrbitControls, key/button toggle day/dusk. Technical: self-contained, import map, no external assets, seeded random, reuse geometries/materials, InstancedMesh or merged geometry for repeated parts. Handle resize.

Need craft code robust. Need maybe use BufferGeometryUtils.mergeGeometries from three/addons/utils/BufferGeometryUtils.js. Import map. Use three@0.170.0. Need ensure import map includes addons. Use ES modules.

Need design. We can create scene with groups. Use seeded RNG. Need generate layout. Need avoid too complex but enough. Need performance. Use InstancedMesh for repeated parts: building blocks, roofs, trees, lanterns, fences, torii parts? Could use merged geometry for castle? Simpler: use InstancedMesh for many boxes, roofs, tree trunks, blossoms. But need varied colors. InstancedMesh can have per-instance color if material supports vertexColors? For MeshStandardMaterial with instanceColor. Use setColorAt. Need shadows. InstancedMesh castShadow/receiveShadow.

Need water: plane with transparent material, maybe animated? Could use simple plane. Fog. Day/dusk lighting: two directional lights? Or change sun color/intensity, hemisphere, fog, sky background. Toggle key 'd' or button. Need maybe animate water? Could simple.

Need ensure no external textures. Use colors.

Potential pitfalls: import map in HTML. Use type="module". Need import OrbitControls from 'three/addons/controls/OrbitControls.js'. BufferGeometryUtils from 'three/addons/utils/BufferGeometryUtils.js'.

Need code length. Let's outline.

HTML:
<!doctype html>
<html><head> meta charset, title, style body margin 0 overflow hidden, canvas, UI panel. import map. script module.

UI: button toggle day/dusk, info.

JS:
import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
import * as BufferGeometryUtils from 'three/addons/utils/BufferGeometryUtils.js';

Seeded RNG: mulberry32.

Scene, camera, renderer. renderer = new WebGLRenderer({antialias:true}); shadowMap enabled. scene fog.

Materials:
- stone, whiteWall, darkWood, roofDark, roofTile? cherryPink, cherryPinkLight, greenLeaf, trunk, water, road, grass, lanternStone, toriiRed, market? Use MeshStandardMaterial.

Geometries:
- box unit? For instanced boxes, use BoxGeometry(1,1,1). For roofs, maybe BoxGeometry? Need curved layered roofs. Could use custom roof geometry: a pyramid-like or hip roof. Voxel-inspired but layered. Could create roof geometry as a box with top? For Japanese roofs, use a flat box plus smaller boxes? Maybe use a custom geometry: a hip roof made from a box scaled? Could use ConeGeometry? But voxel-style. Need layered detail. Could create roof as a box with overhang and ridge. For instanced roofs, use a simple roof geometry: BoxGeometry(1,0.2,1) plus maybe top? But curved layered roofs for castle. Could use multiple boxes. For town roofs, use a simple gable roof geometry: two slanted boxes? Could create a geometry function makeRoof(width, depth, height, pitch) using boxes? For instanced, need one geometry. Could create a roof geometry as a box with top ridge? Maybe use a custom BufferGeometry for a hip roof: vertices. But simpler: use BoxGeometry for roof slab, plus ridge box? For repeated town roofs, use a roof geometry that is a box with a ridge? Could use a triangular prism? Let's design.

For voxel-style, roofs can be boxes with slight pitch. Use BoxGeometry(1,0.2,1) as roof slab, but not curved. Could use a "layered roof" geometry: a box plus smaller box on top, maybe merged. For instanced, can create a geometry with multiple boxes merged: base roof slab, ridge, maybe eaves. Use mergeGeometries. For each building, scale instance. But if geometry includes multiple boxes, scaling instance scales all. Good.

Need castle: five levels, each smaller. Could build manually with boxes. Use merged geometry for castle? Could use group with meshes. Need shadows. Use materials. Could use helper addBox(group, x,y,z,w,h,d, material). For castle, many boxes. Could use InstancedMesh for repeated castle parts? Not necessary. But need reuse. Could create castle as group with boxes.

Need terrain: ground plane. Castle raised stone base. Could use a large ground plane, maybe with grass. Castle base as stepped stone platform. Town lower. Need gentle terrain. Could use a plane with height? Simpler: ground plane at y=0, castle base raised by boxes. Town buildings on ground. Castle above town. Could add a hill under castle: a few stacked boxes or a cone? Voxel terrain. Use a large plane. Maybe create terrain as grid of boxes? Too many. Use plane. Castle base: stone platform with walls.

Moat: water around castle. Could be a ring plane or boxes. Need water around castle. Could create a rectangular moat as a plane with hole? Simpler: water plane at y=0.05 around castle, with castle base above. But ground plane also. Need avoid water under castle. Could create water as a large plane with a hole? Or use a ring geometry? Or create water as a plane covering area, then castle base blocks above. If ground plane at y=0, water plane at y=0.02 covering moat area. But water over town? Could make water only around castle as a rectangular ring using boxes? Could use a plane with a central hole via ShapeGeometry? Or use multiple water planes around castle. Simpler: create a large water plane for moat area, but town outside. Use a rectangular water plane from x=-30 to 30, z=-30 to 30, but castle base covers center. Town south/east outside. If water plane covers town, bad. Could make water as a ring: four rectangular planes around castle. Or use ShapeGeometry with hole. Use THREE.Shape with rectangle and hole, extrude? Or ShapeGeometry. Need water material. Could create water plane as a ShapeGeometry with hole. Coordinates. Or use a large plane and set castle base above, but water visible under town? If town outside water area, okay. Define water area as rectangle around castle, not entire town. Use ShapeGeometry with hole for moat. ShapeGeometry is flat. Need maybe water surface. Use ShapeGeometry with hole. Good.

Shape: outer rectangle x -24 to 24, z -24 to 24, hole x -14 to 14, z -14 to 14. But castle base maybe 12x12. Water ring. Bridge crossing. Need bridge over water. Could add bridge boxes. Main gate on north? Town south/east. Need main gate maybe on east or south. Bridge from town to gate. Could place gate at south side of castle, bridge across moat. Need stone walls around castle. Could create wall boxes around perimeter, with gate opening. Use instanced? Could manually.

Town layout: Need 60-100 buildings. Could generate grid blocks south/east. Roads. Need avoid moat. Use seeded random. Place buildings on grid cells, skip roads. Need market square. Need roads as boxes or plane. Could use road material. Use instanced boxes for building walls, roofs. Need per-instance colors. Need building types: houses, shops, warehouses. Sizes. Need roofs. Need maybe fences. Need trees. Need shrine.

Approach: Use InstancedMesh for building bodies and roofs. Need count. Could first generate building data, then create InstancedMesh with count. For each building, set matrix and color. For roofs, set matrix. Need geometry. For building body, BoxGeometry(1,1,1). For roof, custom geometry. For trees, trunk instanced, foliage instanced. For cherry blossoms, maybe multiple foliage boxes? Use instanced boxes with pink colors. For green trees, green. Could use one instanced mesh for all foliage with colors. Trunks one instanced. Need count. Generate tree data.

Need lanterns, torii, fences. Could use instanced or individual. Could use merged geometry for props? Simpler: use InstancedMesh for lanterns, fences, torii parts. But torii unique. Could create group.

Need shadows: renderer.shadowMap. Directional light. Need shadow camera bounds. Use large.

Day/dusk: function setLighting(mode). Use sun light, hemisphere, ambient. Change scene.background, fog color, materials? Could keep materials. For dusk, lower intensity, warmer colors, maybe add point lights? Could add lantern lights? Maybe not. Could add a few point lights for dusk? Could be nice. But performance. Could add 3 point lights near market/shrine. Toggle. Need button and key 'd'.

Need camera default view: position maybe (45, 35, 60), target (0, 8, 0). Castle focal.

Need ensure import map: 
<script type="importmap">
{ "imports": { "three": "https://cdn.jsdelivr.net/npm/three@0.170.0/build/three.module.js", "three/addons/": "https://cdn.jsdelivr.net/npm/three@0.170.0/examples/jsm/" } }
</script>

Need code. Need maybe use BufferGeometryUtils.mergeGeometries. Need ensure geometries have same attributes. BoxGeometry has position, normal, uv. Good.

Potential issue: InstancedMesh with setColorAt requires material.vertexColors? Actually InstancedMesh instanceColor works if material has vertexColors? In three, instanceColor is used if material.vertexColors? I think InstancedMesh supports instanceColor automatically if setColorAt called, but material must have vertexColors? Let's recall: InstancedMesh has instanceColor attribute. In WebGLRenderer, if instanceColor exists, it uses it. Material doesn't need vertexColors? I think it does not require vertexColors? Actually for InstancedMesh, instanceColor is multiplied by material color. It works. But to be safe, set material.vertexColors = false? Hmm. In three, InstancedMesh instanceColor is supported. setColorAt. Need call instanceColor.needsUpdate. Good.

Need maybe use MeshStandardMaterial with flatShading? Voxel style. Could set flatShading true for boxes. But for roofs maybe. Use flatShading true.

Need custom roof geometry. Let's design a simple hip roof geometry using boxes merged:
- base slab: BoxGeometry(w, 0.15, d) at y=0.075? If instance scaling, geometry centered at origin. For building, place roof at top. Could create roof geometry centered at origin with base at y=0? If we set matrix position at roof base, geometry extends upward. For BoxGeometry centered, need offset. Could create geometry with translate. For roof: base slab from y=0 to 0.15, ridge from y=0.15 to 0.35, maybe top. Use BoxGeometry(1,0.15,1), translate y=0.075. Ridge BoxGeometry(0.2,0.2,1), translate y=0.25. Maybe eaves. For gable roof, ridge along z? Need orientation. Could make roof geometry with ridge along x or z. For instanced, can rotate instance. Simpler: roof geometry as a pyramid? Use ConeGeometry? But voxel. Could use a "layered roof" geometry: three stacked boxes decreasing size, centered. That gives stepped pyramid, good for castle roofs. For town, maybe gable. Could use same stepped roof for all. For Japanese roofs, layered boxes. Use geometry: base slab width 1, depth 1, height 0.12; second slab 0.7,0.7,0.12 at y=0.12; third 0.4,0.4,0.12 at y=0.24; ridge 0.1,0.1,0.3 at y=0.36. This is a stepped pyramid. Good. For building roof, scale x,z to building width/depth, y maybe roof height. But if scale y, all layers scale. Good. Could use for all roofs. For castle, use custom larger layered roofs with overhang. Could manually add boxes.

Need building body geometry: BoxGeometry(1,1,1). For instanced, scale to width,height,depth. Position at base y. Need color. For white walls, dark trim? Could use body material white, roof dark. For shops maybe dark wood. Use instance colors. Need maybe building base? Could add dark trim as thin box? Could use body color varied. For dark wooden trim, maybe add instanced trim boxes? Could add a dark base/edge. Simpler: building body white, roof dark. Some buildings dark wood. Use colors.

Need roads: Could use plane or boxes. Use road material. Could create a merged geometry for roads? Or use a large ground plane with road patches. Simpler: use a ground plane, then add road boxes as thin boxes. Could use InstancedMesh for road segments. Generate road grid. Need market square. Could add road segments as boxes. Use instanced boxes with road color. Need maybe ground grass. Use large plane. Could add terrain hill. Use plane.

Need town layout. Let's define coordinate system. Castle center at (0,0,0). Town south/east. Need south maybe positive z? Let's choose z positive south, x positive east. Camera from southeast. Castle base at center. Moat ring around center. Town in x>15, z>15? But also south/east. Need roads. Could generate grid from x=18 to 70, z=18 to 70. Buildings placed in blocks. Need main road from gate to town. Gate maybe at south side of castle, bridge at z=15, road along z from 15 to 70 at x=0? But town east? Could have main road from gate south then east. Need market square around (35,35). Need avoid moat. Buildings outside water ring. Water outer maybe 24. Town start x=26,z=26. Good.

Need castle walls around center. Could create stone wall ring from -18 to 18, height 3, thickness 1. Gate opening at south z=18, x=0. Bridge from z=18 to 24? Wait water outer 24, wall at 18, water between 14 and 24? Need consistent.

Let's define castle base: stone platform from x=-12 to 12, z=-12 to 12, height 4. Keep on top. Stone walls around base? Maybe walls at perimeter x=-16..16, z=-16..16, height 3, thickness 1. Moat water ring outer x=-24..24, z=-24..24, hole x=-14..14? If walls at 16, water from 14 to 24. Bridge from wall gate at z=16 to town at z=24. Good.

But town south/east outside water. Need roads start at z=24. Good.

Castle keep: base platform y=4. Level 1 from y=4 to 10, footprint 12x12? Each level smaller. Could use boxes. Need white walls, dark trim. Roofs. Five levels. Could create helper addBox. Use materials. Need maybe roof overhang. For each level: wall box, roof box. Level sizes: 12,10,8,6,4. Height 4 each. Roof height 1.5. Use dark roof. Add dark trim at base/top? Could add thin boxes. Use group.

Need stone base: stepped. Could add boxes. Use stone material. Maybe base from y=0 to 4, footprint 16x16, with steps. Add stone walls. Need gate: main gate structure at south wall. Could add gate posts, roof. Bridge: boxes.

Need shrine: small area maybe west or north? Town south/east, but shrine can be northwest? Need torii, stone lanterns, fences. Could place shrine at (-30,0,30) or (-20,0,40). Need not conflict. Use group. Torii red. Stone lanterns instanced. Fences instanced.

Need trees: 40-60 cherry, some green. Concentrated along moat and main road. Could generate along water ring and main road. Need total 40-60. Use seeded. Place cherry along moat perimeter outside water, along main road. Green trees scattered. Need avoid buildings. Could generate positions with grid and check distance. Simpler: manually generate along lines. Need count. Use arrays.

Need performance: Use InstancedMesh for building bodies, roofs, trees, lanterns, fences, road segments. Need counts known. Could generate data first.

Potential issue: InstancedMesh count must be exact. We can collect data arrays then create. For each data, set matrix. Need dummy object.

Need materials. For instanced colors, use material with color white, then setColorAt. For roofs, dark. For trees, foliage material with instance colors. Trunks brown. For road, maybe one material. For fences, wood. For lanterns, stone. For torii, red.

Need shadows: InstancedMesh castShadow true, receiveShadow true. Ground plane receive. Water maybe not.

Need water animation? Could add simple sine displacement? Not necessary. Could use MeshStandardMaterial transparent, opacity 0.7, roughness 0.1, metalness 0.0. Maybe add slight color. Could animate by rotating? No.

Need day/dusk: Use two lights? Simpler: one directional, one hemisphere. Toggle changes. Need maybe add point lights for dusk. Could create point lights and set intensity. Use button. Key 'd'. Need UI.

Need ensure code not too long but complete. Let's design data generation.

Seeded RNG:
function mulberry32(a){ return function(){ a |=0; a = a + 0x6D2B79F5 |0; var t = Math.imul(a ^ a>>>15, 1|a); t = t + Math.imul(t ^ t>>>7, 61|t) ^ t; return ((t ^ t>>>14) >>>0) / 4294967296; } }
const rng = mulberry32(1337);
function rand(min,max){ return min + rng()*(max-min); }
function randInt(min,max){ return Math.floor(rand(min,max+1)); }

Scene setup:
const scene = new THREE.Scene();
const camera = new THREE.PerspectiveCamera(45, innerWidth/innerHeight, 0.1, 500);
camera.position.set(60,40,70);
const renderer = new THREE.WebGLRenderer({antialias:true});
renderer.setSize(innerWidth, innerHeight);
renderer.setPixelRatio(Math.min(devicePixelRatio,2));
renderer.shadowMap.enabled = true;
renderer.shadowMap.type = THREE.PCFSoftShadowMap;
renderer.outputColorSpace = THREE.SRGBColorSpace;
document.body.appendChild(renderer.domElement);
controls = new OrbitControls(camera, renderer.domElement); controls.target.set(0,10,0); controls.enableDamping = true; maxPolarAngle = Math.PI/2 - 0.05; minDistance 10 maxDistance 200.

Lighting:
const hemi = new THREE.HemisphereLight(0xbfd8ff, 0x8a7a66, 0.7);
const sun = new THREE.DirectionalLight(0xfff2d8, 1.2);
sun.position.set(40,60,30); sun.castShadow=true; sun.shadow.mapSize.set(2048,2048); sun.shadow.camera.left=-100; right=100; top=100; bottom=-100; near=1; far=200; scene.add(sun, hemi);
Maybe ambient.
Day/dusk function:
let mode='day';
function setLighting(mode){ if day: scene.background = new THREE.Color(0xd8e7f5); fog = new THREE.Fog(0xd8e7f5, 80, 220); hemi.color.set(0xbfd8ff); hemi.groundColor.set(0x8a7a66); hemi.intensity=0.7; sun.color.set(0xfff2d8); sun.intensity=1.2; sun.position.set(40,60,30); point lights intensity 0; else: scene.background = new THREE.Color(0x2b3550); fog.color.set(0x2b3550); fog.near=60; fog.far=180; hemi.color.set(0x4a5a7a); hemi.groundColor.set(0x2a2418); hemi.intensity=0.35; sun.color.set(0xffb070); sun.intensity=0.45; sun.position.set(-30,20,40); point lights intensity 1.2; }
Need fog created. scene.fog = new THREE.Fog(...). Use fog.color.

Point lights: maybe 4. Add to scene. For dusk, warm. Could be near market, shrine, gate. Use PointLight(0xffd28a, 0, 30). Set intensity.

Materials:
const matStone = new THREE.MeshStandardMaterial({color:0x8f8f8f, roughness:0.9, flatShading:true});
const matWhite = new THREE.MeshStandardMaterial({color:0xf5f2ea, roughness:0.8, flatShading:true});
const matDarkWood = new THREE.MeshStandardMaterial({color:0x3b2f28, roughness:0.8, flatShading:true});
const matRoof = new THREE.MeshStandardMaterial({color:0x2f3a44, roughness:0.7, flatShading:true});
const matRoofTown = new THREE.MeshStandardMaterial({color:0x4a4f55, roughness:0.8, flatShading:true});
const matWater = new THREE.MeshStandardMaterial({color:0x4a7a8c, roughness:0.2, metalness:0.1, transparent:true, opacity:0.75, flatShading:true});
const matGrass = new THREE.MeshStandardMaterial({color:0x7fa86a, roughness:0.9, flatShading:true});
const matRoad = new THREE.MeshStandardMaterial({color:0x9a8f7f, roughness:0.9, flatShading:true});
const matTrunk = new THREE.MeshStandardMaterial({color:0x5a4332, roughness:0.9, flatShading:true});
const matFoliage = new THREE.MeshStandardMaterial({color:0xffffff, roughness:0.8, flatShading:true}); // instance colors
const matLantern = new THREE.MeshStandardMaterial({color:0xb5b5b5, roughness:0.8, flatShading:true});
const matTorii = new THREE.MeshStandardMaterial({color:0xc44536, roughness:0.7, flatShading:true});
const matFence = new THREE.MeshStandardMaterial({color:0x6b5b4a, roughness:0.9, flatShading:true});
const matMarket = maybe.

Geometries:
const boxGeo = new THREE.BoxGeometry(1,1,1);
const roofGeo = createRoofGeometry(); // merged boxes.
const treeGeo? For foliage, use boxGeo. For trunk, boxGeo. Could use same boxGeo. For lantern, maybe boxGeo. For torii, custom.

createRoofGeometry:
function makeRoofGeometry(){
 const parts=[];
 const p1 = new THREE.BoxGeometry(1,0.12,1); p1.translate(0,0.06,0); parts.push(p1);
 const p2 = new THREE.BoxGeometry(0.72,0.12,0.72); p2.translate(0,0.18,0); parts.push(p2);
 const p3 = new THREE.BoxGeometry(0.45,0.12,0.45); p3.translate(0,0.30,0); parts.push(p3);
 const ridge = new THREE.BoxGeometry(0.12,0.12,0.35); ridge.translate(0,0.42,0); parts.push(ridge);
 return BufferGeometryUtils.mergeGeometries(parts);
}
But for town roofs, if scale x,z, ridge scales too. Good. Could make roof height 0.45. For building, roof instance scale (w, roofHeight, d). If roofHeight 0.45, geometry height 0.45? Actually geometry height 0.45, scaling y by 0.45? If geometry already height 0.45, scaling y=1 gives height 0.45. If we want roof height variable, set scale y = roofHeight / 0.45? Simpler: geometry height 1? Let's make roof geometry normalized height 1. Use parts with y from 0 to 1. Then instance scale y = roofHeight. For base slab height 0.25, second 0.25, third 0.25, ridge 0.25. Total 1. Good.
function makeRoofGeometry(){ parts: BoxGeometry(1,0.25,1) translate y=0.125; BoxGeometry(0.7,0.25,0.7) translate y=0.375; BoxGeometry(0.4,0.25,0.4) translate y=0.625; ridge BoxGeometry(0.12,0.25,0.35) translate y=0.875. }
Good.

For castle roofs, could use same roofGeo with larger scale. But need curved layered roofs. Could manually add boxes with overhang. Could use roofGeo for each level. Good.

Need building data. Let's define town grid. Coordinates. Town area x from 26 to 70, z from 26 to 70. Roads: main road from gate at (0,0,24) to market at (35,35)? Could have road along z from 24 to 35 at x=0? But town east? Need town south/east. Maybe main road from gate south to z=35, then east to x=35. Market square at (35,35). Roads grid. Need buildings placed in blocks between roads.

Simpler: generate a grid of blocks with roads. Use cell size 6. Town from x=26 to 70, z=26 to 70. Roads at x multiples? Let's define road segments as thin boxes. Could generate building positions on grid cells, skip if on road. Need ensure 60-100. Could use loops.

Option: Use a grid of building plots. For x from 26 to 68 step 6, z from 26 to 68 step 6. If not road, place building. Roads: main road along z=30? Hmm.

Let's design town layout manually:
- Main road from gate: from (0,0,24) to (35,0,35)? Actually road width 4. Could place road segments along z axis x=0 from z=24 to 35, then along x axis z=35 from x=0 to 35. But town south/east, so main road L-shape. Market square at (35,35) size 10x10.
- Secondary roads: grid streets at x=45, z=45, etc.
- Narrow alleys: some smaller roads.

Could generate roads as boxes: addRoad(x,z,w,d). Use instanced road data. Need road segments. Could define road segments array. Then building placement: for each grid cell, if not on road and not market, place building. Need avoid water. Town outside water. Good.

Maybe easier: create a grid of blocks with roads as lines. Use function isRoad(x,z). But for building placement, we can iterate cells and skip if road. Need roads visual. Could create road segments as boxes for each road line. Use instanced boxes.

Let's define town bounds: xMin=26, xMax=70, zMin=26, zMax=70. Cell size 6. Building footprint max 4x4, height 2-4. Place at cell center. Roads: main road width 4: segment from x=0 to 35 at z=30? Wait gate at z=24, main road should connect to town. If main road along z from 24 to 30 at x=0, then east along x from 0 to 35 at z=30. Market at (35,30)? But town south/east. Let's set main road: from gate (0,24) to market (35,35). Could be diagonal? Voxel roads axis-aligned. Use L: z from 24 to 35 at x=0, x from 0 to 35 at z=35. But town starts x=26,z=26, so road at x=0 outside town? Gate at x=0,z=24, road south to z=35, then east to x=35. Town south/east includes x>26,z>26, so road segment x=0,z=24-35 is west of town, okay. Market at (35,35). Good.

But buildings should be south/east of castle. If town bounds x=26..70,z=26..70, main road at z=35 from x=0 to 35 passes through town? It enters at x=26,z=35. Good. Gate bridge from z=16 to 24 at x=0. Good.

Roads: main road width 4. Secondary roads: maybe at x=45, z=45, x=60, z=60. Narrow alleys: at x=32, z=32? Need not too many. Could define road segments as rectangles. For building placement, skip cells whose center lies within road rectangle. Need market square: rectangle x=30..40,z=30..40? But main road at z=35, market square maybe x=30..40,z=30..40, skip buildings. Add market stalls? Could add instanced boxes.

Need ensure building count 60-100. If grid 7x7=49 cells, too few. Use cell size 5, bounds 26..70 => 9x9=81, minus roads/market maybe 60. Good. Use step 5. Building footprint 3-4. Place at cell center. Need avoid roads. Could generate building data by iterating x=26 to 68 step 5, z=26 to 68 step 5. If not road/market, place. Count maybe 60. Need ensure at least 60. Could adjust. Let's estimate: 9x9=81. Roads: main road z=35 from x=0 to 35 affects cells z=35, x=26..35 maybe 2 cells. Secondary roads x=45, z=45, x=60, z=60: each line 9 cells, but intersections. Market 4 cells. Total skip maybe 30, leaving 51. Need more. Use step 4: 11x11=121, skip roads maybe 40, leaving 80. Good. Building footprint 2-3. Use step 4. Town bounds 26..70 step 4 => 11x11=121. Skip roads. Good.

Need roads visual: road segments as boxes. Could add main road, secondary, alleys. Use instanced road data. Need road width 3 or 4. For building placement, define road rectangles. Could use function onRoad(x,z) checks if within any road rectangle. But for building placement, if cell center on road skip. Need roads not too wide. Use road width 3. Main road width 4. Market square skip.

Define roadRects = [
 {x0:0,x1:35,z0:33,z1:37}, // main east-west at z=35
 {x0:-2,x1:2,z0:24,z1:37}, // main north-south from gate
 {x0:43,x1:47,z0:26,z1:70}, // secondary x=45
 {x0:26,x1:70,z0:43,z1:47}, // secondary z=45
 {x0:58,x1:62,z0:26,z1:70}, // x=60
 {x0:26,x1:70,z0:58,z1:62}, // z=60
 {x0:30,x1:34,z0:26,z1:70}, // alley x=32
 {x0:26,x1:70,z0:30,z1:34}, // alley z=32
]
But main road from gate at x=0,z=24 to z=37, then east z=35. Good. Market square maybe x=30..40,z=30..40? Overlaps roads. Could define market rect x=30..40,z=30..40 skip buildings. Add market stalls.

Need building placement: for x from 26 to 68 step 4, z from 26 to 68 step 4. If in market rect skip. If in any road rect skip. Else place building. Need ensure not too close to water. Good.

Building types: house, shop, warehouse. Use rng. For each building:
- width = rand(2,3.5), depth = rand(2,3.5), height = rand(1.8,3.5). Warehouse larger: width 4, depth 4, height 3.5.
- color: white, cream, light wood, dark wood. Use instance colors.
- roof height = rand(0.4,0.8). Roof color dark.
- position x,z, y=height.
Need maybe add dark trim? Could add a thin base box? Could use building body color. Maybe add dark wooden trim as instanced boxes? Could add a dark base/edge for each building: a box slightly larger at bottom? Could use same boxGeo with dark material, scale width+0.2, height 0.2, depth+0.2 at y=0.1. This adds detail. But count doubles. Could include as trim data. Use instanced mesh for trim with dark material. Good. For each building, add trim. Count maybe 80. Fine.

Need roofs: use roofGeo. For each building, roof instance at y=height, scale width, roofHeight, depth. Color dark. Could use same material. Need maybe roof orientation? Geometry symmetric. Good.

Need market stalls: instanced boxes with colors. Could add around market square. Maybe 12 stalls. Use building data? Could add as small boxes with roof? Could use building data with type market. Simpler: add market stall data as boxes and roofs. Use same building instanced? Could include in building data. But market square skip buildings, add stalls. Could add 12 small stalls with colors. Use building data? Need separate? Could just add to building data with small size and roof. Good.

Need trees. Generate 40-60 cherry. Need positions along moat and main road. Could create arrays. Need avoid roads/buildings? Could place along perimeter. Use seeded. For cherry along moat: positions around water ring outside outer boundary? Water outer 24. Place at radius 26-30 around center, but town bounds start 26. Could place along moat edge. For main road: along road sides. Need total. Could generate 45 cherry: 20 around moat, 15 along main road, 10 near market/shrine. Green trees 15. Total 60. Need ensure not inside water. Use positions manually.

Tree data: position, trunkHeight, foliageSize, color. Use instanced meshes. For cherry, foliage color pink variations. For green, green. Could use one foliage instanced with colors. Trunk instanced. Need count. Generate treeData.

Function addTree(x,z,type): trunk height, foliage size. For cherry, multiple foliage boxes? Could use one box. But cherry blossom trees should be pink, maybe multiple clusters. Could use foliage geometry as box. For better, use a custom tree geometry? Could use boxGeo. For cherry, maybe 3 foliage boxes per tree? Could use instanced foliage with multiple instances per tree. Need count. Could generate foliageData. For each tree, add trunk instance, and 1-3 foliage instances. Use same boxGeo. For green, one or two. Need total foliage count maybe 100. Fine.

Need tree placement avoid buildings. Could generate positions with simple loops and check distance to building positions? Could store building positions. But easier: generate along known lines, not on roads. Need avoid water. Use function canPlaceTree(x,z): if inside water ring? water outer 24, hole 14. If distance from center within 14? skip. If within road rects? skip. If within building positions? Could check min distance. We can store building positions and check. But building data generated first. Then tree generation can check. Good.

Generate building data first. Then tree data. Need road rects. Need market rect.

Tree generation:
- Cherry along moat: for angle 0 to 2π, positions at radius 26-28, but only south/east? Could all around. Need concentrated along moat. Use 20. For i 0..19, angle = i/20*2π + rng, r=26+rand(0,2), x=cos*r, z=sin*r. Check not road? Some may be on main road. Could skip if on road. But need count. Could just place if not on road and not building. If skip, try offset. Simpler: generate candidate, if canPlace add. Need ensure count. Could loop until count 20 with attempts.
- Cherry along main road: for t from 24 to 70 step 4, x=0? But town east. Main road L. Place on sides: for z=26..34, x=±3; for x=26..70, z=35±3. Add. Check canPlace.
- Green trees: random in town bounds, canPlace.
Need total 40-60. Could set target cherry 45, green 15. Use attempts.

Need canPlaceTree: if x,z within water outer? If inside water ring (between hole and outer) skip. If inside road rect skip. If inside market rect? maybe allow? Could skip. If distance to any building center < 3 skip. If distance to castle walls? skip. Use building positions array.

Need building positions array for check. Good.

Need shrine. Place at (-30,0,30) maybe west/south? Need torii, lanterns, fences. Could create group. Torii: two posts, two beams. Use boxes. Stone lanterns: instanced? Could add 4 lanterns around shrine. Use lantern data. Fences: instanced boxes around shrine. Could add. Need not conflict. Place shrine area x=-35..-25,z=25..35. But town south/east? It's west, okay. Could be near moat. Need torii facing path. Add path? Maybe road? Could add small road from town to shrine? Not necessary.

Need stone walls around castle. Could use instanced boxes? Could manually add boxes. Need gate. Let's design castle group.

Castle base:
- Ground grass plane large. Maybe add a hill: a box platform under castle. Use stone.
- Stone base: BoxGeometry(16,4,16) at y=2. Add stepped edges: boxes around. Could use addBox.
- Keep levels:
  level sizes [12,10,8,6,4], heights [4,3.5,3,2.5,2], y start 4. For each level: wall box white, dark trim at bottom/top, roof. Use addBox.
  Need roofs: for each level, roof overhang. Use roofGeo? Could add roof box dark with scale size+1, height 1.2, y top. For Japanese layered roofs, use multiple boxes. Could use addRoofLevel: base roof slab, second, third. Use roofGeo scaled. Good.
  Add dark wooden trim: thin boxes at corners? Could add a dark base under each level. Use addBox.
- Stone walls: around base perimeter. Use boxes. Wall height 3, thickness 1. Need gate opening at south. Could create wall segments: north, west, east, south with gap. Use addBox. Add corner towers? Could add small boxes.
- Main gate: at south wall gap x=-2..2, z=16. Add gate posts, roof. Use dark wood, white? Torii-like? Japanese gate. Add roof.
- Bridge: from z=16 to 24 at x=0. Use road/stone boxes. Add railings? Could add instanced? Simple.

Need water: ShapeGeometry with hole. Coordinates. Use ShapeGeometry. Need orient horizontal. ShapeGeometry lies in XY plane. Need rotate -PI/2. Create shape outer rectangle, hole. Use THREE.Shape. Coordinates in x,z? If shape in XY, then rotate. Let's define shape in x,y (where y will become z after rotation). Outer: x -24 to 24, y -24 to 24. Hole: x -14 to 14, y -14 to 14. ShapeGeometry. Rotate -PI/2 around x, so y becomes z? If shape XY, rotate -PI/2: x stays x, y -> -z? Let's test: rotation -90 about x: (x,y,0) -> (x,0,-y). So z = -y. If we want z positive south, use y negative? Could just set shape y coordinates as z? Simpler: create shape in XZ? ShapeGeometry uses XY. Could rotate -PI/2 and set position y=0.05. If shape y coordinate corresponds to -z. We can define shape y from -24 to 24, then z = -y. It's symmetric, no issue. Hole symmetric. Good.

Need water plane maybe double-sided? MeshStandardMaterial side DoubleSide? ShapeGeometry normal? If rotated, normal maybe -Y? Could set side DoubleSide. Good.

Need ground plane: PlaneGeometry(200,200), rotate -PI/2, receiveShadow. Grass. Could add terrain hill under castle: a few boxes. Use addBox.

Need roads: Instanced road segments. Use boxGeo. For each road rect, create one instance scaled to rect width, height 0.05, depth. But road rects as rectangles. Use roadData. Need maybe road height 0.06. Use material road. Could add road segments as boxes. Good.

Need market stalls: Could add as building data? If building data includes market stalls, they will have roofs. Good. But market square skip buildings, add stalls around perimeter. Use building data with small sizes. Need ensure count. Could add 12 stalls. Good.

Need fences: around shrine and maybe town. Use instanced boxes. Could generate fence data around shrine perimeter. Use boxGeo. Count maybe 20.

Need lanterns: around shrine, market, gate. Use instanced boxes. Could use a lantern geometry? Use boxGeo. Maybe add small top. Could use same boxGeo. For stone lanterns, use a geometry with base, post, top? Could create lanternGeo merged. Use instanced. Good.

Need torii: unique group. Use boxes. Could use torii material. Add to scene.

Need maybe add castle details: dark wooden trim, windows? Could add instanced windows? Maybe not. But need white walls dark wooden trim. Could add dark trim boxes around each level. Use addBox.

Need ensure shadows. For instanced meshes, castShadow true. For ground, receive. For water, maybe not cast. For roofs, cast. For trees, cast.

Need animation loop: controls.update, maybe water shimmer? Could animate point lights? Could animate foliage? Not necessary. But maybe water slight opacity? Could animate sun? No.

Need toggle day/dusk: button and key. Need UI. Use div. Button. Key 'd'. Could also 't'. Need prevent default? Fine.

Need handle resize.

Potential issue: BufferGeometryUtils.mergeGeometries requires geometries have same attributes. BoxGeometry all have position, normal, uv. Good. If we translate, okay. Need import BufferGeometryUtils. In three 0.170, mergeGeometries exported. Good.

Potential issue: InstancedMesh with geometry roofGeo, material roof. setColorAt? If material color fixed, no need. For building body, use material white with instance colors. Need create InstancedMesh with count. For each instance, set matrix. Need dummy = new THREE.Object3D(); dummy.position.set; dummy.scale.set; dummy.updateMatrix(); mesh.setMatrixAt(i, dummy.matrix); mesh.setColorAt(i, color). Need after loop mesh.instanceMatrix.needsUpdate = true; mesh.instanceColor.needsUpdate = true. If no colors? For building, set colors. For foliage, set colors. For road, no colors. For lantern, no colors. For fence, no colors.

Need if count zero? Avoid. But counts >0.

Need building data arrays. Let's define:
const buildingData = [];
const roadData = [];
const treeData = []; // {x,z,trunkH,foliageSize,color,type}
const foliageData = []; // {x,y,z,size,color}
const trunkData = [];
const lanternData = [];
const fenceData = [];

But for trees, could store trunk and foliage separately. For each tree, add trunk instance and foliage instances. Need colors. Use foliageData with color. TrunkData no color.

Building data: {x,y,z,w,h,d,color,roofW,roofH,roofD,roofColor}. For trim: {x,y,z,w,h,d}. Could use separate trimData.

Need road data: {x,y,z,w,h,d}. Use y=0.03.

Need market stalls: buildingData with small. Could add.

Need building placement. Let's define roadRects. Need function inRect(x,z,rect). For building cell center. Need roadData from rects. For each rect, add road instance. But road rects may overlap. Fine.

Town bounds. Let's choose:
const townMinX=26, townMaxX=70, townMinZ=26, townMaxZ=70;
const step=4;
const marketRect = {x0:30,x1:40,z0:30,z1:40};
const roadRects = [
 {x0:-2,x1:2,z0:24,z1:37}, // gate approach
 {x0:0,x1:35,z0:33,z1:37}, // main road east
 {x0:43,x1:47,z0:26,z1:70}, // secondary x=45
 {x0:26,x1:70,z0:43,z1:47}, // secondary z=45
 {x0:58,x1:62,z0:26,z1:70}, // x=60
 {x0:26,x1:70,z0:58,z1:62}, // z=60
 {x0:30,x1:34,z0:26,z1:70}, // alley x=32
 {x0:26,x1:70,z0:30,z1:34}, // alley z=32
];
But main road east from x=0 to 35 at z=35, and gate approach x=0,z=24-37. Good. Market rect overlaps main road and alley. Skip buildings. Add stalls around market. Need roadData includes market square? Could add market square as road/plaza. Use roadData for market rect too. Good.

Building placement: for x=townMinX; x<=townMaxX; x+=step, for z... center. If in marketRect skip. If in any roadRect skip. Else place. Need ensure building not too close to road? Cell center on road skip. Building footprint 2-3, could overlap road if center near edge. But okay. Could check distance to road rect > 1.5. Use function nearRoad. Simpler: if inRect with expanded margin 1.5 skip. Good.

Need building count. Let's estimate with step 4, bounds 26..70 inclusive: 11x11=121. Road rects expanded margin 1.5. Many skip. Could be ~70. Good. Need at least 60. If too few, can add more by step 3? But code can't know. Could ensure by generating until count 70? Could use step 3.5? Let's choose step=3.5? Grid 13x13=169, skip maybe 80, leaving 89. Good. But building footprint 2-3, step 3.5 okay. Use step=3.5. Count maybe 80. Good. Need not too dense. Use step=3.5.

Building sizes: house w=2.2-3.2, d=2.2-3.2, h=2-3.5. Warehouse w=3.5-4.5, d=3.5-4.5, h=3-4.5. Shop w=2-3, h=2-3. Need colors. Use palette:
const buildingColors = [0xf5f2ea, 0xe8e2d6, 0xd9c7b0, 0xb8a894, 0x8b7a66, 0x6f5f50, 0x4f4438];
For white walls, mostly light. For dark wood, some. Use rng.

Roof colors: dark gray, dark brown. Use palette [0x2f3a44, 0x3b3f45, 0x4a4f55, 0x35302a]. For instanced roof, set colors.

Trim: dark wood. Add trimData for each building: base box w+0.2, h=0.15, d+0.2 at y=0.075. Maybe top trim? Could add dark eave? Roof already. Good.

Need building body material with instance colors. If material color white, setColorAt. Good.

Need roof material with instance colors. Use roofGeo. For each building, roof instance at y=h, scale w, roofH, d. Color roof. Need roofH = rand(0.4,0.8). If roofGeo height 1, scale y=roofH. Good.

Need market stalls: add buildingData with small w=1.5,d=1.5,h=1.2, roofH=0.3, colors bright. Place around market rect perimeter. Could add 12. Need not on roads? Could place inside market. Good.

Need shrine. Could add group. Need torii. Use addBox helper. But addBox uses material. Need create group. Could use helper addBox(parent, x,y,z,w,h,d, material). For torii, use torii material. Add posts, beams. Add shrine building: small white building with dark roof. Add stone lanterns via lanternData. Add fenceData around shrine. Add path? Could add road rect from town to shrine? Maybe not.

Need lantern geometry. Could create lanternGeo: base box, post, top. Use merged. For instanced lanterns, use lanternGeo. Data positions. Add around shrine, market, gate. Use material lantern. Could add small light? Not necessary. For dusk, point lights maybe at lanterns? Could add a few point lights at lantern positions. But point lights count. Could add 4. Good.

Need fence geometry: boxGeo. Data positions. Add around shrine. Could also fences along roads? Maybe. Use fenceData.

Need torii unique. Could use group with boxes. Need maybe torii at shrine entrance. Add.

Need castle helper. Could use addBox. Need ensure materials. For castle, use white, dark wood, stone, roof. Could add windows? Maybe dark boxes. Could add dark trim. Use addBox.

Need maybe use merged geometry for castle? Not necessary. But many boxes maybe 100. Fine.

Need performance: InstancedMesh for repeated. Castle group maybe 100 meshes, okay. Town buildings instanced. Trees instanced. Roads instanced. Lanterns instanced. Fences instanced. Good.

Need shadows: For InstancedMesh, castShadow true. For ground, receive. For castle meshes, cast/receive. For water, maybe receive? Could set receive false. For roofs, cast. For trees, cast. For roads, receive. For fences, cast.

Need camera view. Default target maybe (20,8,20) to include town and castle. But castle focal. Could target (10,8,10). Camera position (70,45,80). Good.

Need UI: top-left panel with button. Use CSS.

Need code structure. Need ensure no syntax errors. Let's draft.

Potential issue: import map and module script. Need no top-level await. Good.

Potential issue: THREE.ShapeGeometry with hole. Need shape.holes.push(hole). ShapeGeometry. Good.

Potential issue: ShapeGeometry normals. If rotate -PI/2, normal maybe up? Let's check: ShapeGeometry in XY plane, normal +Z. Rotate -PI/2 about X: +Z -> -Y? Actually rotation -90 about X: z axis -> -y? Vector (0,0,1) -> (0,1? Let's compute: Rx(-90): y' = y cos - z sin? For -90, cos=0, sin=-1. y' = y*0 - z*(-1)=z; z'= y*sin + z*cos = -y. So normal +Z -> +Y. Good. So normal up. Good.

Need water position y=0.04. ShapeGeometry coordinates x,y. After rotation, y coordinate becomes z? If point (x,y,0) -> (x,0,-y). So shape y positive maps to z negative. Symmetric. Good.

Need ground plane. PlaneGeometry normal +Z, rotate -PI/2 -> +Y. Good.

Need shadow camera. Sun position. Shadow bounds cover town. Use left=-100, right=100, top=100, bottom=-100. Good.

Need day/dusk. Need scene.fog. Use fog = new THREE.Fog(0xd8e7f5, 80, 220); scene.fog = fog. In setLighting, update fog.color, near, far. Need scene.background. Use renderer.setClearColor? scene.background. Good.

Need point lights. Create array. For dusk, intensity. For day, 0. Could add point lights at lantern positions? Maybe 4 fixed. Use warm. Add to scene. Could not cast shadows. Good.

Need maybe animate water? Could add simple water plane with opacity. Could animate by moving? Not needed. But maybe add slight sine to water material? No.

Need ensure seeded random layout same. Use rng. Good.

Need maybe use flatShading true. For InstancedMesh with flatShading, okay.

Need maybe use MeshStandardMaterial with vertexColors? For instanceColor, I think no need. But to be safe, set material.vertexColors = true? If vertexColors true, material expects vertex colors attribute; instanceColor is separate. In three, instanceColor works regardless? Let's recall: In InstancedMesh, if instanceColor is set, renderer uses it. Material.vertexColors maybe not required. But if vertexColors true and no vertex color attribute, maybe black? Actually material.vertexColors = true means use vertex colors from geometry. If geometry lacks color attribute, maybe default? Could cause issues. Better not set vertexColors. InstancedMesh instanceColor is supported. I think okay.

Need maybe use setColorAt before instanceColor exists? InstancedMesh constructor creates instanceColor? In three, instanceColor is null until setColorAt called. setColorAt creates. Good.

Need create InstancedMesh with count. If count 0, skip. But counts >0.

Need dummy object. For each data, set. Need color = new THREE.Color(hex). For building colors, use setColorAt. For roof colors. For foliage colors. For road no color. For lantern no color. For fence no color.

Need maybe use same boxGeo for all instanced. Good.

Need create helper createInstancedMesh(data, geometry, material, useColors). But data includes color. Could implement.

Pseudo:
function buildInstanced(data, geometry, material, colorData=false) {
 if (!data.length) return null;
 const mesh = new THREE.InstancedMesh(geometry, material, data.length);
 const dummy = new THREE.Object3D();
 const color = new THREE.Color();
 data.forEach((d,i)=>{
   dummy.position.set(d.x,d.y,d.z);
   dummy.scale.set(d.w,d.h,d.d);
   dummy.rotation.set(d.rx||0,d.ry||0,d.rz||0);
   dummy.updateMatrix();
   mesh.setMatrixAt(i, dummy.matrix);
   if (colorData) { color.setHex(d.color); mesh.setColorAt(i, color); }
 });
 mesh.instanceMatrix.needsUpdate = true;
 if (colorData) mesh.instanceColor.needsUpdate = true;
 mesh.castShadow = true; mesh.receiveShadow = true;
 scene.add(mesh);
 return mesh;
}

Need data fields. For road, y=0.03, h=0.06. For building, y=h/2? If boxGeo centered, position should be center. For building body, position y = h/2. For trim, y=0.075. For roof, roofGeo geometry extends from y=0 to 1, so position y = h (base at top). If geometry centered? We made roof geometry with parts translated from y=0 to 1, not centered. So position y = building height. Good. For tree trunk, if using boxGeo centered, position y = trunkH/2. For foliage, position y = trunkH + foliageSize/2. Good. For lantern, if geometry centered? Could make lanternGeo centered? Let's make lanternGeo with parts from y=0 to 1, position y=0. Then scale. Or use boxGeo centered. Simpler: use boxGeo for lanterns, position y = height/2. But lantern detail? Could create lanternGeo normalized height 1, base at 0. Then position y=0. Use scale. Good.

Need lanternGeo:
function makeLanternGeometry(){ parts: base Box(0.4,0.15,0.4) y=0.075; post Box(0.15,0.35,0.15) y=0.325; top Box(0.35,0.15,0.35) y=0.575; cap Box(0.25,0.1,0.25) y=0.725; } total height 0.8. Could normalize? Not necessary. Use position y=0, scale. Good.

Need fence geometry: boxGeo. Data position y=height/2.

Need torii geometry? Could use boxes. Unique.

Need castle addBox. Use group. For addBox, create mesh with boxGeo? But boxGeo is unit. Need scale. Could create mesh = new THREE.Mesh(boxGeo, material); mesh.scale.set(w,h,d); mesh.position.set(x,y,z); mesh.castShadow=true; mesh.receiveShadow=true; group.add(mesh). This reuses geometry. Good. Many meshes but okay. Could use same boxGeo. For roofs, use roofGeo. For castle roofs, use roofGeo scaled. Good.

Need maybe addBox with y center. For castle levels, wall box center y = baseY + h/2. Roof base at top, position y = topY. Good.

Need castle details. Let's define castle group.

Ground:
const ground = new THREE.Mesh(new THREE.PlaneGeometry(200,200), matGrass); ground.rotation.x=-Math.PI/2; ground.receiveShadow=true; scene.add(ground);

Castle base hill:
addBox(castleGroup, 0, 1.5, 0, 18, 3, 18, matStone); // center y=1.5 height 3
addBox(castleGroup, 0, 3.5, 0, 14, 1, 14, matStone); // top platform? Actually base height 4. Let's do base height 4: main box center y=2, size 16x4x16. Add steps: lower box 18x1x18 y=0.5, upper 14x1x14 y=3.5. Good.

Stone walls:
Wall height 3, thickness 1, perimeter 16? If base 16, walls at x=±8? Need around base. Let's set wall ring at x=-8..8, z=-8..8, height 3, thickness 1, base y=0? But castle base already. Maybe walls around base at perimeter, height 3, from y=0 to 3. Use addBox. Gate gap at south z=8, x=-2..2. Wall segments:
- north: x=0,z=-8,w=16,h=3,d=1 center y=1.5
- west: x=-8,z=0,w=1,h=3,d=16
- east: x=8,z=0,w=1,h=3,d=16
- south left: x=-5,z=8,w=6,h=3,d=1
- south right: x=5,z=8,w=6,h=3,d=1
Add corner posts. Add gate.

But water hole 14, outer 24. Walls at 8, water from 14 to 24. There is land between wall and water? Maybe okay. Bridge from gate z=8 to water outer 24? Need bridge across water from z=14 to 24? If wall at z=8, gate at z=8, bridge should cross water from z=14 to 24. But there is gap from wall to water. Could make water hole 10, outer 24, walls at 12? Let's adjust.

Better: castle base footprint 14x14, walls at perimeter 14, water hole 16, outer 24. Bridge from wall gate z=14 to water outer z=24. Town starts 26. Good. Let's define:
- Castle base: 14x4x14, top y=4.
- Stone walls: perimeter 14, height 3, thickness 1, at x=±7,z=±7. Gate gap at south z=7, x=-2..2.
- Water hole: x=-10..10,z=-10..10? If walls at 7, water from 10 to 24. Bridge from z=10 to 24? Gate at z=7, need bridge from z=7 to 10 land? Could add bridge from z=7 to 24 crossing water. Good.
- Town starts z=26. Good.

But water hole 10, outer 24. Shape hole 10. Good. Castle base 14, walls 14. Water ring from 10 to 24. Bridge from gate z=7 to z=24. Good.

Need main gate at z=7. Bridge from z=7 to 24 at x=0. Add road from z=24 to 37. Good.

Need town bounds start 26. Good.

Need trees along moat: water outer 24, place at radius 26-28. Good.

Need castle keep levels on base top y=4. Level sizes: 12,10,8,6,4. But base 14. Level 1 12. Good. Heights: 4,3.5,3,2.5,2. Total top y=17. Roofs. Good.

Need dark wooden trim: for each level, add dark base box slightly larger at bottom, maybe top. Use addBox. For white walls, add dark corner posts? Could add dark trim at corners: four boxes. But maybe too many. Could add a dark band at bottom and top. Use addBox.

Castle roof: For each level, use roofGeo scaled to size+1, height 1.2, position top. But roofGeo normalized height 1. Use scale (size+1, 1.2, size+1). Good. Add dark roof. For top level, maybe smaller. Add ridge. Good.

Need maybe add castle windows: dark boxes on walls. Could add instanced? Could manually add a few. Use dark material. For each level, add small boxes on sides. Could add 4 per level. Good. Use addBox.

Need main gate: gate posts at x=±2, z=7, height 3, width 0.5, depth 1. Roof dark. Add gate roof. Use addBox. Add bridge: road box from z=7 to 24, width 3, height 0.1, at y=0.05. Add railings: boxes along sides. Could use fenceData? Add manually.

Need stone lanterns: add around gate, market, shrine. Use lanternData. Need positions. Could add 6.

Need shrine: group. Place at (-30,0,30). Add small building: white walls, dark roof. Torii at (-30,0,24)? Need torii facing path. Add torii group. Add fence around shrine. Add lanterns. Add path? Could add road rect from town to shrine? Maybe not. But can add a small road from market to shrine? Could add roadData. Need not.

Need torii: posts at x=-30±1.5, z=24, height 3, width 0.3, depth 0.3. Top beam, second beam. Use torii material. Add.

Need fences: around shrine perimeter x=-34..-26,z=26..34. Use fenceData. Could add 16 posts. Use boxGeo. Data positions. Good.

Need market stalls: add buildingData. Need colors. Could add around market rect. For i, positions. Use buildingData. Need ensure not on roads? Market rect skip buildings, but stalls inside. Good. Add 12 stalls. Use small sizes. Could add roofs. Good.

Need roads: roadData from roadRects plus market rect. Need maybe add road from gate to shrine? Could add road rect x=-30..-2,z=30..34? But town? Could add. Let's add a small road from shrine to main road: {x0:-30,x1:0,z0:33,z1:37}? But main road already z=33-37 from x=0 to 35. Extend west to -30. Good. Add road rect. Then shrine path. Good.

Need building placement skip roads. If road extends west, town bounds unaffected. Good.

Need tree canPlace check road rects. Good.

Need building positions for tree check. Store buildingData positions. But buildingData includes market stalls. Good.

Need tree generation. Let's implement robust.

Data arrays:
const buildingData = [];
const trimData = [];
const roofData = [];
const roadData = [];
const trunkData = [];
const foliageData = [];
const lanternData = [];
const fenceData = [];

Need addBuilding(x,z,type). It pushes buildingData, trimData, roofData. Need buildingPositions array for tree check. Could use buildingData positions. But buildingData y center. For check, use x,z.

Function addBuilding(x,z, opts):
 const w=opts.w, d=opts.d, h=opts.h, color=opts.color, roofColor=opts.roofColor, roofH=opts.roofH;
 buildingData.push({x,y:h/2,z,w,h,d,color});
 buildingPositions.push({x,z});
 trimData.push({x,y:0.08,z,w:w+0.2,h:0.16,d:d+0.2});
 roofData.push({x,y:h,z,w:w, h:roofH, d:d, color:roofColor});

Need roofData color. Good.

Building placement:
for x=townMinX; x<=townMaxX; x+=3.5:
 for z=townMinZ; z<=townMaxZ; z+=3.5:
   if (inRect(x,z,marketRect)) continue;
   if (nearRoad(x,z,1.5)) continue;
   // maybe skip if too close to castle? town bounds okay.
   const type = rng()<0.6?'house':rng()<0.7?'shop':'warehouse';
   let w,d,h;
   if type warehouse: w=rand(3.2,4.2), d=rand(3.2,4.2), h=rand(3.0,4.2);
   else if shop: w=rand(2.0,3.0), d=rand(2.0,3.0), h=rand(2.0,3.2);
   else: w=rand(2.2,3.2), d=rand(2.2,3.2), h=rand(1.8,3.0);
   color = pick buildingColors; roofColor = pick roofColors; roofH = rand(0.4,0.7);
   addBuilding(x,z,...)
Need ensure count. Could if buildingData.length < 60, add more? Could adjust step. But likely. Could add a fallback: if count < 60, loop random positions in town bounds not road/market, add. But need avoid duplicates. Could do after grid, while buildingData.length < 65, random x,z, check canPlaceBuilding. Need canPlaceBuilding function. Good.

Function canPlaceBuilding(x,z): if inside water ring? town bounds outside. If in market? maybe no. If nearRoad skip. If distance to existing building positions < 3.5 skip. If distance to shrine? skip. Good.

But buildingData already includes. Use buildingPositions.

Need market stalls: add 12 around market. Use addBuilding with small. Need positions not on roads? Market rect includes roads, but stalls can be inside. Use positions around perimeter. Could add manually.

Need roadData: for each roadRect, add road instance. Use roadData.push({x:(x0+x1)/2, y:0.03, z:(z0+z1)/2, w:x1-x0, h:0.06, d:z1-z0}); Good.

Need nearRoad function: for rect, if x > rect.x0-margin && x < rect.x1+margin && z > rect.z0-margin && z < rect.z1+margin. Good.

Need inRect. Good.

Need tree generation. Use buildingPositions. Need roadRects. Need water ring. Function canPlaceTree(x,z):
 if (x*x+z*z < 10*10) return false; // inside castle hole? Actually water hole 10, but trees can be on land inside? Castle base. skip.
 if (x*x+z*z > 24*24) maybe okay? But water ring only within outer 24. If outside, okay. If inside water ring: if x between -24 and 24 and z between -24 and 24 and outside hole? Need check. Use if (Math.abs(x)<=24 && Math.abs(z)<=24 && (Math.abs(x)>10 || Math.abs(z)>10)) return false; // water ring. But if x=25,z=0 outside. Good.
 if (nearRoad(x,z,1.5)) return false;
 if (inRect(x,z,marketRect)) return false;
 for bp in buildingPositions: if dist < 3.5 return false;
 if (x < -34 || x > 70 || z < -34 || z > 70) return false;
 return true;

Need generate cherry. Use target counts. Could use attempts.
function addTree(x,z,type){
 const trunkH = type==='cherry'? rand(2.5,3.5) : rand(2.0,3.0);
 const foliageSize = type==='cherry'? rand(1.8,2.6) : rand(1.5,2.2);
 trunkData.push({x,y:trunkH/2,z,w:0.35,h:trunkH,d:0.35});
 // foliage clusters
 const clusters = type==='cherry'? randInt(2,4) : randInt(1,3);
 for i=0;i<clusters;i++:
   const fx = x + rand(-1,1)*foliageSize*0.4;
   const fz = z + rand(-1,1)*foliageSize*0.4;
   const fy = trunkH + rand(0.5,1.2)*foliageSize;
   const size = foliageSize * rand(0.6,1.0);
   const color = type==='cherry' ? pick cherryColors : pick greenColors;
   foliageData.push({x:fx,y:fy,z:fz,w:size,h:size*rand(0.7,1.0),d:size,color});
}

Cherry colors: [0xf7c6d8, 0xf2b7c8, 0xe8a3b8, 0xf5d0e0, 0xd98ba0]. Green: [0x6fa86a, 0x7fb87a, 0x5f9a5f, 0x86b878].

Generate cherry along moat:
let cherryCount=0;
for attempts=0; attempts<200 && cherryCount<22; attempts++:
 angle = rng()*Math.PI*2; r = 26 + rng()*3; x=Math.cos(angle)*r; z=Math.sin(angle)*r; if canPlaceTree add, cherryCount++.
But canPlaceTree may skip if near building? Town bounds start 26, so okay. Need not inside water. Good.

Cherry along main road:
for z=24; z<=34; z+=3: for side x=±3, if canPlace add.
for x=26; x<=70; x+=3: for side z=35±3, if canPlace add.
Need count target 45. Could add until 45. Use attempts.

Green trees:
for attempts while greenCount<15: x=rand(26,70), z=rand(26,70), if canPlace add.
Total maybe 45+15=60. Good.

Need ensure cherry count 40-60. If canPlace too strict, maybe less. Could set target 45, attempts 500. Good.

Need lanterns. Add positions:
- gate: (0,0,12), (0,0,20)? But water? Lantern on bridge? Could place at bridge sides z=12,20. Good.
- market: (32,0,32), (38,0,32), (32,0,38), (38,0,38) maybe inside market. Good.
- shrine: (-30,0,26), (-30,0,34), (-34,0,30), (-26,0,30). Good.
Need lanternData with height 0.8. Use addLantern(x,z). Data {x,y:0,z,w:1,h:0.8,d:1}? If lanternGeo normalized height 1, scale. Use scale 1. Good.

Need fences. Around shrine: for x=-34..-26 step 1, z=26 and z=34; for z=26..34 step 1, x=-34 and x=-26. Add fenceData. Need avoid torii? Could skip near torii. Use simple. Fence height 1.2, thickness 0.2. Data {x,y:0.6,z,w:0.2,h:1.2,d:0.2}. Could add rails? Not necessary. Could add fence posts. Good.

Need torii. Add group. Use addBox. Torii at (-30,0,24). Posts: x=-31.5,z=24, x=-28.5,z=24, height 3, width 0.3, depth 0.3. Top beam: x=-30,y=3,z=24,w=4,h=0.3,d=0.3. Second beam: y=2.4,w=3.5,h=0.25,d=0.25. Maybe top cap. Good.

Need shrine building: at (-30,0,30). Add small building: white box w=3,h=2,d=3, roof dark. Use addBox. Add dark trim. Could use addBuilding? But addBuilding pushes instanced. Could use group. Simpler: addBox. Add roof using roofGeo? Could add mesh. Use addBox with roofGeo? addBox uses boxGeo. Could create addRoofMesh. But for shrine, can use addBox for roof. Good.

Need maybe add stone lanterns as instanced. Good.

Need point lights for dusk. Create at lantern positions? Could create 4:
const pointLights = [];
function addPointLight(x,y,z){ const l = new THREE.PointLight(0xffd28a, 0, 25); l.position.set(x,y,z); scene.add(l); pointLights.push(l); }
Add at gate bridge z=12, market (35,2,35), shrine (-30,2,30), town (-30,2,30)? Maybe. For dusk, intensity 1.5. For day, 0. Could add 5. Good.

Need maybe add castle lights? Not.

Need animation loop. Could animate water? Maybe simple: water material opacity? Could animate point lights intensity flicker? Not necessary. But maybe add slight water shimmer by rotating? No.

Need ensure renderer uses import map. Good.

Potential issue: BufferGeometryUtils.mergeGeometries returns geometry with groups? Fine.

Potential issue: InstancedMesh with roofGeo and material roof. If roofGeo has multiple boxes, flatShading. Good.

Potential issue: InstancedMesh with boxGeo and material with instance colors. If material flatShading true, okay.

Potential issue: shadows with InstancedMesh. Good.

Potential issue: UI button. Need add event listener. Key 'd'. Could use document.addEventListener('keydown', e => { if (e.key.toLowerCase()==='d') toggle(); });

Need maybe toggle button text. Good.

Need final code block only. Need no explanation. Need ensure complete.

Let's think through code details.

HTML structure:
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Voxel Japanese Castle Town</title>
<style>
html,body{margin:0;height:100%;overflow:hidden;background:#d8e7f5;font-family:system-ui,sans-serif;}
canvas{display:block;}
#ui{position:fixed;top:12px;left:12px;padding:10px 12px;background:rgba(255,255,255,0.75);border-radius:10px;box-shadow:0 4px 12px rgba(0,0,0,0.15);font-size:14px;}
#ui button{margin-top:6px;padding:6px 10px;border:0;border-radius:6px;background:#2f3a44;color:#fff;cursor:pointer;}
#ui small{display:block;color:#444;margin-top:4px;}
</style>
<script type="importmap">
{
  "imports": {
    "three": "https://cdn.jsdelivr.net/npm/three@0.170.0/build/three.module.js",
    "three/addons/": "https://cdn.jsdelivr.net/npm/three@0.170.0/examples/jsm/"
  }
}
</script>
</head>
<body>
<div id="ui">
  <strong>Voxel Castle Town</strong>
  <button id="toggle">Toggle Day / Dusk</button>
  <small>Press D to toggle. Orbit, zoom, pan.</small>
</div>
<script type="module">
...
</script>
</body>
</html>

Need ensure import map before module. Good.

JS code. Need maybe use THREE.ColorManagement? Not necessary.

Let's write code carefully.

Potential issue: In module script, top-level const. Good.

Need define rng. Good.

Need materials. Good.

Need helper addBox. But addBox uses boxGeo. For castle group. Need ensure boxGeo defined. Good.

Need create roofGeo before addRoof? Good.

Need create lanternGeo. Good.

Need create water. Good.

Need create town data before instanced meshes. Good.

Need create castle group. Could create before town. Good.

Need order:
- rng
- scene, camera, renderer, controls
- lights, fog, point lights
- materials, geometries
- helper functions
- build castle
- build town data
- build trees
- build props
- create instanced meshes
- setLighting
- animate

Need ensure helper functions use scene. Good.

Potential issue: addBox for castle uses material. If material is instanced? No.

Potential issue: addBox with roofGeo? Could define addRoofMesh(parent, x,y,z,w,h,d, material). Use roofGeo. Good.

Potential issue: addBox with boxGeo unit, scale. Good.

Potential issue: addBox position y center. For roofGeo, position base. Good.

Need castle details. Let's write.

const castle = new THREE.Group(); scene.add(castle);
function addBox(parent, x, y, z, w, h, d, material) { const mesh = new THREE.Mesh(boxGeo, material); mesh.scale.set(w,h,d); mesh.position.set(x,y,z); mesh.castShadow=true; mesh.receiveShadow=true; parent.add(mesh); return mesh; }
function addRoof(parent, x, y, z, w, h, d, material) { const mesh = new THREE.Mesh(roofGeo, material); mesh.scale.set(w,h,d); mesh.position.set(x,y,z); mesh.castShadow=true; mesh.receiveShadow=true; parent.add(mesh); return mesh; }

Castle base:
addBox(castle, 0, 0.5, 0, 18, 1, 18, matStone);
addBox(castle, 0, 2, 0, 14, 3, 14, matStone); // center y=2 height 3, top y=3.5? Need top y=4. Let's do height 4 center y=2. top 4. Add lower step height 1 center 0.5. Good.
addBox(castle, 0, 3.5, 0, 12, 1, 12, matStone); // top platform? If main base top y=4, adding center 3.5 height 1 top 4. Good.

Stone walls:
const wallY=1.5, wallH=3, wallT=1;
addBox(castle, 0, wallY, -7, 14, wallH, wallT, matStone);
addBox(castle, -7, wallY, 0, wallT, wallH, 14, matStone);
addBox(castle, 7, wallY, 0, wallT, wallH, 14, matStone);
addBox(castle, -5, wallY, 7, 4, wallH, wallT, matStone); // left segment from x=-7 to -3? center -5 width4
addBox(castle, 5, wallY, 7, 4, wallH, wallT, matStone);
// corner posts
addBox(castle, -7, wallY, -7, 1.5, wallH+0.5, 1.5, matStone); etc. Could add.
Gate:
addBox(castle, -2, 1.5, 7, 0.5, 3, 1, matDarkWood);
addBox(castle, 2, 1.5, 7, 0.5, 3, 1, matDarkWood);
addBox(castle, 0, 3.2, 7, 5, 0.4, 1.2, matDarkWood); // beam
addRoof(castle, 0, 3.6, 7, 5, 0.8, 1.2, matRoof); // gate roof
Bridge:
addBox(castle, 0, 0.05, 15, 3, 0.1, 10, matRoad); // from z=10 to 20? center z=15 length10. Need from gate z=7 to water outer 24. Could center z=15.5 length17. But water from 10 to 24. Bridge should cross water. Add from z=7 to 24: center z=15.5, length17. Use matRoad. Add railings: boxes along sides. Use addBox.
addBox(castle, -1.5, 0.5, 15.5, 0.2, 1, 17, matFence);
addBox(castle, 1.5, 0.5, 15.5, 0.2, 1, 17, matFence);
Maybe bridge piers? Not.

Castle keep levels:
const levels = [
 {size:12, height:4},
 {size:10, height:3.5},
 {size:8, height:3},
 {size:6, height:2.5},
 {size:4, height:2}
];
let y=4;
for level:
  addBox(castle, 0, y+level.height/2, 0, level.size, level.height, level.size, matWhite);
  // dark trim bottom
  addBox(castle, 0, y+0.15, 0, level.size+0.4, 0.3, level.size+0.4, matDarkWood);
  // dark trim top
  addBox(castle, 0, y+level.height-0.15, 0, level.size+0.3, 0.3, level.size+0.3, matDarkWood);
  // corner posts
  for sx [-1,1], sz [-1,1]: addBox(castle, sx*(level.size/2-0.2), y+level.height/2, sz*(level.size/2-0.2), 0.4, level.height, 0.4, matDarkWood);
  // windows: maybe dark boxes on sides. For each side, add small boxes. Could add 4 windows per level. Use addBox.
  for side: addBox(castle, level.size/2+0.01, y+level.height/2, 0, 0.1, 0.8, 1.2, matDarkWood); etc. But if level size small. Could add.
  // roof
  addRoof(castle, 0, y+level.height, 0, level.size+1.2, 1.2, level.size+1.2, matRoof);
  y += level.height;
Need top roof. Good.

But addRoof geometry normalized height 1, scale y=1.2. Good.

Need maybe add dark roof ridge? roofGeo includes ridge. Good.

Need water:
const waterShape = new THREE.Shape();
waterShape.moveTo(-24,-24); waterShape.lineTo(24,-24); waterShape.lineTo(24,24); waterShape.lineTo(-24,24); waterShape.lineTo(-24,-24);
const hole = new THREE.Path(); hole.moveTo(-10,-10); hole.lineTo(10,-10); hole.lineTo(10,10); hole.lineTo(-10,10); hole.lineTo(-10,-10); waterShape.holes.push(hole);
const waterGeo = new THREE.ShapeGeometry(waterShape);
const water = new THREE.Mesh(waterGeo, matWater); water.rotation.x=-Math.PI/2; water.position.y=0.04; water.receiveShadow=true; scene.add(water);
Need maybe water side DoubleSide. Set matWater.side = THREE.DoubleSide. Good.

Need ground plane. Good.

Need town data. Need roadRects. Need add roadData. Need buildingData. Need buildingPositions.

Define roadRects before building placement. Need include market rect? Could define marketRect separately. roadRects includes market? For roadData, add market as plaza. For nearRoad, use roadRects. If marketRect not in roadRects, building skip separately. Could add marketRect to roadRects for roadData and nearRoad. But then building skip if in market. Good. Let's define roadRects includes market. But nearRoad margin may skip buildings near market. Good.

roadRects = [
 {x0:-30,x1:0,z0:33,z1:37}, // shrine path/main road west
 {x0:0,x1:35,z0:33,z1:37}, // main road east
 {x0:-2,x1:2,z0:24,z1:37}, // gate approach
 {x0:43,x1:47,z0:26,z1:70},
 {x0:26,x1:70,z0:43,z1:47},
 {x0:58,x1:62,z0:26,z1:70},
 {x0:26,x1:70,z0:58,z1:62},
 {x0:30,x1:34,z0:26,z1:70},
 {x0:26,x1:70,z0:30,z1:34},
 {x0:30,x1:40,z0:30,z1:40} // market
];
But main road west from -30 to 0 at z=33-37, gate approach x=-2..2,z=24-37. Good.

Building placement bounds x=26..70,z=26..70. If roadRect market included, skip. Good.

Need nearRoad function uses roadRects. Good.

Need add roadData for each rect. Good.

Building placement:
const buildingPositions = [];
function addBuilding(x,z,w,h,d,color,roofColor,roofH) { ... }
function canPlaceBuilding(x,z) { if (inRect(x,z, marketRect? but roadRects includes market) return false; if (nearRoad(x,z,1.5)) return false; for bp...; return true; }
But if roadRects includes market, canPlaceBuilding skip market. Good.

Grid:
for (let x=26; x<=70; x+=3.5) for (let z=26; z<=70; z+=3.5) { if (nearRoad(x,z,1.5)) continue; ... addBuilding }
Need ensure building not inside water. Town bounds outside. Good.

After grid, if buildingData.length < 60, add random:
while (buildingData.length < 65) { x=rand(26,70); z=rand(26,70); if (canPlaceBuilding(x,z)) addBuilding(...); }
But canPlaceBuilding uses buildingPositions, so okay. Need avoid infinite. Add attempts.

Need market stalls: add 12. Could add before or after. If after, canPlaceBuilding skip market, but we want stalls inside. Use addBuilding directly. Need positions around market. For i=0..11, angle, x=35+cos*4, z=35+sin*4, but avoid roads? Stalls can be on market. Use addBuilding. Good. Need buildingPositions includes them. Good.

Need building colors. Use pick function.

Need roofData. Good.

Need tree generation after building data. Need canPlaceTree uses buildingPositions. Good.

Need tree data. Need cherry target. Let's implement:
let cherryCount=0, greenCount=0;
function addTree(x,z,type) { ... }
// cherry moat
for (let i=0; i<200 && cherryCount<22; i++) { angle = rng()*Math.PI*2; r=26+rng()*3; x=Math.cos(angle)*r; z=Math.sin(angle)*r; if (canPlaceTree(x,z)) { addTree(x,z,'cherry'); cherryCount++; } }
// cherry main road
for (let z=24; z<=34 && cherryCount<35; z+=3) { for (const side of [-3,3]) { x=side; if (canPlaceTree(x,z)) { addTree(x,z,'cherry'); cherryCount++; } } }
for (let x=26; x<=70 && cherryCount<45; x+=3) { for (const side of [-3,3]) { z=35+side; if (canPlaceTree(x,z)) { addTree(x,z,'cherry'); cherryCount++; } } }
// green
for (let i=0; i<300 && greenCount<15; i++) { x=rand(26,70); z=rand(26,70); if (canPlaceTree(x,z)) { addTree(x,z,'green'); greenCount++; } }
Total maybe 60. If cherryCount less, add random cherry until 40. Could after green, while cherryCount<40, random. Good.

Need canPlaceTree: if inside water ring. Use waterOuter=24, waterHole=10. If Math.abs(x)<=24 && Math.abs(z)<=24 && (Math.abs(x)>10 || Math.abs(z)>10) return false. But if x=25,z=0, abs x >24 false, okay. If x=20,z=20, inside water ring true. Good. If x=0,z=25, outside. Good.
If nearRoad margin 1.5 skip. If inRect market? roadRects includes market, nearRoad skip. Good.
If distance to buildingPositions < 3.5 skip. If distance to castle walls? If x,z within 14? skip. Use if (Math.abs(x)<14 && Math.abs(z)<14) return false. Good.
If distance to shrine? Could skip if x<-34 or x>-26? Not necessary. But canPlaceTree may place inside shrine fence. Could skip if x between -34 and -26 and z between 26 and 34. Add.

Need tree foliage colors. Use pick.

Need lanternData. Add function addLantern(x,z). Data {x,y:0,z,w:1,h:0.8,d:1}. But lanternGeo normalized height 1? Let's make lanternGeo height 1. Parts:
base Box(0.45,0.15,0.45) y=0.075
post Box(0.18,0.35,0.18) y=0.325
top Box(0.35,0.15,0.35) y=0.575
cap Box(0.25,0.1,0.25) y=0.725
Total height 0.8. If scale y=1, height 0.8. Could normalize by scaling parts to total 1? Not necessary. Data h=0.8. Use position y=0. Good.

Need fenceData. Add around shrine. Use boxGeo. Data {x,y:0.6,z,w:0.2,h:1.2,d:0.2}. Add rails? Could add horizontal rails? Maybe not. Could add fence posts. Good.

Need torii. Use addBox. Need torii group. Add to scene.

Need point lights. Add after lanterns? Could add fixed.

Need create instanced meshes. Need data arrays. For buildingData, use matBuilding with instance colors. But material color maybe white. Use matBuilding = new THREE.MeshStandardMaterial({color:0xffffff, roughness:0.8, flatShading:true}); Then setColorAt. For roofData, use matRoofTown with instance colors. For foliageData, use matFoliage with instance colors. For roadData, use matRoad no colors. For trimData, use matDarkWood no colors. For trunkData, use matTrunk no colors. For lanternData, use matLantern no colors. For fenceData, use matFence no colors.

Need buildInstanced function. For data with color, use colorData true. For no color, false. Need if data.length >0.

Potential issue: InstancedMesh with material flatShading and instanceColor. Good.

Need maybe set mesh.frustumCulled = false? Not necessary.

Need shadows: castShadow true, receiveShadow true. For road, receive true, cast false maybe. For building, cast true. For roof, cast true. For foliage, cast true. For trunk, cast true. For lantern, cast true. For fence, cast true.

Need maybe water receive shadow. Good.

Need setLighting. Need fog. Need point lights. Need button. Need toggle function.

Potential issue: scene.background in setLighting. Use scene.background = new THREE.Color(...). Good.

Need animate. Use requestAnimationFrame. controls.update. Maybe animate water? Could add simple water shimmer by moving water mesh? Not. Could animate point lights intensity flicker in dusk. Could add time. But not necessary. Maybe add slight water opacity pulse? Could. But code simple.

Need ensure no external images. Good.

Let's consider if code length within 500-900 lines. We can write ~700 lines. Good.

Need think about possible runtime errors.

Import map: In HTML, import map must be before module script. Good.

BufferGeometryUtils import: `import * as BufferGeometryUtils from 'three/addons/utils/BufferGeometryUtils.js';` In three 0.170, path examples/jsm/utils/BufferGeometryUtils.js. Good.

mergeGeometries: In BufferGeometryUtils, function `mergeGeometries` (not mergeBufferGeometries). Good.

Potential issue: `THREE.ShapeGeometry` with holes. Good.

Potential issue: `renderer.outputColorSpace = THREE.SRGBColorSpace;` Good.

Potential issue: `controls.maxPolarAngle = Math.PI / 2 - 0.05;` Good.

Potential issue: `controls.target.set(15, 8, 15);` Good.

Potential issue: `camera.position.set(70, 45, 80);` Good.

Potential issue: shadow camera. Good.

Potential issue: `scene.fog = fog;` Good.

Potential issue: `setLighting` uses `fog.color.setHex`. Good.

Potential issue: point lights intensity. Good.

Potential issue: `pick` function. Good.

Potential issue: `randInt` inclusive. Good.

Potential issue: `addBuilding` uses `roofData.push({x, y:h, z, w, h:roofH, d, color:roofColor});` If roofGeo normalized height 1, scale y=roofH. Good.

Potential issue: `roofGeo` parts total height 1. Let's define:
function makeRoofGeometry() {
 const parts = [];
 const p1 = new THREE.BoxGeometry(1, 0.25, 1); p1.translate(0, 0.125, 0); parts.push(p1);
 const p2 = new THREE.BoxGeometry(0.72, 0.25, 0.72); p2.translate(0, 0.375, 0); parts.push(p2);
 const p3 = new THREE.BoxGeometry(0.45, 0.25, 0.45); p3.translate(0, 0.625, 0); parts.push(p3);
 const ridge = new THREE.BoxGeometry(0.12, 0.25, 0.35); ridge.translate(0, 0.875, 0); parts.push(ridge);
 return BufferGeometryUtils.mergeGeometries(parts);
}
Total height 1. Good.

Potential issue: `addRoof` for castle uses roofGeo, scale y=1.2. Good.

Potential issue: `addBox` for castle uses boxGeo unit. Good.

Potential issue: `addBox` for torii uses torii material. Good.

Potential issue: `addBox` for shrine building uses white. Good.

Potential issue: `addRoof` for shrine uses roofGeo. Good.

Potential issue: `addBox` for bridge uses matRoad. Good.

Potential issue: `addBox` for fence railings uses matFence. Good.

Potential issue: `addBox` for corner posts. Good.

Potential issue: `addBox` for windows. Need ensure not too many. Could add windows for each level. Use loops. For each level, for side in [x,z], for offset. Could add. But addBox with material dark. Good.

Potential issue: `addBox` for castle level corner posts at y center. Good.

Potential issue: `addBox` for dark trim top may intersect roof. Fine.

Potential issue: `addBox` for gate roof uses addRoof. Good.

Potential issue: `addBox` for bridge railings at y=0.5, length 17. Good.

Potential issue: `addBox` for stone walls gate gap. Good.

Potential issue: `addBox` for wall corner posts. Good.

Potential issue: `addBox` for shrine fence? We use fenceData instanced, not addBox. Good.

Potential issue: `addBox` for torii top beam. Good.

Potential issue: `addBox` for shrine building roof. Good.

Potential issue: `addBox` for market stalls? We use instanced buildingData. Good.

Potential issue: `addBuilding` for market stalls uses buildingPositions, canPlaceTree check. Good.

Potential issue: `canPlaceBuilding` uses `buildingPositions` but during grid, buildingPositions updated. Good.

Potential issue: `nearRoad` uses roadRects. Good.

Potential issue: `inRect` for market? roadRects includes market. Good.

Potential issue: `canPlaceTree` uses `buildingPositions`, but buildingPositions includes market stalls. Good.

Potential issue: `canPlaceTree` water ring check. If tree at x=26,z=0, abs x=26 >24, okay. If x=20,z=20, inside water ring, skip. Good. If x=0,z=25, abs z=25 >24, okay. If x=24,z=24, inside water ring? abs <=24 and (abs x>10 or abs z>10) true, skip. Good. If x=10,z=10, hole? abs x>10 false, abs z>10 false, not skip, but inside hole? Actually hole is -10..10, so point on boundary. Could skip if abs x <=10 && abs z <=10. Add if (Math.abs(x) <= 10 && Math.abs(z) <= 10) return false; Good.

Potential issue: `canPlaceTree` nearRoad margin 1.5. Good.

Potential issue: `canPlaceTree` distance to buildingPositions < 3.5. Good.

Potential issue: `canPlaceTree` shrine area skip. Good.

Potential issue: `addTree` foliage clusters may overlap buildings? canPlaceTree checks center, but clusters offset could overlap. Fine.

Potential issue: `addTree` trunkData uses boxGeo. Good.

Potential issue: `foliageData` uses boxGeo. Good.

Potential issue: `buildInstanced` for foliage with colorData true. Good.

Potential issue: `buildInstanced` for building with colorData true. Good.

Potential issue: `buildInstanced` for roof with colorData true. Good.

Potential issue: `buildInstanced` for road no color. Good.

Potential issue: `buildInstanced` for trim no color. Good.

Potential issue: `buildInstanced` for trunk no color. Good.

Potential issue: `buildInstanced` for lantern no color. Good.

Potential issue: `buildInstanced` for fence no color. Good.

Potential issue: `buildInstanced` sets castShadow true for all. For road, maybe cast false. Could set. But okay.

Potential issue: `buildInstanced` if data length 0 returns null. Good.

Potential issue: `mesh.instanceColor.needsUpdate` only if colorData. Good.

Potential issue: `dummy.rotation` for data. We don't use rotation. Could include. Good.

Potential issue: `data` fields for road: x,y,z,w,h,d. Good.

Potential issue: `data` fields for building: x,y,z,w,h,d,color. Good.

Potential issue: `data` fields for roof: x,y,z,w,h,d,color. Good.

Potential issue: `data` fields for foliage: x,y,z,w,h,d,color. Good.

Potential issue: `data` fields for trunk: x,y,z,w,h,d. Good.

Potential issue: `data` fields for lantern: x,y,z,w,h,d. Good.

Potential issue: `data` fields for fence: x,y,z,w,h,d. Good.

Potential issue: `buildInstanced` uses `d.rx||0`. Good.

Potential issue: `THREE.Color` setHex. Good.

Potential issue: `pick` function. Good.

Potential issue: `rand` function. Good.

Potential issue: `rng` seeded. Good.

Potential issue: `setLighting` called initially. Good.

Potential issue: `toggle` function. Good.

Potential issue: `button` event. Good.

Potential issue: `keydown` event. Good.

Potential issue: `resize` event. Good.

Potential issue: `animate` loop. Good.

Potential issue: `controls.update` requires enableDamping. Good.

Potential issue: `renderer.setPixelRatio`. Good.

Potential issue: `document.body.appendChild(renderer.domElement)`. Good.

Potential issue: UI overlay. Good.

Potential issue: `scene.background` initially. Good.

Potential issue: `fog` initially. Good.

Potential issue: `sun.shadow.camera` update? Need `sun.shadow.camera.updateProjectionMatrix()`? Not necessary but can. Good.

Potential issue: `sun.shadow.bias = -0.0005;` Good.

Potential issue: `hemi` ground color. Good.

Potential issue: `pointLights` in setLighting. Good.

Potential issue: `pointLight.decay`? Default 2. Good.

Potential issue: `pointLight.distance`. Good.

Potential issue: `pointLight.castShadow` false. Good.

Potential issue: `water` material side. Good.

Potential issue: `water` receiveShadow. Good.

Potential issue: `ground` plane size. Good.

Potential issue: `town` bounds. Good.

Potential issue: `roadRects` include market. Good.

Potential issue: `addBuilding` for grid uses `roofColor` pick. Good.

Potential issue: `buildingColors` includes dark. Good.

Potential issue: `roofColors`. Good.

Potential issue: `cherryColors`, `greenColors`. Good.

Potential issue: `addBuilding` for market stalls uses colors. Good.

Potential issue: `addBuilding` for random fallback uses canPlaceBuilding. Good.

Potential issue: `canPlaceBuilding` uses `buildingPositions` but if called before buildingPositions defined? Define before. Good.

Potential issue: `addBuilding` pushes buildingPositions. Good.

Potential issue: `canPlaceBuilding` uses `nearRoad` which uses roadRects. Good.

Potential issue: `nearRoad` margin. Good.

Potential issue: `inRect` for canPlaceBuilding? Could use nearRoad. Good.

Potential issue: `canPlaceBuilding` should skip if inside water. Town bounds outside, but random fallback within town. Good.

Potential issue: `canPlaceBuilding` should skip if distance to existing building < 3.5. Good.

Potential issue: `canPlaceBuilding` should skip if distance to shrine? Could add. Good.

Potential issue: `canPlaceTree` should skip if distance to building < 3.5. Good.

Potential issue: `canPlaceTree` should skip if inside shrine fence. Good.

Potential issue: `addTree` uses `randInt(2,4)`. Good.

Potential issue: `randInt` inclusive. Good.

Potential issue: `addTree` foliage y = trunkH + rand(0.5,1.2)*foliageSize. If trunkH 3, foliage y 4-4.5. Good.

Potential issue: `foliageData` size h = size * rand(0.7,1.0). Good.

Potential issue: `trunkData` w=0.35. Good.

Potential issue: `buildInstanced` for trunk uses matTrunk no color. Good.

Potential issue: `buildInstanced` for foliage uses matFoliage with instance colors. Good.

Potential issue: `matFoliage` color white, instance colors. Good.

Potential issue: `matBuilding` color white, instance colors. Good.

Potential issue: `matRoofTown` color white? If instance colors, set base color white. Good.

Potential issue: `matRoof` for castle fixed dark. Good.

Potential issue: `matRoofTown` for instanced roofs with colors. Good.

Potential issue: `matBuilding` for instanced building colors. Good.

Potential issue: `matFoliage` for instanced colors. Good.

Potential issue: `matRoad` for road instanced no color. Good.

Potential issue: `matDarkWood` for trim no color. Good.

Potential issue: `matTrunk` no color. Good.

Potential issue: `matLantern` no color. Good.

Potential issue: `matFence` no color. Good.

Potential issue: `matTorii` no color. Good.

Potential issue: `matStone` no color. Good.

Potential issue: `matWhite` no color. Good.

Potential issue: `matWater` no color. Good.

Potential issue: `matGrass` no color. Good.

Potential issue: `flatShading` true. Good.

Potential issue: `MeshStandardMaterial` with `flatShading` and `side` for water. Good.

Potential issue: `ShapeGeometry` with `flatShading`? It has normals. Good.

Potential issue: `water` position y=0.04, ground y=0. Good.

Potential issue: `roadData` y=0.03, h=0.06, top y=0.06. Good.

Potential issue: `trimData` y=0.08, h=0.16, top 0.16. Good.

Potential issue: `buildingData` y=h/2, bottom 0. Good.

Potential issue: `roofData` y=h, base at top. Good.

Potential issue: `lanternData` y=0, geometry base at 0. Good.

Potential issue: `fenceData` y=0.6, bottom 0. Good.

Potential issue: `addBox` for castle uses y center. Good.

Potential issue: `addRoof` for castle uses y base. Good.

Potential issue: `addBox` for torii uses y center. Good.

Potential issue: `addRoof` for shrine uses y base. Good.

Potential issue: `addBox` for bridge uses y center. Good.

Potential issue: `addBox` for railings uses y center. Good.

Potential issue: `addBox` for gate posts uses y center. Good.

Potential issue: `addBox` for gate beam uses y center. Good.

Potential issue: `addBox` for wall segments uses y center. Good.

Potential issue: `addBox` for base uses y center. Good.

Potential issue: `addBox` for windows uses y center. Good.

Potential issue: `addBox` for corner posts uses y center. Good.

Potential issue: `addBox` for dark trim uses y center. Good.

Potential issue: `addBox` for shrine building uses y center. Good.

Potential issue: `addBox` for shrine roof uses addRoof. Good.

Potential issue: `addBox` for torii top beam uses y center. Good.

Potential issue: `addBox` for torii second beam uses y center. Good.

Potential issue: `addBox` for torii posts uses y center. Good.

Potential issue: `addBox` for torii cap? Good.

Potential issue: `addBox` for lantern? We use instanced. Good.

Potential issue: `addBox` for fence? We use instanced. Good.

Potential issue: `addBox` for bridge railings uses matFence. Good.

Potential issue: `addBox` for castle windows may be too many but okay.

Potential issue: `addBox` for castle corner posts may intersect walls. Fine.

Potential issue: `addBox` for castle dark trim top may intersect roof. Fine.

Potential issue: `addBox` for castle level sizes. Good.

Potential issue: `addRoof` for castle level uses roofGeo normalized. Good.

Potential issue: `addRoof` for gate uses roofGeo. Good.

Potential issue: `addRoof` for shrine uses roofGeo. Good.

Potential issue: `roofGeo` ridge along z. For castle roofs, ridge along z. Good.

Potential issue: `roofGeo` for town roofs, ridge along z. Good.

Potential issue: `roofGeo` base at y=0. Good.

Potential issue: `roofGeo` parts merged. Good.

Potential issue: `lanternGeo` parts merged. Good.

Potential issue: `lanternGeo` base at y=0. Good.

Potential issue: `lanternGeo` total height 0.8. Data h=0.8. Good.

Potential issue: `buildInstanced` for lantern uses data h=0.8. Good.

Potential issue: `lanternGeo` scale x,z=1, y=0.8. Good.

Potential issue: `lanternGeo` parts have y positions within 0-0.8. Good.

Potential issue: `lanternGeo` base at y=0.075, post 0.325, top 0.575, cap 0.725. Good.

Potential issue: `lanternData` positions. Good.

Potential issue: `fenceData` positions. Good.

Potential issue: `addBox` for torii uses `boxGeo`. Good.

Potential issue: `addBox` for torii top beam y=3.0, height 0.3, center 3.0, top 3.15. Posts height 3, center 1.5, top 3. Good. Beam center 3.0, bottom 2.85, top 3.15. Good.

Potential issue: `addBox` for torii second beam y=2.4, height 0.25, center 2.4, bottom 2.275, top 2.525. Good.

Potential issue: `addBox` for torii cap y=3.35, height 0.2, center 3.35, bottom 3.25, top 3.45. Good.

Potential issue: `addBox` for shrine building: white box center y=1, height 2, bottom 0, top 2. Roof base y=2, height 0.8. Good.

Potential issue: `addBox` for shrine dark trim: center y=0.15, height 0.3, bottom 0, top 0.3. Good.

Potential issue: `addBox` for shrine roof: addRoof at y=2, scale w=3.5,h=0.8,d=3.5. Good.

Potential issue: `addBox` for shrine torii path? Not.

Potential issue: `addBox` for castle gate roof: addRoof at y=3.6, scale w=5,h=0.8,d=1.2. Good.

Potential issue: `addBox` for castle gate beam: center y=3.2, height 0.4, bottom 3.0, top 3.4. Posts height 3, top 3. Good. Beam bottom 3.0. Good.

Potential issue: `addBox` for castle wall height 3, center 1.5, top 3. Gate posts height 3, top 3. Good.

Potential issue: `addBox` for castle base top y=4. Level 1 base y=4. Good.

Potential issue: `addBox` for castle base lower step center 0.5 height 1, top 1. Main base center 2 height 3, top 3.5? Wait if main base height 3 center 2, top 3.5. But we want top y=4. Let's recalc. If lower step height 1 center 0.5 top 1. Main base height 3 center 2 top 3.5. Top platform height 1 center 3.5 top 4. Good. So main base top 3.5, top platform top 4. Good. Castle level starts y=4. Good.

Potential issue: `addBox` for main base height 3 center 2, top 3.5. Top platform center 3.5 height 1 top 4. Good.

Potential issue: `addBox` for lower step size 18, main base size 14, top platform size 12. Good.

Potential issue: `addBox` for stone walls height 3 center 1.5 top 3. Good. They sit on lower step? Lower step top 1, walls bottom 0? If wall center 1.5 height 3 bottom 0, top 3. It intersects lower step. Fine. Could set wall bottom 1? But okay. Maybe walls should sit on ground. Good.

Potential issue: `addBox` for wall corner posts height 3.5 center 1.75 bottom 0 top 3.5. Good.

Potential issue: `addBox` for gate posts height 3 center 1.5 bottom 0 top 3. Good.

Potential issue: `addBox` for bridge y=0.05 height 0.1 bottom 0 top 0.1. Good.

Potential issue: `addBox` for bridge railings y=0.5 height 1 bottom 0 top 1. Good.

Potential issue: `addBox` for bridge length 17 center z=15.5, from z=7 to 24. Good. Gate at z=7. Good.

Potential issue: `addBox` for bridge width 3. Good.

Potential issue: `addBox` for bridge railings at x=±1.5, length 17. Good.

Potential issue: `addBox` for bridge maybe over water. Good.

Potential issue: `addBox` for castle windows. Need ensure windows on walls? For each level, add boxes at sides. Could add for level size >4. Use loops. Good.

Potential issue: `addBox` for castle windows uses dark material. Good.

Potential issue: `addBox` for castle windows at y = y + level.height/2. Good.

Potential issue: `addBox` for castle windows on x side: x = level.size/2 + 0.01, z=0, w=0.1, h=0.8, d=1.2. On z side: z=level.size/2+0.01, x=0, w=1.2,h=0.8,d=0.1. Good. For top level size 4, okay.

Potential issue: `addBox` for castle corner posts at corners. Good.

Potential issue: `addBox` for castle dark trim bottom/top. Good.

Potential issue: `addRoof` for castle level uses roofGeo. Good.

Potential issue: `addRoof` for castle level at y = y + level.height. Good.

Potential issue: `addRoof` for castle level scale y=1.2. Good.

Potential issue: `addRoof` for castle level base at top of wall. Good.

Potential issue: `addRoof` for castle level may overlap dark trim top. Fine.

Potential issue: `addRoof` for castle level ridge. Good.

Potential issue: `addRoof` for castle top level. Good.

Potential issue: `addRoof` for castle gate. Good.

Potential issue: `addRoof` for shrine. Good.

Potential issue: `addBox` for torii. Good.

Potential issue: `addBox` for shrine building. Good.

Potential issue: `addBox` for shrine dark trim. Good.

Potential issue: `addBox` for torii posts. Good.

Potential issue: `addBox` for torii beams. Good.

Potential issue: `addBox` for torii cap. Good.

Potential issue: `addBox` for torii base? Could add. Good.

Potential issue: `addBox` for torii at z=24, but water outer 24. Torii at z=24 on water edge? Could be okay. Maybe move to z=26. Shrine area z=26..34. Torii at z=24, path road z=33-37? Hmm. Torii should be at shrine entrance, maybe z=26. Let's set torii at (-30,0,26). Posts z=26. Top beam z=26. Good. Shrine building at (-30,0,30). Fence around -34..-26, z=26..34. Torii at z=26, okay. Path road z=33-37 from x=-30 to 0, not through torii. Could add path from torii to shrine? Not necessary. Maybe torii at (-30,0,24) on path? But water. Let's set torii at (-30,0,26). Good.

Potential issue: `addBox` for torii posts at x=-31.5 and -28.5, z=26. Good.

Potential issue: `addBox` for torii top beam at x=-30,y=3,z=26. Good.

Potential issue: `addBox` for torii second beam y=2.4,z=26. Good.

Potential issue: `addBox` for torii cap y=3.35,z=26. Good.

Potential issue: `addBox` for torii base? Could add small boxes at posts bottom. Good.

Potential issue: `addBox` for torii uses torii material. Good.

Potential issue: `addBox` for torii top beam width 4, height 0.3, depth 0.3. Good.

Potential issue: `addBox` for torii second beam width 3.5, height 0.25, depth 0.25. Good.

Potential issue: `addBox` for torii cap width 4.2, height 0.2, depth 0.3. Good.

Potential issue: `addBox` for torii posts height 3, center 1.5. Good.

Potential issue: `addBox` for torii base at y=0.1, height 0.2. Good.

Potential issue: `addBox` for torii at z=26, fence around z=26. Could overlap. Fine.

Potential issue: `addBox` for shrine building at (-30,0,30). Good.

Potential issue: `addBox` for shrine dark trim. Good.

Potential issue: `addRoof` for shrine at y=2. Good.

Potential issue: `addBox` for shrine maybe uses white material. Good.

Potential issue: `addBox` for shrine roof uses roofGeo. Good.

Potential issue: `addBox` for torii group. Good.

Potential issue: `addBox` for torii group uses `boxGeo`. Good.

Potential issue: `addBox` for torii group uses `matTorii`. Good.

Potential issue: `addBox