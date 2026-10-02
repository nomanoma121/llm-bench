We need answer user's request: produce complete index.html code block only. Need think in English (same language). Need craft code. Need ensure fits ~20k tokens, 500-900 lines. Need no tools. Need plan brief? User says keep planning short, spend most answer on code. Output only code block. Need ensure working. Need import map, Three.js 0.170.0, OrbitControls. Seeded random 1337. Voxel-style stylized Japanese castle town spring. Need geometry in code. Use instanced meshes or merged geometry. Day/dusk toggle. Resize.

Need design robust. Let's outline:
- HTML/CSS minimal, canvas full screen, button toggle day/dusk.
- import map: three, three/addons.
- module script.
- Seeded RNG: mulberry32.
- Scene, camera, renderer, controls.
- Lighting: hemisphere, directional shadow, fog. Day/dusk params.
- Ground: large plane or voxel terrain. Castle raised base. Town lower. Could use merged geometry for ground blocks? Simpler: ground plane with slight height? Need gentle terrain. Could create terrain as grid of voxel blocks? But performance. Use merged geometry for ground blocks maybe 60x60? Need manageable. Could use InstancedMesh for ground blocks? But shadows? InstancedMesh can cast/receive. Need geometry. Maybe use a large plane for ground and castle base as stacked boxes. But "gentle terrain" maybe heightfield. Could create terrain with merged boxes for blocks? 64x64 = 4096 boxes, okay. But shadows and fog. Use BoxGeometry(1,1,1) instanced for terrain? Need colors varied. InstancedMesh with per-instance color. But shadows? InstancedMesh can cast/receive. Need set matrix. Could use terrain grid 80x80, height based on distance from castle. Castle base height 4, town height 0. Use instanced boxes for ground. But too many? 80x80=6400 okay. But if each block 1x1, visual voxel. Need maybe 100x100=10000 okay. Use InstancedMesh with material MeshStandardMaterial, vertexColors? InstancedMesh supports instanceColor. Need set colors.

But buildings: use merged geometry? Could create many boxes with shared material. For performance, use InstancedMesh for building boxes? Need varied sizes. InstancedMesh with BoxGeometry unit, scale via matrix. Need colors. Could use one InstancedMesh for building walls, one for roofs, one for trim? Need shadows. Use InstancedMesh with instanceColor. For roofs, maybe use BoxGeometry or custom roof geometry. Could use InstancedMesh with a roof geometry (pyramid? Japanese roof). Need many roofs. Could create a roof geometry as a box? Voxel-style but layered detail. Could use simple gabled roof: a box with top? Maybe use a custom geometry: a flat roof slab plus ridge? For instanced, can use a single geometry representing roof. For Japanese roofs, maybe a low-poly hip roof: a box with top smaller? Could create a geometry manually: a pyramid-like roof with base rectangle? But instanced scaling can distort. Could use a roof geometry as a box with top? Simpler: use BoxGeometry for roof slab, plus ridge? But many. Could use merged geometry for roofs? Need repeated parts. Could use InstancedMesh with a roof geometry built as a low-poly hip roof: base rectangle, top ridge. But scaling non-uniform may distort. Could use unit roof geometry with fixed proportions and scale. Good.

Need castle: five-level keep on raised stone base. Each level smaller. Curved layered roofs. Could build manually with boxes and roof geometry. Use shared materials. Need white walls, dark wood trim. Could use boxes for walls, roof slabs. Maybe use custom roof geometry for each level. Need curved layered roofs: can approximate with multiple stacked roof slabs or a hip roof with overhang. Use a roof geometry: a box with top smaller? For Japanese roof, maybe a low-poly pyramid with ridge. Could create a geometry from vertices: base rectangle, top ridge line. For each level, use roof geometry scaled. Need dark roof material. Could use a custom geometry function makeRoof(width, depth, height, ridge?) but if many, can use InstancedMesh with unit roof geometry. For castle, individual meshes okay.

Town buildings: 60-100 small buildings. Need roads, alleys, market square. Could generate grid layout. Need avoid castle area. Place buildings on town grid. Use roads as flat boxes or ground color. Could use instanced boxes for building bodies, roofs, maybe doors? Need colors. Use seeded random.

Trees: 40-60 cherry blossom pink, some green. Use instanced trunk cylinders? Could use BoxGeometry for trunk, foliage as boxes or spheres? Voxel-style: trunk box, foliage cluster boxes. Use InstancedMesh for trunks and foliage. Need colors. Could use cherry foliage pink, green foliage. Use instanced boxes for foliage. Maybe multiple foliage boxes per tree. Need performance. Could use InstancedMesh for trunks (unit box scaled), foliage (unit box scaled). For cherry, pink material. For green, green. Could use two instanced meshes: pink foliage, green foliage, trunks. Need shadows.

Shrine: torii gate, stone lanterns, fences, props. Could build manually. Torii: two posts, top beam, second beam. Stone lanterns: base, shaft, light box, cap. Fences: instanced posts/rails. Market square: open area, maybe stalls.

Lighting day/dusk: toggle changes fog color, sky background, hemisphere, directional color/intensity, maybe point lights? Need button. Could animate? Simple.

Camera default view: position around town, target castle. OrbitControls.

Need ensure import map works. Use type="module".

Potential pitfalls:
- InstancedMesh instanceColor requires material.vertexColors? Actually InstancedMesh with instanceColor works if material supports vertexColors? In Three.js, instanceColor is used automatically if set? I think material must have vertexColors? Let's recall: InstancedMesh instanceColor: if instanceColor is set, material uses it? It may require material.vertexColors = true? In examples, set instanceColor and material has vertexColors? I think for InstancedMesh, instanceColor is supported if material.vertexColors = true? Let's check memory: InstancedMesh has instanceColor attribute. It is used in shader if instanceColor exists. Material doesn't need vertexColors? Actually MeshStandardMaterial with vertexColors true? I think instanceColor works regardless? Many examples set material.vertexColors = true? Hmm. To be safe, set material.vertexColors = true for instanced materials. But if using instanceColor, need set color attribute. If material.vertexColors true, it expects color attribute? InstancedMesh instanceColor is separate. I think works. Could avoid instanceColor by using multiple materials? But many colors. Use instanceColor.

- InstancedMesh count must be allocated. Need fill matrices. If count > actual, set count = actual. Use dummy Object3D.

- Shadows: renderer.shadowMap.enabled = true. Directional light shadow camera bounds. Need large enough. Use PCFSoft.

- Ground instanced boxes: if count 10000, shadows okay. Need receive shadows. InstancedMesh receiveShadow = true. castShadow maybe false for ground.

- Terrain height: Use grid size maybe 100x100, cell size 1. Castle center at (0,0). Town south/east. Need coordinate system. Let's define world units. Castle base radius maybe 12. Town extends x 10..60, z 10..60? Need camera. Use grid from -50 to 50. Castle at center. Town south/east means positive z? In Three, z positive maybe south? We can choose. Need main road from gate to town. Could define castle center at (0,0). Town in quadrant x>10,z>10. Main road along z from gate at z=12 to z=50, x=0? But east too. Need roads. Could generate town grid with roads.

Simpler: Use a town grid from x=10..60, z=10..60. Roads: main road along z at x=20? Market square around x=30,z=30. Alleys. Buildings placed in blocks avoiding roads. Need castle gate at south side, bridge over moat. Moat around castle. Need water. Could use a ring of water blocks around castle base. Use plane or instanced boxes. Water material blue, transparent? MeshStandardMaterial with opacity. Could use a circular/rectangular moat. Since voxel, rectangular moat around castle. Castle base on island. Bridge from gate to town. Need water surface. Could use a large plane with hole? Simpler: water as instanced boxes in a ring around castle base, height slightly below ground. But ground blocks around castle? Need terrain. Could make castle base raised, moat water at height 0.5, ground blocks outside. For water, use a ring of boxes or a plane. Use a plane with transparent material, maybe circular. But voxel style. Could use instanced water blocks in a rectangular ring. Need not too many.

Maybe terrain grid: height function. Castle base height 4. Town height 0. Moat water height 0.2. Ground blocks for land. Water blocks for moat. Need avoid buildings on water.

Could define castle footprint: base from -12 to 12, height 4. Moat ring from -16 to 16 excluding base. Water blocks at height 0.2. Bridge at south from z=12 to z=18, x=0, height 1. Gate at z=18? Town starts z=20. Main road from gate to market.

But terrain grid with height function: for each cell, if inside castle base, height=4; if inside moat, water; else land height maybe 0 or gentle. Need ground blocks. Could use instanced boxes for land. For water, separate instanced boxes. For castle base, maybe stacked boxes? Could use ground blocks height 4? If using unit boxes, height 4 means stack? Could use a box for base. Simpler: ground instanced boxes at height h, but each block is 1x1x1 at y=h? If h=4, one block at y=4? That's not a raised base thickness. Could use a large box for castle base. But terrain blocks can be at y=height, representing top surface. For visual, ground blocks as boxes from y=0 to height? If unit box centered at y=h/2? Need consistent. Could use ground blocks as top surface boxes centered at y=h+0.5? Hmm.

Maybe use ground as a heightfield of boxes: each cell has a box of height h, centered at y=h/2. For h=0, box from 0 to 1? If h=0, center 0.5. But buildings sit on top y=h+1? Need define. Could use ground blocks as top surface at y=h, with box centered at y=h+0.5? If h=0, top at 1. Buildings base at 1. That's okay. But castle base height 4: top at 4? If box centered at 2.5? Need top at 4? Let's define ground block top surface at y = height + 0.5? Actually if box height 1 centered at y = height + 0.5, top = height+1. If height=0, top=1. Buildings base at top. For castle base height 4, top=5. Could set castle base as a box from y=1 to 5? Hmm.

Alternative: Use ground blocks as flat top boxes centered at y=height, height 1, top = height+0.5. Buildings base at height+0.5. For height=0, top=0.5. For castle base height=4, top=4.5. Good. Use ground block centered at y=height. Box height 1. Top = height+0.5. Buildings sit at top. For water, height=0.2, top=0.7.

But if ground blocks are boxes, many. Could use a plane for ground and instanced blocks for visual? Maybe okay.

Need shadows: ground blocks receive. Buildings cast.

Could use merged geometry for ground? Instanced easier. Need colors. For grass, varied greens. For roads, gray. For market square, stone. For castle base, stone. Could use instanced ground blocks with instanceColor. Need assign road cells. Could define road cells set.

Town layout:
- Grid cells size 1. Town area x from 10 to 60, z from 10 to 60. Castle center at 0. Moat ring from -16 to 16. Gate at z=18, x=0. Main road from z=18 to z=60 at x=0? But town east? Maybe main road from gate to market square at x=20,z=30. Could have road along x=0 from z=18 to 30, then along z=30 from x=0 to 60. Market square around x=30,z=30. Alleys: x=20,40, etc. Need buildings placed in blocks between roads.

Simpler: define roads as cells in town grid. Main road: x=0..60, z=30? But gate at z=18, x=0, road from gate to market at x=0,z=18..30, then east along z=30 x=0..60. Market square cells x=25..35,z=25..35. Alleys: x=15,45 vertical; z=20,40 horizontal. Buildings placed in cells not road/market/water/castle. Need ensure town south/east. Could use grid x=10..60,z=10..60. But main road at x=0? If town starts x=10, gate road from x=0 to 10? Need bridge from castle to gate at x=0,z=18. Then road from gate to town maybe x=0..10,z=18..30. Could include town area x=0..60,z=10..60. But castle moat occupies -16..16. Gate at z=18, x=0. Road from z=18 to 30 at x=0. Then east road z=30 x=0..60. Market square x=25..35,z=25..35. Buildings in town x=10..60,z=10..60 excluding roads. Good.

Need bridge: from castle base edge z=16? Moat outer z=16, gate z=18. Bridge cells x=0,z=16..18, height maybe 1.5. Could build as boxes.

Gate: main gate at z=18, x=0. Could build gate structure: two pillars, roof, opening. Maybe at road.

Shrine: place in town, maybe x=50,z=15? Need torii, lanterns, fence. Could manually place.

Trees: concentrated along moat and main road. Could place trees on land cells near moat ring and road. Use seeded random. Need avoid roads/buildings. Could generate candidate cells in town and moat edge. For cherry, along moat outer edge and main road. For green, scattered.

Building generation:
- For each town cell, if not road/market/water/castle/shrine, maybe place building with probability. Need 60-100. Could iterate cells and place if random < 0.35, but ensure count. Need avoid too many. Could collect candidate cells, shuffle, choose 80. But roads defined. Need building footprint maybe 2x2 or 3x3. If using cell grid, building occupies multiple cells. Need avoid overlapping. Could use occupancy grid. Simpler: place buildings on 2x2 blocks in town blocks. Use occupancy boolean for cells. For each candidate block, if free, place building. Need roads as cells. Could define town blocks between roads. But easier: iterate x,z, if cell free and random, place building occupying 2x2 or 3x3. Need ensure not on road. Use occupancy.

Instanced building bodies: BoxGeometry(1,1,1). For each building, choose width, depth, height. Place body centered at x+width/2, z+depth/2, y=top+height/2. Need top ground height. Use instance matrix scale. Color white/beige. Need dark trim? Could add roof. For Japanese houses, dark roof. Use roof geometry. Could use a simple gable roof geometry. Need instanced roof. Let's design roof geometry: a low-poly hip roof or gable. For unit base 1x1, height 0.5. Could create a geometry with vertices: base rectangle at y=0, top ridge line at y=h. For gable: base corners (-0.5,0,-0.5),(0.5,0,-0.5),(0.5,0,0.5),(-0.5,0,0.5); ridge top (-0.25,h,0),(0.25,h,0). Faces: two sloped sides, two triangular ends. But if scaled non-uniform, okay. Need normals. Could create BufferGeometry manually. Or use ConeGeometry? A pyramid with 4 sides, but Japanese roof maybe hip. ConeGeometry(radius, height, 4) rotated? For rectangular base, ConeGeometry with radialSegments=4 gives pyramid. Could scale. But roof overhang? Could use a box slab plus pyramid. Simpler: use a BoxGeometry for roof slab? But "curved, layered roofs" for castle. For town, gable roofs. Could use a custom roof geometry with overhang: base rectangle slightly larger than building, top ridge. Need normals. Could create using BufferGeometry from vertices and indices. Need compute normals. Could use geometry.computeVertexNormals().

Roof geometry function:
```
function makeRoofGeometry(width=1, depth=1, height=0.5, overhang=0.1) {
  const hw = width/2 + overhang, hd = depth/2 + overhang;
  const vertices = [
    -hw,0,-hd, hw,0,-hd, hw,0,hd, -hw,0,hd, // base
    -hw*0.5,height,0, hw*0.5,height,0 // ridge
  ];
  indices: base? maybe no bottom. Slopes: 0-5-1, 1-5-2, 2-5-3, 3-5-0? That's pyramid, not gable. For gable ridge along x? Need ridge line from -hw*0.5 to hw*0.5 at z=0. Triangles: left slope: 0,5,1? Wait base corners: 0(-hw,0,-hd),1(hw,0,-hd),2(hw,0,hd),3(-hw,0,hd), ridge 4(-hw*0.5,h,0),5(hw*0.5,h,0). Slopes: front? If ridge along x, slopes to z sides: triangles 0,4,3? and 1,5,2? Actually roof slopes from base edges to ridge. For gable with ridge along x, slopes on z sides: base edge z=-hd to ridge: vertices 0,1,5,4? Quad. Use triangles 0,4,1 and 1,4,5? Need consistent. Let's define ridge endpoints R0=(-hw*0.5,h,0), R1=(hw*0.5,h,0). Slope front (z=-hd): base corners B0=(-hw,0,-hd), B1=(hw,0,-hd). Quad B0-B1-R1-R0. Triangles B0,R0,B1 and B1,R0,R1? Need normals. Slope back: B2=(hw,0,hd), B3=(-hw,0,hd), quad B2-B3-R0-R1. Triangles B2,R1,B3? Hmm. End triangles: left end B0-B3-R0, right end B1-B2-R1. Could create. But for instanced scaling, okay.

Maybe simpler: use a pyramid roof (hip) with base rectangle and top point. For Japanese, hip roof. Geometry: base corners, top point at center height. Triangles from base edges to top. This is easy. Use ConeGeometry? But rectangular base with top point. Could create BufferGeometry: base corners, top. Indices for four triangles. No bottom. This looks like hip roof. Good. For castle layered roofs, can use multiple hip roofs stacked. For town, hip roofs. Use roof geometry unit base 1x1, height 0.5. Scale. Need dark material. Could use same roof geometry for all. Good.

But "curved, layered roofs" maybe can approximate by multiple roof layers: for castle, each level has a roof with overhang, maybe stacked smaller roofs. Use roof geometry scaled. Could add a top ridge? Maybe okay.

Need materials:
- groundMaterial: MeshStandardMaterial vertexColors true, roughness 0.9.
- waterMaterial: MeshStandardMaterial color blue, transparent opacity 0.7, roughness 0.2, metalness 0.1.
- buildingMaterial: white/beige, vertexColors true.
- roofMaterial: dark brown/black, maybe no vertexColors.
- trimMaterial: dark wood.
- stoneMaterial: gray.
- trunkMaterial: brown.
- cherryMaterial: pink.
- greenMaterial: green.
- lanternMaterial: stone, maybe emissive? Could add small point lights? Not necessary.

Instanced meshes:
- groundInst: BoxGeometry(1,1,1), count max cells. Need fill. Use instanceColor.
- waterInst: BoxGeometry(1,1,1), count max moat cells.
- buildingInst: BoxGeometry(1,1,1), count max buildings.
- roofInst: roof geometry, count max buildings.
- treeTrunkInst: BoxGeometry(1,1,1) or CylinderGeometry? Voxel trunk box. count max trees.
- cherryFoliageInst: BoxGeometry(1,1,1), count max foliage boxes.
- greenFoliageInst: BoxGeometry(1,1,1), count max foliage boxes.
- fenceInst maybe posts/rails. Could use instanced boxes for fence posts and rails.
- lanterns maybe instanced? Could manually build few.

Need manage counts. Could preallocate arrays of transforms/colors. Simpler: create InstancedMesh with max count, fill, then set count. Need dummy. For colors, set instanceColor. Need ensure instanceColor allocated. `mesh.instanceColor = new THREE.InstancedBufferAttribute(new Float32Array(count*3), 3);` or use `setColorAt`. If count max, setColorAt. Then `mesh.instanceColor.needsUpdate = true`.

Need helper `addInstance(mesh, position, scale, color)` using dummy. But if mesh count max, need track. Could create class? Simpler functions.

Potential issue: InstancedMesh with geometry roof and instanceColor? If roof material no vertexColors, instanceColor may not apply. Could use material.vertexColors true. But roof color uniform dark, no need. For building colors, use instanceColor. For ground, use instanceColor. For foliage, use instanceColor? Could have separate cherry/green materials, no instanceColor. For building, use instanceColor. For ground, use instanceColor. For water, uniform. For trunk, uniform.

Need shadows: InstancedMesh castShadow true, receiveShadow true. For ground, receive only. For roofs, cast. For trees, cast.

Need camera: PerspectiveCamera fov 50, position (60,45,70), target (10,5,20)? Castle focal. Controls target maybe (10,4,20). Need default view whole town. Could set target (15,4,20). Castle at 0,0. Town east/south. Camera from southeast. Good.

Day/dusk toggle:
- Define day params: background 0x87ceeb, fog 0x87ceeb density 0.006, hemi sky 0xbfd7ff ground 0x8a7a66 intensity 0.8, dir color 0xffffff intensity 1.2, position (40,60,30).
- Dusk: background 0x2b3a55, fog 0x3a4a66 density 0.008, hemi sky 0x6a7a9a ground 0x3a3a3a intensity 0.4, dir color 0xffb070 intensity 0.6, position maybe (-30,25,-20). Could add point lights? Maybe not.
- Button toggles. Need update scene.background, fog, lights. Could animate? Simple.

Need ensure renderer output sRGB? Three 0.170 default. Use `renderer.outputColorSpace = THREE.SRGBColorSpace;` maybe.

Seeded RNG:
```
function mulberry32(a){ return function(){ a |= 0; a = a + 0x6D2B79F5 | 0; var t = Math.imul(a ^ a >>> 15, 1 | a); t = t + Math.imul(t ^ t >>> 7, 61 | t) ^ t; return ((t ^ t >>> 14) >>> 0) / 4294967296; } }
const rand = mulberry32(1337);
```

Need terrain generation. Let's define grid size. Use cell size 1. World coordinates integer cells. Castle center at (0,0). Town x 10..60, z 10..60. But if grid includes negative for castle. Need ground cells from -60 to 60? Could be 121x121=14641, okay. But maybe too many. Use 100x100 from -50 to 50. Town x 10..50, z 10..50. Good. Castle base -12..12, moat -16..16. Ground cells -50..50. Count 10201. Fine.

Height function:
```
function heightAt(x,z) {
 if (insideCastleBase(x,z)) return 4;
 if (insideMoat(x,z)) return 0.2; // water
 // gentle terrain: base 0, maybe small hills away from castle
 let h = 0;
 const d = Math.hypot(x,z);
 if (d > 20) h = Math.max(0, Math.sin(x*0.1)*0.5 + Math.cos(z*0.1)*0.5);
 return h;
}
```
But ground blocks for water? We'll separate. For land, if not water/castle base, add ground block. For castle base, add ground block with stone color height 4. For water, add water block height 0.2. For roads, ground block road color. For market, stone. For town, grass. Need top surface for buildings: top = height + 0.5? If ground block centered at y=height, top = height+0.5. For water top = 0.7. For castle base top = 4.5. Buildings on town top = height+0.5. Good.

But if heightAt returns 0 for land, ground block centered at y=0, top=0.5. Buildings base at 0.5. Good.

Need occupancy grid for town cells. Could use Map or 2D array. Since grid -50..50, use array size 101. But town only positive. Could use `occupied = new Set()` with key `x,z`. For roads, mark. For buildings, mark. For water/castle, mark. Need place buildings.

Roads:
- Main road from gate to market: cells x=0, z=18..30? But gate at z=18. Bridge from z=16..18. Road from z=18 to 30 at x=0. Then east road z=30, x=0..50. Market square x=25..35,z=25..35. Alleys: x=15,45 vertical z=10..50; z=20,40 horizontal x=10..50. Need ensure roads not inside castle/moat. For x=0,z=18..30, z=18 outside moat? Moat outer z=16, so okay. Bridge cells x=0,z=16..18 maybe water? Need bridge over water. We can mark bridge separately.

Maybe define road cells as Set. For each road cell, ground color road. But if road cell inside moat? Bridge. Need not place water there. Could define bridge cells x=0,z=16..18, ground as bridge stone height 1.5? But ground block height? Could add bridge as separate boxes. Simpler: for bridge cells, ground block height 1.5, color stone, top=2.0. But water around. Need water not under bridge? If heightAt water for z=16..18? Moat ring includes z=16? Let's define moat as cells with max(|x|,|z|) between 13 and 16? Castle base max <=12. Moat ring max 13..16. Bridge cells x=0,z=16..18: z=16 is moat, z=17,18 outside. If we mark bridge, ground block height 1.5, no water. Good.

Gate at z=18, x=0. Could build gate at road. Need maybe gate structure spans road. Place at x=0,z=18. Could have pillars at x=-1,1? But road cell x=0. Build manually.

Town building placement:
- Candidate cells in town x=10..50,z=10..50. Exclude roads, market, water, castle, bridge, shrine, trees? We can place buildings first, then trees on free cells. Need ensure 60-100. Could iterate all cells, if free and rand < 0.3, place building. But with 41x41=1681, 0.3 too many. Need limit. Could collect free cells, shuffle, choose 80. But buildings occupy multiple cells, so choose anchor cells. Simpler: iterate blocks. Define town blocks between roads. For each block, place a few buildings. But need code manageable.

Approach:
- Create `occupied` Set for all cells. Mark castle base, moat, roads, market, bridge, shrine area.
- Create list of candidate anchor cells in town grid. For each cell, if not occupied and not adjacent? Could choose random cells, attempt building footprint. Need avoid overlap. Use while count < 80: pick random x,z in town, if free, try footprint w,d. If all cells free, place. This can loop but finite. Need seeded. Could do for attempts 5000. Good.
- Building types: small house, shop, warehouse. Choose w,d,h. Small: 2x2, h 1.5-2.5; shop: 2x3, h 2-3; warehouse: 3x4, h 3-4. Need ensure footprint within town and not road. Use occupancy. Place building body. Roof on top. Maybe add dark trim? Could add a thin box at base? Maybe not. Could add a small porch? Not necessary.

Need building colors: white walls, varied beige. Use instanceColor. Roof dark. Need maybe dark trim: could add a thin dark box around top? Could use trim instanced boxes. Maybe add a dark base/trim for each building: a box slightly larger at bottom? Or a dark roof edge. Could use roof geometry dark. Good.

Roof geometry: Need create once. For building roof, scale to footprint plus overhang. Use roof geometry unit base 1x1 height 0.5. For building, scale x=w+0.4, z=d+0.4, y=roofHeight. Position at building top + roofHeight/2? If roof geometry base at y=0, top point at y=height. If we scale, base at y=0. Need position at building top. If geometry vertices base y=0, top y=0.5. Scaling y by roofHeight, top at roofHeight. Place at building top. Good. But if using dummy scale, geometry base at y=0, position at top. Good.

Castle:
- Base: stone base maybe from y=1 to 4.5? Could use ground block height 4. Need add stone walls? Could build a large box for base: width 24, depth 24, height 3.5, centered at y=2.5? But ground block top at 4.5. Maybe base box from y=1 to 4.5. Use BoxGeometry(24,3.5,24) centered y=2.75? Need align. Simpler: ground block height 4 top 4.5. Add stone base as a box from y=1 to 4.5: height 3.5, center y=2.75. But ground block already occupies cell? Could skip ground block for castle base and use a single box. But for instanced ground, can add a large box? Instanced unit boxes can't large. Could add separate mesh for castle base. Good. Use stone material. Castle base box width 24, depth 24, height 3.5, center y=2.75. Top y=4.5. Add stone trim? Maybe.

Castle levels:
- Level 1 on base: footprint 18x18, height 3, walls white. Center y=4.5+1.5=6.0. Roof on top: base 20x20, height 1.2, dark. Level 2: footprint 14x14, height 2.5, center y=roof top? Need stack. Let's define levels from bottom to top. Base top = 4.5. For each level i=0..4:
  size = 18 - i*3? Level0 18, level1 15, level2 12, level3 9, level4 6. Height = 3 - i*0.3? Maybe.
  wall box: width=size, depth=size, height=levelHeight, center y = currentY + levelHeight/2. currentY starts 4.5. Add wall. Add dark trim: maybe a thin box at top? Could add a dark band: BoxGeometry(size+0.2,0.3,size+0.2) at top? Or base. Use dark material. Add roof: roof geometry scaled size+2, height=1.2, position at currentY+levelHeight. Then currentY += levelHeight + roofHeight? But roof sits on top, next level should start at roof top? In Japanese castle, each level has roof, next smaller level above roof? Actually floors have roofs, next level above. Could set currentY = wall top + roofHeight. But next level walls start at roof top? That may look like stacked roofs. Good.
  Need top roof maybe smaller. Add top ornament? Could add a small gold? Maybe not.

Need curved layered roofs: Use roof geometry with overhang. Could add multiple roof layers per level: a lower roof slab and upper? Maybe use roof geometry plus a thin dark slab. Could approximate by two roof geometries: one large lower, one smaller upper? For each level, add roof geometry scaled. Maybe enough.

Could create a custom "curved roof" geometry: a hip roof with a ridge and slight curve? Hard. Could use multiple stacked boxes for roof layers. But code can be simple.

Maybe use a roof geometry with base rectangle and top ridge, not point, for Japanese. Let's design a gable/hip roof with ridge line. For castle, layered roofs can be multiple roof geometries with ridge. Use a roof geometry function with ridge. For instanced town, use same. Need normals. Let's create a BufferGeometry for a hip roof with ridge line? Could be more complex. Maybe use a simple pyramid roof (point) for all. It's okay.

But "curved, layered roofs" maybe can be approximated by adding a roof slab with overhang and a smaller roof above. For each level, add a dark roof slab (box) and a pyramid roof. Could use roof geometry as a box slab? Hmm.

Let's create a roof geometry as a low-poly hip roof with a ridge line, more Japanese. Use vertices:
Base rectangle corners: B0(-hw,0,-hd), B1(hw,0,-hd), B2(hw,0,hd), B3(-hw,0,hd). Ridge endpoints: R0(-hw*0.4,h,0), R1(hw*0.4,h,0). Faces:
- Front slope: B0, B1, R1, R0. Triangles: B0,R0,B1; B1,R0,R1? Need orientation. Let's compute normals later. Use indices:
  front: 0,4,1, 1,4,5? Wait R0=4, R1=5. Quad B0(0), B1(1), R1(5), R0(4). Triangles (0,4,1) and (1,4,5). This may produce normal? Let's not worry, computeVertexNormals.
- Back slope: B2(2), B3(3), R0(4), R1(5). Quad B2,B3,R0,R1. Triangles (2,5,3) and (3,5,4)? Need consistent.
- Left end: B0(0), B3(3), R0(4). Triangle (0,3,4).
- Right end: B1(1), B2(2), R1(5). Triangle (1,2,5).
- Bottom maybe not.
This creates a hip roof with ridge. Good. Use indices. Need ensure no duplicate vertices? Fine.

For roof geometry, base at y=0, ridge at y=h. If scale, okay.

For castle layered roofs, can use roof geometry with ridge. Add a dark roof slab? Maybe roof geometry itself. Add a thin dark box under roof? Could add a dark eave slab: BoxGeometry(size+1,0.2,size+1) at wall top. Then roof geometry above. Good.

Need materials for castle: white walls, dark trim, dark roof, stone base. Use separate meshes. Could use shared geometry BoxGeometry(1,1,1) and scale? For castle, individual meshes with BoxGeometry? Could use shared unit box and set scale. But easier create BoxGeometry for each? Not too many. Could use helper `addBox(material, x,y,z,w,h,d)`. Use shared geometry? If create many BoxGeometry, okay. But rule reuse geometries. Use `unitBox = new THREE.BoxGeometry(1,1,1)`, then mesh = new THREE.Mesh(unitBox, material); mesh.scale.set(w,h,d); position. This reuses geometry. Good. For roof, use shared roof geometry. For castle, use meshes. For instanced, use unitBox.

Need ensure shadows: meshes cast/receive.

Shrine:
- Place at x=45,z=15 maybe. Need torii gate. Use dark red material? Could use `toriiMaterial` red. Build posts, beams. Stone lanterns: use stone material, maybe emissive? Could add small point lights? Maybe not. Fences: use instanced boxes? Could manually add a few fence posts/rails around shrine. Need not too many. Could use `addBox` for posts and rails.

Market square:
- Ground cells stone. Maybe add market stalls: small boxes with roofs? Could add a few instanced stalls? Could just ground color. Maybe add a few market stalls manually or as buildings. Need "market square". Could place a few stalls in square: small boxes with dark roofs. Could use building instanced? But square cells occupied. Could add manually 6 stalls. Use same building/roof instanced? Could add to instanced arrays. Simpler: during building placement, allow market square stalls? But square should be open. Could manually add stalls around square. Use instanced building/roof? We can add instances manually. Need track counts. Could define helper `addBuildingInstance(x,z,w,d,h,color)` and `addRoofInstance`. Then for stalls call. Good.

Trees:
- Need 40-60 cherry, some green. Could generate tree instances. Use trunk instanced, cherry foliage instanced, green foliage instanced. Need max counts. For each tree, trunk box height 1.5, foliage cluster 3-5 boxes. Need place on free cells. Candidate cells: along moat outer edge and main road. Could first place cherry trees: for cells near moat ring (max abs 17-19?) and road. Need avoid occupied. Use random. Then green trees. Need ensure count. Could collect candidate cells, shuffle, choose. But occupancy includes buildings. Need not place on roads/water. Could place on land cells. For cherry, choose cells with distance to moat edge <3 or near main road. For green, random town. Need avoid building cells. Use occupancy. Could generate candidate list from town grid and moat edge. Then shuffle with seeded rand. Choose first N. Need ensure not on water/road/building. Use `isFree(x,z)`.

Tree geometry: trunk unit box scaled (0.3,1.5,0.3). Foliage unit box scaled (1.2,1.2,1.2) etc. For cherry, pink. For green, green. Could use multiple foliage boxes per tree. Need instanced counts. Max trees 60, foliage boxes maybe 60*5=300. Allocate 400. Trunks 60. Good.

Fences:
- Could use instanced posts and rails. Need max 100. Use dark wood material. Place around shrine and maybe town. Could manually add fence segments. Use helper addBox? But for performance, instanced. Could create `fencePostInst`, `fenceRailInst`. Add around shrine perimeter. Maybe also along main road? Not necessary. Need "fences". Could add a few.

Stone lanterns:
- Could use instanced? Maybe manual few. Use shared unit box. Add base, shaft, light, cap. Could add 4 lanterns. Use stone material. Maybe add small emissive? Could use MeshStandardMaterial emissive yellow? But day/dusk? Could add point lights? Maybe not. Could make lantern light material with emissive. But no need.

Water:
- Use instanced water blocks. Need water surface. Could use a plane for water? Instanced boxes okay. Need transparent. If water blocks are boxes, top at height+0.5. Use height 0.2, top 0.7. Good. Could add slight animation? Not required. Could use `waterMaterial.transparent = true; opacity = 0.75;`.

Ground colors:
- Grass: varied green. Use `new THREE.Color().setHSL(0.3, 0.4, 0.35 + rand*0.1)`.
- Road: gray.
- Market: stone.
- Castle base: stone.
- Bridge: stone.
- Town ground maybe grass. Need roads. Could assign colors.

Need ensure ground blocks not too many. Use grid -50..50. For each cell, if water, add water. Else if castle base, add ground stone. Else if road/market, add ground road/stone. Else add grass. But if building occupies cells, ground still there. Good.

Potential issue: ground blocks as boxes height 1 centered at y=height. If height=4, top=4.5. But castle base separate box top=4.5. Good. If height=0, top=0.5. Buildings base at 0.5. If height gentle 0.5, top=1.0. Good.

Need define `topAt(x,z)` for building placement. If ground block height h, top = h+0.5. For water, not build. For castle base, top=4.5. For bridge, top=2.0. For road, top=0.5. For market, top=0.5.

Building placement occupancy: Need mark cells occupied by building footprint. Use key `x + ',' + z`. For each building, check all cells in footprint. If free, add. Need ensure footprint within town bounds. Use `townMin=10, townMax=50`. Good.

Roads definition:
```
const roads = new Set();
function addRoad(x,z){ roads.add(key); occupied.add(key); }
// main road from gate to market: x=0, z=18..30
for z=18..30 addRoad(0,z)
// east main road: z=30, x=0..50
for x=0..50 addRoad(x,30)
// alleys: x=15, z=10..50; x=45, z=10..50; z=20, x=10..50; z=40, x=10..50
// market square: x=25..35,z=25..35 addMarket
```
But main road x=0,z=18..30 includes gate. Bridge cells x=0,z=16..18. Need mark bridge separately. If road x=0,z=18, gate. Good.

Castle base and moat:
```
function insideCastleBase(x,z){ return Math.max(Math.abs(x), Math.abs(z)) <= 12; }
function insideMoat(x,z){ return Math.max(Math.abs(x), Math.abs(z)) >= 13 && <= 16; }
```
But bridge cells x=0,z=16..18: z=16 inside moat, but bridge. Need exclude water for bridge. Define `bridgeCells` set. For water, if insideMoat and not bridge. For ground, if bridge, add bridge ground height 1.5. For castle base, add ground stone height 4. For town, land.

Gate:
- At z=18, x=0. Could build gate spanning road: two pillars at x=-1,1, z=18, height 3, width 0.5, depth 0.5. Top beam, roof. Use dark wood/stone. Maybe add gate roof. Good.

Shrine:
- Choose area x=45,z=15. Need mark occupied cells for shrine maybe 5x5. Build torii at entrance. Stone lanterns. Fence around. Could use manual boxes. Need not conflict with buildings. Mark shrine cells occupied.

Market stalls:
- Add a few stalls in market square. Could use building instanced? But market square cells occupied. Could add stalls at perimeter. Use `addBuildingInstance` with small w,d,h. Need ensure not on road? Could manually add. But building instanced helper uses occupancy? Could bypass. Simpler: define `addBuildingInstance` that just adds instance, no occupancy. For stalls, call. Need roof. Good.

Instanced helper details:
```
const dummy = new THREE.Object3D();
function addInstanced(mesh, x,y,z, sx,sy,sz, color) {
  dummy.position.set(x,y,z); dummy.scale.set(sx,sy,sz); dummy.rotation.set(0,0,0); dummy.updateMatrix();
  mesh.setMatrixAt(mesh.count, dummy.matrix);
  if (color) mesh.setColorAt(mesh.count, color);
  mesh.count++;
}
```
But `mesh.count` initially 0? InstancedMesh count is max. We can set `mesh.count = 0` then increment. Need `instanceColor` allocated. If using `setColorAt`, it allocates? In Three, `setColorAt` requires `instanceColor` exists? It creates if not? I think `setColorAt` will create instanceColor if null? Let's check: InstancedMesh.setColorAt(index, color) if instanceColor is null, creates. Good. But if material.vertexColors false? It may still work? To be safe, set `mesh.instanceColor = new THREE.InstancedBufferAttribute(new Float32Array(maxCount*3), 3);` and material.vertexColors = true. Then setColorAt.

Need after filling, `mesh.instanceMatrix.needsUpdate = true; mesh.instanceColor.needsUpdate = true;`.

For meshes without instanceColor, no need.

Potential issue: InstancedMesh with count 0 initially, but `setMatrixAt` index 0 okay. Need maxCount. If count exceeds max, error. Allocate enough.

Max counts:
- ground cells: grid 101x101 = 10201. Allocate 11000.
- water: moat ring cells: outer 34x34? max 17? Actually cells -16..16, ring thickness 4? Count ~ (33^2 - 25^2?) if max 13..16 inclusive: outer 33x33=1089, inner max<=12: 25x25=625, ring 464. Allocate 600.
- buildings: 100. Allocate 120.
- roofs: 120.
- trunks: 60.
- cherry foliage: 60*6=360. Allocate 400.
- green foliage: 40*6=240. Allocate 300.
- fence posts: 100, rails: 100.
- lantern parts? Could manual.

Need ensure building count 60-100. We can target 80. If placement attempts fail, maybe less. Could force by trying many. Use attempts 5000. Good.

Tree count: target cherry 45, green 20. Need ensure. Could generate candidate cells and choose. If not enough, fallback. Use `chooseCandidates` with shuffle. Need seeded shuffle. Could implement Fisher-Yates using rand.

Candidate generation:
- For cherry: cells in town grid and moat edge. Need free. Could create list. For each x,z in -50..50, if free and (nearMoat or nearMainRoad) add. NearMoat: max(abs(x),abs(z)) between 17 and 20? But town positive. Main road: x==0 or z==30? But roads occupied. Need adjacent cells. Could define `nearMainRoad` if (x==1 && z>=18 && z<=30) or (z==31 && x>=0 && x<=50) etc. But roads occupied, so adjacent. Could include cells adjacent to road. For cherry concentrated along moat and main road. Good.
- For green: random town cells free.
Need occupancy includes buildings, roads, water, castle, shrine. Good.

But if buildings placed first, candidate free cells fewer. Need enough. Could place buildings target 80, then trees. Good.

Building placement algorithm:
```
const buildingCount = 0;
for attempts=0; attempts<5000 && buildingCount<80; attempts++ {
  x = 10 + Math.floor(rand()*41); z=10+Math.floor(rand()*41);
  if (occupied.has(key)) continue;
  type = rand(); choose w,d,h;
  if footprint free, place.
}
```
Need ensure not on market? Market occupied. Good. Need not on shrine. Good.

Building colors: white walls. Use `new THREE.Color().setHSL(0.1, 0.1, 0.85 + rand*0.1)` maybe. For warehouses, darker. Use instanceColor.

Roof colors: dark. Could use uniform material. If using instanceColor for roof, maybe dark varied. Could set roof material vertexColors true and instanceColor dark brown/black. Good.

Need add building body and roof. For each building, add body instance at top + h/2. Add roof instance at top + h. Roof scale w+0.4, d+0.4, roofHeight=0.6. Need roof geometry base at y=0. If add instance position at top+h, scale y=roofHeight. Good.

Need maybe dark trim: add a thin dark box at building top? Could use `trimInst` with unit box. For each building, add a dark band at top: scale w+0.1,0.2,d+0.1, position top+h-0.1? But roof covers. Could add base trim: scale w+0.1,0.2,d+0.1, position top+0.1. Use dark material. Could add for all buildings. Need instanced trim count 120. Good. This adds detail. Use `trimInst` with dark material. Add for buildings and castle? Castle manual.

Need maybe doors? Not necessary.

Castle details:
- Use `addBox` helper for meshes. Need shared unitBox. `addBox(material, x,y,z,w,h,d, cast=true, receive=true)`.
- Castle base: stone box. Add stone walls? Maybe base has layered stone: add a few boxes around base. Could add a dark trim band at top.
- Levels: for i 0..4:
  size = 18 - i*3; height = 3 - i*0.3; currentY = baseTop.
  wall = addBox(whiteMaterial, 0, currentY+height/2, 0, size, height, size);
  trim = addBox(darkMaterial, 0, currentY+height-0.1, 0, size+0.2, 0.2, size+0.2);
  roofHeight = 1.2 - i*0.1;
  roof = addRoof(roofMaterial, 0, currentY+height, 0, size+2, roofHeight, size+2); // roof geometry base at y=0, position at wall top.
  currentY += height + roofHeight;
- Top: maybe add a small gold ornament? Could add a small box or sphere. Use gold material. Add a small roof? Maybe top level roof already. Add a small pole. Good.

Need roof geometry orientation: If base at y=0, ridge at y=height. For addRoof, mesh position at base y. Good.

Need roof geometry with ridge line. Let's define function:
```
function makeRoofGeometry() {
  const hw=0.5, hd=0.5, h=0.5;
  const vertices = [
    -hw,0,-hd, hw,0,-hd, hw,0,hd, -hw,0,hd,
    -hw*0.4,h,0, hw*0.4,h,0
  ];
  const indices = [
    0,4,1, 1,4,5, // front? maybe
    2,5,3, 3,5,4, // back
    0,3,4, 1,2,5
  ];
  const geo = new THREE.BufferGeometry();
  geo.setAttribute('position', new THREE.Float32BufferAttribute(vertices,3));
  geo.setIndex(indices);
  geo.computeVertexNormals();
  return geo;
}
```
Need check winding. Could produce normals inconsistent. But computeVertexNormals uses face winding. If some faces reversed, normals may be inward. Could set DoubleSide? Use `side: THREE.DoubleSide` for roof material to avoid. But shadows? DoubleSide okay. Could use `side: THREE.DoubleSide` for roof. Or ensure indices. Let's reason.
Vertices:
0 (-0.5,0,-0.5) front-left
1 (0.5,0,-0.5) front-right
2 (0.5,0,0.5) back-right
3 (-0.5,0,0.5) back-left
4 (-0.2,0.5,0) ridge-left
5 (0.2,0.5,0) ridge-right
Front slope (z=-0.5): quad 0-1-5-4. Triangles should be (0,4,1) and (1,4,5)? Let's compute normal for triangle (0,4,1): v0=(-.5,0,-.5), v4=(-.2,.5,0), v1=(.5,0,-.5). Edge1 = v4-v0=(.3,.5,.5), edge2=v1-v0=(1,0,0). Cross edge1 x edge2 = (0*0 - .5*0? Let's compute: (a,b,c)x(d,e,f)=(bf-ce, cd-af, ae-bd). a=.3,b=.5,c=.5,d=1,e=0,f=0 => (0-0, .5*1 - .3*0 = .5, .3*0 - .5*1 = -.5). Normal (0,.5,-.5), points up/front? Good. Triangle (1,4,5): v1=(.5,0,-.5), v4=(-.2,.5,0), v5=(.2,.5,0). Edge1=(-.7,.5,.5), edge2=(-.3,.5,.5). Cross = (0.5*0.5-0.5*0.5=0, 0.5*(-.3)-(-.7)*0.5=-.15+.35=.2, (-.7)*.5 - .5*(-.3)=-.35+.15=-.2) normal (0,.2,-.2) up/front. Good.
Back slope (z=0.5): quad 2-3-4-5? Need triangles (2,5,3) and (3,5,4). Triangle (2,5,3): v2=(.5,0,.5), v5=(.2,.5,0), v3=(-.5,0,.5). Edge1=(-.3,.5,-.5), edge2=(-1,0,0). Cross = (0, -0.5? compute a=-.3,b=.5,c=-.5,d=-1,e=0,f=0 => (0-0, c*d - a*f = (-.5)*(-1)-0=0.5, a*e - b*d = 0 - .5*(-1)=.5) normal (0,.5,.5) up/back. Good. Triangle (3,5,4): v3=(-.5,0,.5), v5=(.2,.5,0), v4=(-.2,.5,0). Edge1=(.7,.5,-.5), edge2=(.3,.5,-.5). Cross = (0, .5*(-.5)-.7*(-.5)= -0.25+0.35=0.1, .7*.5 - .5*.3=0.35-0.15=0.2) up/back. Good.
Left end: quad 0-3-4. Triangle (0,3,4): v0=(-.5,0,-.5), v3=(-.5,0,.5), v4=(-.2,.5,0). Edge1=(0,0,1), edge2=(.3,.5,.5). Cross = (0*0.5-1*0.5=-0.5, 1*0.3-0*0.5=0.3, 0*0-0*0.3=0) normal (-.5,.3,0) points left/up? Good. Right end: triangle (1,2,5): v1=(.5,0,-.5), v2=(.5,0,.5), v5=(.2,.5,0). Edge1=(0,0,1), edge2=(-.3,.5,.5). Cross = (0*0.5-1*0.5=-0.5, 1*(-.3)-0*0.5=-0.3, 0*0-0*(-.3)=0) normal (-.5,-.3,0) points left? Should point right. Maybe winding reversed. Use DoubleSide. Fine.

Could use `side: THREE.DoubleSide` for roof material. Good.

Need roof geometry base at y=0. If using DoubleSide, shadows okay.

Need ground blocks: If using instanced unit boxes, roof geometry not. Good.

Potential performance: 10k instanced boxes with shadows okay. But shadow map size 2048. Need shadow camera cover -60..60. Use `dirLight.shadow.camera.left=-70; right=70; top=70; bottom=-70; near=1; far=200;`. Good.

Need fog: `scene.fog = new THREE.FogExp2(0x87ceeb, 0.006);` or Fog. Use FogExp2. Day/dusk update.

Need background: `scene.background = new THREE.Color(...)`.

Need controls: `controls.enableDamping = true; controls.maxPolarAngle = Math.PI/2 - 0.05?` Could allow. `controls.target.set(15,4,20);`.

Need button UI: fixed top right. CSS.

Need handle resize.

Need code length. Aim 700 lines. Need ensure no syntax errors.

Let's draft code mentally.

HTML:
```
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Japanese Castle Town in Spring</title>
<style>
html,body{margin:0;height:100%;overflow:hidden;background:#000;}
canvas{display:block;}
#ui{position:fixed;top:12px;right:12px;font-family:system-ui;...}
button{...}
</style>
</head>
<body>
<div id="ui"><button id="toggle">Toggle Day / Dusk</button></div>
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
...
</script>
</body>
</html>
```

Need ensure import map before module. Good.

Code structure:
- RNG
- renderer, scene, camera, controls
- lights
- materials
- geometries
- helper functions
- generate ground/water
- generate town buildings
- generate trees
- generate castle
- generate gate/shrine/props
- animate loop

Need order: materials before helpers. Instanced meshes need max counts. Could define `makeInstancedMesh(geometry, material, maxCount, vertexColors=false)`.

```
function makeInstancedMesh(geo, mat, maxCount, useColor=false) {
  const mesh = new THREE.InstancedMesh(geo, mat, maxCount);
  mesh.count = 0;
  if (useColor) {
    mesh.instanceColor = new THREE.InstancedBufferAttribute(new Float32Array(maxCount*3), 3);
    mat.vertexColors = true;
  }
  mesh.castShadow = true;
  mesh.receiveShadow = true;
  scene.add(mesh);
  return mesh;
}
```
But if material reused and vertexColors true, okay. For materials without color, no.

Helper addInstanced:
```
const dummy = new THREE.Object3D();
function addInstanced(mesh, x,y,z, sx,sy,sz, color) {
  if (mesh.count >= mesh.instanceMatrix.count) return;
  dummy.position.set(x,y,z);
  dummy.scale.set(sx,sy,sz);
  dummy.rotation.set(0,0,0);
  dummy.updateMatrix();
  mesh.setMatrixAt(mesh.count, dummy.matrix);
  if (color && mesh.instanceColor) mesh.setColorAt(mesh.count, color);
  mesh.count++;
}
```
Need `mesh.instanceMatrix.count` is max. Good.

For addBox:
```
function addBox(mat, x,y,z,w,h,d, cast=true, receive=true) {
  const mesh = new THREE.Mesh(unitBox, mat);
  mesh.scale.set(w,h,d);
  mesh.position.set(x,y,z);
  mesh.castShadow = cast;
  mesh.receiveShadow = receive;
  scene.add(mesh);
  return mesh;
}
```
For roof:
```
function addRoof(mat, x,y,z,w,h,d) {
  const mesh = new THREE.Mesh(roofGeo, mat);
  mesh.scale.set(w,h,d);
  mesh.position.set(x,y,z);
  mesh.castShadow = true;
  mesh.receiveShadow = true;
  scene.add(mesh);
  return mesh;
}
```
But roof geometry base at y=0, position at base. Good.

Materials:
```
const groundMat = new THREE.MeshStandardMaterial({vertexColors:true, roughness:0.95});
const waterMat = new THREE.MeshStandardMaterial({color:0x4a7fb5, transparent:true, opacity:0.75, roughness:0.2, metalness:0.1});
const buildingMat = new THREE.MeshStandardMaterial({vertexColors:true, roughness:0.8});
const roofMat = new THREE.MeshStandardMaterial({vertexColors:true, roughness:0.7, side:THREE.DoubleSide});
const darkMat = new THREE.MeshStandardMaterial({color:0x3b2f2a, roughness:0.8});
const stoneMat = new THREE.MeshStandardMaterial({color:0x8a8a8a, roughness:0.9});
const trunkMat = new THREE.MeshStandardMaterial({color:0x6b4a3a, roughness:0.9});
const cherryMat = new THREE.MeshStandardMaterial({color:0xf7b7c8, roughness:0.8});
const greenMat = new THREE.MeshStandardMaterial({color:0x5a8f5a, roughness:0.8});
const toriiMat = new THREE.MeshStandardMaterial({color:0xb33a3a, roughness:0.7});
const lanternMat = new THREE.MeshStandardMaterial({color:0x9a9a9a, roughness:0.8});
const lanternLightMat = new THREE.MeshStandardMaterial({color:0xffe08a, emissive:0xffb040, emissiveIntensity:0.5});
```
Need roofMat vertexColors true if instanceColor. For castle roof, if no instanceColor, material color? If vertexColors true but no color attribute, may render black? For regular Mesh with vertexColors true, geometry needs color attribute. If roofMat used for instanced with instanceColor and regular meshes, regular meshes may not have vertex colors. Could use separate roofMatInst with vertexColors true, and roofMatPlain without. Or set roofMat vertexColors false and use instanceColor? Need decide. For instanced roof, if using instanceColor, material should have vertexColors true? But regular roof meshes with same material and no vertex colors may be black. Could use separate materials: `roofMatInst = new MeshStandardMaterial({vertexColors:true, side:DoubleSide})` for instanced; `roofMat = new MeshStandardMaterial({color:0x2f2a25, side:DoubleSide})` for castle. Or use instanceColor for roof and set base color? If vertexColors true, regular mesh without color attribute may use material color? I think vertexColors true requires color attribute; if absent, maybe black. So separate.

Similarly buildingMat vertexColors true for instanced only. No regular building meshes. GroundMat vertexColors true for instanced only. Good.

For instanced materials, set vertexColors true. For regular materials, no.

Instanced meshes:
```
const groundInst = makeInstancedMesh(unitBox, groundMat, 11000, true);
const waterInst = makeInstancedMesh(unitBox, waterMat, 600, false);
const buildingInst = makeInstancedMesh(unitBox, buildingMat, 120, true);
const roofInst = makeInstancedMesh(roofGeo, roofMatInst, 120, true);
const trimInst = makeInstancedMesh(unitBox, darkMatInst? or darkMat, 120, false); // dark uniform, no vertexColors.
const trunkInst = makeInstancedMesh(unitBox, trunkMat, 60, false);
const cherryInst = makeInstancedMesh(unitBox, cherryMat, 400, false);
const greenInst = makeInstancedMesh(unitBox, greenMat, 300, false);
const fencePostInst = makeInstancedMesh(unitBox, darkMat, 120, false);
const fenceRailInst = makeInstancedMesh(unitBox, darkMat, 120, false);
```
But darkMat used for regular and instanced. If no vertexColors, okay. For instanced, no instanceColor. Good.

Need `roofMatInst` vertexColors true. For instanced roof, set colors dark varied. Could use `roofMatInst = new THREE.MeshStandardMaterial({vertexColors:true, side:THREE.DoubleSide, roughness:0.7});`.

Need `groundMat` vertexColors true. `buildingMat` vertexColors true.

Need `makeInstancedMesh` sets `mat.vertexColors = true` if useColor. But if material already used? okay.

Potential issue: `InstancedMesh` with `instanceColor` and material.vertexColors true: if no instanceColor set for some indices? We fill all. Good.

Need after generation, update instanceMatrix and instanceColor. Could call `finishInstanced(mesh)`.

```
function finishInstanced(mesh) {
  mesh.instanceMatrix.needsUpdate = true;
  if (mesh.instanceColor) mesh.instanceColor.needsUpdate = true;
}
```

Need occupancy and key:
```
const occupied = new Set();
function key(x,z){ return x + ',' + z; }
function isFree(x,z){ return !occupied.has(key(x,z)); }
function mark(x,z){ occupied.add(key(x,z)); }
```

Grid bounds: `const GRID_MIN=-50, GRID_MAX=50;` Town bounds `TOWN_MIN=10, TOWN_MAX=50`.

Height function:
```
function insideCastleBase(x,z){ return Math.max(Math.abs(x), Math.abs(z)) <= 12; }
function insideMoat(x,z){ return Math.max(Math.abs(x), Math.abs(z)) >= 13 && Math.max(Math.abs(x), Math.abs(z)) <= 16; }
function isBridge(x,z){ return x===0 && z>=16 && z<=18; }
function heightAt(x,z) {
  if (insideCastleBase(x,z)) return 4;
  if (isBridge(x,z)) return 1.5;
  if (insideMoat(x,z)) return 0.2;
  // gentle terrain
  let h = 0;
  const d = Math.hypot(x,z);
  if (d > 20) {
    h = 0.5 * Math.sin(x*0.12) + 0.5 * Math.cos(z*0.12);
    if (h < 0) h = 0;
  }
  return h;
}
```
But for town cells, heightAt may produce small hills. Buildings placed on top. Good.

Roads/market sets:
```
const roadSet = new Set();
const marketSet = new Set();
function addRoad(x,z){ roadSet.add(key(x,z)); occupied.add(key(x,z)); }
function addMarket(x,z){ marketSet.add(key(x,z)); occupied.add(key(x,z)); }
```
Need bridge cells occupied? Mark bridge. `bridgeSet`.

Define roads:
```
for (let z=18; z<=30; z++) addRoad(0,z);
for (let x=0; x<=50; x++) addRoad(x,30);
for (let z=10; z<=50; z++) { addRoad(15,z); addRoad(45,z); }
for (let x=10; x<=50; x++) { addRoad(x,20); addRoad(x,40); }
for (let x=25; x<=35; x++) for (let z=25; z<=35; z++) addMarket(x,z);
for (let z=16; z<=18; z++) { bridgeSet.add(key(0,z)); occupied.add(key(0,z)); }
```
But road x=0,z=18..30 includes bridge z=18? Bridge z=16..18. Good. Gate at z=18. Need road from gate to market: x=0,z=18..30. Good.

Castle base/moat occupied:
```
for x=-12..12 for z=-12..12 mark(x,z);
for x=-16..16 for z=-16..16 if insideMoat mark(x,z);
```
But bridge cells inside moat z=16, x=0. If mark moat then bridge, okay. Need water generation skip bridge.

Ground generation:
```
for x=GRID_MIN..GRID_MAX for z=GRID_MIN..GRID_MAX {
  const k=key(x,z);
  if (insideMoat(x,z) && !bridgeSet.has(k)) {
    addInstanced(waterInst, x, heightAt(x,z), z, 1,1,1);
    continue;
  }
  const h = heightAt(x,z);
  let color;
  if (insideCastleBase(x,z)) color = stoneColor;
  else if (bridgeSet.has(k)) color = stoneColor;
  else if (roadSet.has(k)) color = roadColor;
  else if (marketSet.has(k)) color = marketColor;
  else color = grassColor;
  addInstanced(groundInst, x, h, z, 1,1,1, color);
}
```
But if occupied by building, ground still added. Good. If water, no ground. If bridge, ground. If castle base, ground. Good.

Need colors: use `new THREE.Color()`. For grass, random. For road, `new THREE.Color(0x7a7a7a)`. For market, `new THREE.Color(0x9a9a8a)`. For stone, `new THREE.Color(0x8a8a8a)`. For water, no color.

Building placement:
```
function tryPlaceBuilding(x,z) {
  const type = rand();
  let w,d,h;
  if (type < 0.6) { w=2; d=2; h=1.5+rand()*1.0; }
  else if (type < 0.85) { w=2; d=3; h=2+rand()*1.0; }
  else { w=3; d=4; h=3+rand()*1.0; }
  // ensure footprint free
  for dx=0..w-1 for dz=0..d-1 if occupied.has(key(x+dx,z+dz)) return false;
  // mark
  for ... mark
  const top = heightAt(x,z) + 0.5; // but if footprint cells have different heights? Use average? Use heightAt anchor.
  const color = new THREE.Color().setHSL(0.1, 0.08, 0.82 + rand()*0.12);
  addInstanced(buildingInst, x+w/2, top+h/2, z+d/2, w, h, d, color);
  addInstanced(roofInst, x+w/2, top+h, z+d/2, w+0.4, 0.6, d+0.4, new THREE.Color(0x3b2f2a));
  addInstanced(trimInst, x+w/2, top+0.1, z+d/2, w+0.1, 0.2, d+0.1);
  return true;
}
```
Need if building footprint includes cells with water? occupied includes water, so no. Good.

But `heightAt` for anchor may be water? occupied prevents. Good.

Need target count. Use attempts.
```
let placed=0;
for (let i=0; i<5000 && placed<80; i++) {
  const x = TOWN_MIN + Math.floor(rand()*(TOWN_MAX-TOWN_MIN+1));
  const z = TOWN_MIN + Math.floor(rand()*(TOWN_MAX-TOWN_MIN+1));
  if (tryPlaceBuilding(x,z)) placed++;
}
```
Need ensure buildings to south/east. Good.

Market stalls: Could add manually after buildings. Need not mark? Could add instances. But if market cells occupied, no building placement. Add stalls around market. Use `addBuildingInstance` helper? We can define `addBuildingAt(x,z,w,d,h,color)` without occupancy. But tryPlace uses occupancy. Could define `addBuildingVisual`. Simpler: in tryPlace, call `addBuildingVisual`. For stalls, call `addBuildingVisual` directly. Need ensure not overlapping? Could manually choose cells in market perimeter. But market cells occupied, visual can overlap ground. Fine.

```
function addBuildingVisual(x,z,w,d,h,color) {
  const top = heightAt(x,z)+0.5;
  addInstanced(buildingInst, x+w/2, top+h/2, z+d/2, w,h,d,color);
  addInstanced(roofInst, x+w/2, top+h, z+d/2, w+0.4,0.6,d+0.4, new THREE.Color(0x3b2f2a));
  addInstanced(trimInst, x+w/2, top+0.1, z+d/2, w+0.1,0.2,d+0.1);
}
```
But for stalls, top maybe market height 0.5. Good. Add 6 stalls at e.g. (26,24),(34,24),(26,36),(34,36),(24,28),(36,28). Need ensure within market? Some adjacent. Good.

Trees:
Need helper `addTree(x,z, cherry)`.
```
function addTree(x,z, cherry) {
  const top = heightAt(x,z)+0.5;
  const trunkH = 1.2 + rand()*0.8;
  addInstanced(trunkInst, x, top+trunkH/2, z, 0.3, trunkH, 0.3);
  const foliageCount = 3 + Math.floor(rand()*3);
  const matInst = cherry ? cherryInst : greenInst;
  for i=0; i<foliageCount; i++) {
    const fx = x + (rand()-0.5)*1.2;
    const fz = z + (rand()-0.5)*1.2;
    const fy = top + trunkH + 0.5 + rand()*0.8;
    const s = 0.8 + rand()*0.8;
    addInstanced(matInst, fx, fy, fz, s, s, s);
  }
}
```
Need ensure foliage count not exceed max. Allocate enough.

Candidate generation:
```
function shuffle(arr) { for i=arr.length-1; i>0; i-- { j=Math.floor(rand()*(i+1)); swap; } }
```
But rand already. Good.

Cherry candidates:
```
const cherryCandidates=[];
for x=-50..50 for z=-50..50 {
  if (!isFree(x,z)) continue;
  const maxAbs = Math.max(Math.abs(x), Math.abs(z));
  const nearMoat = maxAbs >= 17 && maxAbs <= 22;
  const nearMainRoad = (x === 1 && z >= 18 && z <= 30) || (z === 31 && x >= 0 && x <= 50) || (x === -1 && z >= 18 && z <= 30) || (z === 29 && x >= 0 && x <= 50);
  if (nearMoat || nearMainRoad) cherryCandidates.push([x,z]);
}
shuffle(cherryCandidates);
let cherryCount=0;
for (let i=0; i<cherryCandidates.length && cherryCount<45; i++) {
  const [x,z]=cherryCandidates[i];
  if (isFree(x,z)) { addTree(x,z,true); mark(x,z); cherryCount++; }
}
```
But if candidate cell is occupied by building, isFree false. Good. Need mark tree cell to avoid overlap. Good.

Green candidates:
```
const greenCandidates=[];
for x=TOWN_MIN..TOWN_MAX for z=TOWN_MIN..TOWN_MAX if isFree(x,z) greenCandidates.push([x,z]);
shuffle(greenCandidates);
let greenCount=0;
for ... if isFree addTree false mark greenCount++;
```
Need ensure total 40-60. If cherry 45, green 20. Good. If not enough, maybe lower. Could target cherry 40, green 20. Good.

But candidate cells may be adjacent to roads but not occupied? Good.

Fences:
- Add around shrine. Need choose shrine area. Let's define `shrineX=45, shrineZ=15`. Mark cells 43..47,13..17 occupied? But if buildings already placed, could conflict. Better mark shrine before building placement? We need building placement avoid shrine. So define shrine area before buildings. But building placement uses occupied. Good.

Order: mark castle/moat/roads/market/shrine, then buildings, then trees, then props. But shrine props need ground height. Good.

Shrine area:
```
const shrineX=45, shrineZ=15;
for x=43..47 for z=13..17 mark(x,z);
```
But if town bounds, okay. Need maybe not too close to roads? x=45 is alley road, conflict. Choose shrine at x=48,z=12? But town bounds. Let's choose `shrineX=48, shrineZ=12`, area 46..50,10..14. But x=45 alley, z=20 road, okay. Need within town. Mark. Good.

Shrine props:
- Torii at entrance maybe x=48,z=14? Need not on occupied? It can be on shrine area. Build manually.
- Stone lanterns at (46,12),(50,12),(48,10). Build.
- Fence around shrine: use instanced posts/rails. Need add fence posts at perimeter cells. Could loop perimeter and add posts. Rails between. Use dark material. Need not mark? Already occupied. Good.

Fence helper:
```
function addFencePost(x,z) {
  const top = heightAt(x,z)+0.5;
  addInstanced(fencePostInst, x, top+0.5, z, 0.2,1.0,0.2);
}
function addFenceRail(x1,z1,x2,z2) {
  const top = heightAt(x1,z1)+0.5;
  const dx=x2-x1, dz=z2-z1;
  const len = Math.hypot(dx,dz);
  const mx=(x1+x2)/2, mz=(z1+z2)/2;
  const angle = Math.atan2(dz,dx);
  // dummy rotation? addInstanced doesn't support rotation. Could use dummy rotation. Need helper with rotation.
}
```
Need addInstanced with rotation. Modify `addInstanced` to accept optional rotation. Or use dummy rotation. Simpler: for rails, use dummy rotation. Define `addInstanced(mesh, x,y,z, sx,sy,sz, color, rx=0, ry=0, rz=0)`. Then rails can rotate. But for boxes, rotation okay. Need dummy.rotation.set. Good.

Fence rails: horizontal rails at height top+0.7. Use scale len,0.1,0.2, rotation ry? If rail along x, no rotation. If along z, rotate ry = Math.PI/2? Box length along x. For segment from x1 to x2, if dx !=0, scale x=len, z=0.2, no rotation. If dz !=0, scale x=len, z=0.2, rotate ry = Math.PI/2? Actually box length along x, to align along z, rotate y by PI/2. Good. Use `addInstanced(..., 0, ry, 0)`.

Fence perimeter: choose rectangle x=46..50,z=10..14. Add posts at corners and maybe every 1. Add rails between adjacent posts. Could loop perimeter cells. Simpler: add posts at each integer perimeter, rails between consecutive. Need avoid too many. 4x4 perimeter 16 posts, rails 16. Good.

Stone lantern helper:
```
function addLantern(x,z) {
  const top = heightAt(x,z)+0.5;
  addBox(lanternMat, x, top+0.2, z, 0.6,0.4,0.6);
  addBox(lanternMat, x, top+0.8, z, 0.3,0.8,0.3);
  addBox(lanternLightMat, x, top+1.3, z, 0.5,0.5,0.5);
  addBox(lanternMat, x, top+1.7, z, 0.6,0.2,0.6);
}
```
Need `addBox` uses unitBox. Good.

Torii helper:
```
function addTorii(x,z) {
  const top = heightAt(x,z)+0.5;
  addBox(toriiMat, x-1, top+1.5, z, 0.3,3,0.3);
  addBox(toriiMat, x+1, top+1.5, z, 0.3,3,0.3);
  addBox(toriiMat, x, top+3.0, z, 2.4,0.2,0.3);
  addBox(toriiMat, x, top+2.5, z, 2.0,0.2,0.3);
  addBox(toriiMat, x, top+3.2, z, 2.6,0.2,0.3); // top curved? maybe.
}
```
Need maybe top beam slightly wider. Good.

Gate:
```
function addGate() {
  const top = heightAt(0,18)+0.5; // road top 0.5
  addBox(stoneMat, -1, top+1.5, 18, 0.6,3,0.6);
  addBox(stoneMat, 1, top+1.5, 18, 0.6,3,0.6);
  addBox(darkMat, 0, top+3.2, 18, 2.4,0.2,0.6);
  addRoof(darkRoofMat? or darkMat, 0, top+3.4, 18, 2.6,0.8,1.0); // roof geometry base at top+3.4
  // maybe gate doors dark boxes
  addBox(darkMat, 0, top+1.2, 18, 1.6,2.4,0.2);
}
```
Need roof material for gate. Use `roofMatPlain` or `darkRoofMat`. Define `roofMatPlain = new THREE.MeshStandardMaterial({color:0x2f2a25, side:THREE.DoubleSide, roughness:0.7});`. Use for castle and gate. Good.

Bridge:
- Ground instanced already. Could add bridge railings? Maybe add boxes along bridge. Use dark material. Add posts/rails. Bridge cells x=0,z=16..18. Add railings at x=-0.5,0.5? Could add simple boxes. Use `addBox` for bridge deck? Ground block already. Add railings: for z=16..18, add posts at x=-0.5,0.5, top=heightAt+0.5? heightAt bridge 1.5 top=2.0. Add rails. Could use instanced fence? Simpler manual boxes. Add a few.

Market stalls: Need add after building placement. Use `addBuildingVisual`. Need colors. Could add 6 stalls. Ensure buildingInst count not exceed. We allocated 120, placed 80, stalls 6 okay.

Castle:
- Need add after ground? Could add before or after. Use materials. Need base top. `baseTop = 4.5`. Add base box: `addBox(stoneMat, 0, 2.75, 0, 24, 3.5, 24);` But ground block for castle base already at height 4, top 4.5. Base box from y=1 to 4.5. Good. Add stone trim at top: `addBox(darkMat, 0, 4.4, 0, 24.2,0.2,24.2);` Maybe.
- Levels:
```
let y = baseTop;
for (let i=0; i<5; i++) {
  const size = 18 - i*3;
  const height = 3 - i*0.3;
  addBox(whiteMat, 0, y+height/2, 0, size, height, size);
  addBox(darkMat, 0, y+height-0.1, 0, size+0.2, 0.2, size+0.2);
  const roofH = 1.2 - i*0.1;
  addRoof(roofMatPlain, 0, y+height, 0, size+2, roofH, size+2);
  y += height + roofH;
}
// top ornament
addBox(goldMat, 0, y+0.5, 0, 0.4,1.0,0.4);
```
Need whiteMat. Define `whiteMat = new THREE.MeshStandardMaterial({color:0xf5f2e8, roughness:0.8});`.
Gold material. Good.

Need ensure castle roofs use roof geometry base at y=0. `addRoof` position at wall top. Good.

Potential issue: roof geometry with DoubleSide and shadows. Good.

Lighting:
```
const hemi = new THREE.HemisphereLight(0xbfd7ff, 0x8a7a66, 0.8);
scene.add(hemi);
const dir = new THREE.DirectionalLight(0xffffff, 1.2);
dir.position.set(40,60,30);
dir.castShadow = true;
dir.shadow.mapSize.set(2048,2048);
dir.shadow.camera.left=-70; ...
scene.add(dir);
```
Day/dusk toggle:
```
let isDay = true;
function setLighting(day) {
  if (day) {
    scene.background.set(0x87ceeb);
    scene.fog.color.set(0x87ceeb);
    scene.fog.density = 0.006;
    hemi.color.set(0xbfd7ff); hemi.groundColor.set(0x8a7a66); hemi.intensity=0.8;
    dir.color.set(0xffffff); dir.intensity=1.2; dir.position.set(40,60,30);
  } else {
    scene.background.set(0x2b3a55);
    scene.fog.color.set(0x3a4a66);
    scene.fog.density = 0.008;
    hemi.color.set(0x6a7a9a); hemi.groundColor.set(0x3a3a3a); hemi.intensity=0.4;
    dir.color.set(0xffb070); dir.intensity=0.6; dir.position.set(-30,25,-20);
  }
}
```
Need `scene.fog = new THREE.FogExp2(0x87ceeb, 0.006);` before. `scene.background = new THREE.Color(0x87ceeb);`.

Button:
```
document.getElementById('toggle').addEventListener('click', () => { isDay = !isDay; setLighting(isDay); });
```

Animation loop:
```
function animate() {
  requestAnimationFrame(animate);
  controls.update();
  renderer.render(scene, camera);
}
animate();
```

Resize:
```
window.addEventListener('resize', () => { camera.aspect = innerWidth/innerHeight; camera.updateProjectionMatrix(); renderer.setSize(innerWidth, innerHeight); });
```

Need ensure `renderer = new THREE.WebGLRenderer({antialias:true});` `renderer.setSize(innerWidth, innerHeight);` `renderer.shadowMap.enabled = true; renderer.shadowMap.type = THREE.PCFSoftShadowMap;` `renderer.outputColorSpace = THREE.SRGBColorSpace;` Append canvas.

Potential issue: `THREE.SRGBColorSpace` exists in 0.170. Yes.

Need ensure no external textures. Good.

Need code line count. Could be ~600. Need final only code block.

Let's think about possible bugs:
- `makeInstancedMesh` sets `mat.vertexColors = true` for useColor. If material is reused for regular meshes? We use separate. Good.
- `addInstanced` with color: if `mesh.instanceColor` exists. For ground/building/roof, yes. For others no. If color passed but no instanceColor, skip. Good.
- `mesh.count = 0` but `InstancedMesh` constructor count max. `setMatrixAt` uses index. Good.
- `instanceColor` attribute: If we manually create, need `mesh.instanceColor = new THREE.InstancedBufferAttribute(new Float32Array(maxCount * 3), 3);` Then `setColorAt` works. Good.
- `finishInstanced` after all. Good.
- `roofGeo` used for instanced and regular. If `roofMatInst` vertexColors true, regular `roofMatPlain` separate. Good.
- `roofGeo` normals with DoubleSide. Good.
- `addRoof` uses `roofMatPlain` for castle/gate. For instanced roofs, use `roofInst` with `roofMatInst`. Good.

- Building placement: `tryPlaceBuilding` uses `heightAt(x,z)` anchor. If footprint cells have different heights, building may float or sink. Could use average height. Better compute average height over footprint. Use `let sum=0; for cells sum += heightAt; top = sum/(w*d)+0.5;`. Good. Implement.

- Occupancy: building footprint marks cells. If building footprint includes road? occupied prevents. Good.

- Tree candidates: `isFree` checks occupied. But tree cells marked. Good. Need ensure candidate list not too large. Fine.

- Shrine area marked before buildings. But building placement may place adjacent. Good.

- Fence rails: Need `addInstanced` supports rotation. We'll implement.

- Fence posts/rails max counts. Need add fence around shrine. Could add 16 posts, 16 rails. Good.

- Stone lanterns: use `addBox` many, okay.

- Bridge railings: use `addBox` or instanced. Could add simple boxes. Need not too many.

- Water: If water blocks are boxes, transparent. Shadows? Water receive? Could set cast false. `waterInst.castShadow = false; receiveShadow = true;` In makeInstanced, cast true. Could override. Good.

- Ground blocks: cast? Ground should not cast maybe. `groundInst.castShadow = false; receiveShadow = true;` In makeInstanced, cast true. Override. Good.

- Instanced meshes with vertexColors: If `groundMat` vertexColors true, but no instanceColor for some? We fill all. Good.

- `setColorAt` expects `THREE.Color`. Good.

- `addInstanced` color: If color is `THREE.Color`, `setColorAt` copies. Good.

- `dummy.updateMatrix()` after rotation. Good.

- `addInstanced` max check: `if (mesh.count >= mesh.instanceMatrix.count) return;` `instanceMatrix.count` is max. Good.

- `finishInstanced`: if count 0, okay.

- `scene.fog` is FogExp2. `scene.fog.color` exists. Good.

- `controls.maxPolarAngle` maybe prevent below ground. Set `Math.PI/2 - 0.05`. But if target low, okay. Could allow. Use `controls.maxPolarAngle = Math.PI / 2 - 0.05;` to avoid under ground. Good.

- Camera position: `camera.position.set(65, 45, 70);` target `(15,4,20)`. Good.

- Need ensure town south/east. If z positive is south? Not important.

- Need "main gate" maybe gate at castle. Good.

- Need "at least one bridge". Bridge ground and railings. Good.

- Need "market square". Ground market and stalls. Good.

- Need "narrow alleys". Roads alleys. Good.

- Need "stone lanterns". Good.

- Need "fences". Good.

- Need "curved, layered roofs". Castle roofs layered. Could add multiple roof layers per level? Current one roof per level. Could add a lower roof slab and upper roof? Maybe enough. But to be safer, add for each castle level: a dark eave slab and a roof geometry. Already trim. Could add a second smaller roof above? Maybe not. Could add roof geometry with ridge, looks layered. Good.

- Need "white walls and dark wooden trim". Castle white walls, dark trim. Town buildings white/beige, dark roofs/trim. Good.

- Need "soft spring daylight with shadows, light fog". Good.

- Need "pleasant default camera view". Good.

Potential issue: `roofGeo` with `side: DoubleSide` for instanced material. If `roofMatInst` vertexColors true, instanceColor works. Good.

Potential issue: `InstancedMesh` with `instanceColor` and `material.vertexColors = true`: In Three.js, for InstancedMesh, instanceColor is used if `instanceColor` is not null. It may not require `vertexColors`. But setting true okay. For regular meshes with vertexColors true but no color attribute, could be black. We use separate. Good.

Potential issue: `makeInstancedMesh` modifies material vertexColors. If material is used for regular meshes? We use separate. Good.

Potential issue: `roofMatInst` vertexColors true, but `roofGeo` has no color attribute. InstancedMesh instanceColor provides. Good.

Potential issue: `groundMat` vertexColors true, but `unitBox` no color attribute. InstancedMesh instanceColor provides. Good.

Potential issue: `buildingMat` vertexColors true. Good.

Potential issue: `addInstanced` for roof with color. Need `roofInst` instanceColor allocated. Good.

Potential issue: `addInstanced` for ground with color. Good.

Potential issue: `addInstanced` for water no color. Good.

Potential issue: `addInstanced` for fence rails with rotation. Need implement optional rotation. Good.

Let's write code carefully.

Detailed code:

```
import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';

function mulberry32(seed) { ... }
const rand = mulberry32(1337);

const renderer = new THREE.WebGLRenderer({ antialias: true });
renderer.setSize(window.innerWidth, window.innerHeight);
renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
renderer.shadowMap.enabled = true;
renderer.shadowMap.type = THREE.PCFSoftShadowMap;
renderer.outputColorSpace = THREE.SRGBColorSpace;
document.body.appendChild(renderer.domElement);

const scene = new THREE.Scene();
scene.background = new THREE.Color(0x87ceeb);
scene.fog = new THREE.FogExp2(0x87ceeb, 0.006);

const camera = new THREE.PerspectiveCamera(50, window.innerWidth/window.innerHeight, 0.1, 500);
camera.position.set(65, 45, 70);

const controls = new OrbitControls(camera, renderer.domElement);
controls.target.set(15, 4, 20);
controls.enableDamping = true;
controls.maxPolarAngle = Math.PI / 2 - 0.05;
controls.minDistance = 10;
controls.maxDistance = 200;
controls.update();

const hemi = new THREE.HemisphereLight(0xbfd7ff, 0x8a7a66, 0.8);
scene.add(hemi);
const dir = new THREE.DirectionalLight(0xffffff, 1.2);
dir.position.set(40, 60, 30);
dir.castShadow = true;
dir.shadow.mapSize.set(2048, 2048);
dir.shadow.camera.left = -70;
...
scene.add(dir);
```

Materials:
```
const unitBox = new THREE.BoxGeometry(1,1,1);
const roofGeo = makeRoofGeometry();
const groundMat = new THREE.MeshStandardMaterial({ vertexColors: true, roughness: 0.95 });
const waterMat = new THREE.MeshStandardMaterial({ color: 0x4a7fb5, transparent: true, opacity: 0.75, roughness: 0.2, metalness: 0.1 });
const buildingMat = new THREE.MeshStandardMaterial({ vertexColors: true, roughness: 0.8 });
const roofMatInst = new THREE.MeshStandardMaterial({ vertexColors: true, side: THREE.DoubleSide, roughness: 0.7 });
const roofMatPlain = new THREE.MeshStandardMaterial({ color: 0x2f2a25, side: THREE.DoubleSide, roughness: 0.7 });
const darkMat = new THREE.MeshStandardMaterial({ color: 0x3b2f2a, roughness: 0.8 });
const stoneMat = new THREE.MeshStandardMaterial({ color: 0x8a8a8a, roughness: 0.9 });
const whiteMat = new THREE.MeshStandardMaterial({ color: 0xf5f2e8, roughness: 0.8 });
const trunkMat = new THREE.MeshStandardMaterial({ color: 0x6b4a3a, roughness: 0.9 });
const cherryMat = new THREE.MeshStandardMaterial({ color: 0xf7b7c8, roughness: 0.8 });
const greenMat = new THREE.MeshStandardMaterial({ color: 0x5a8f5a, roughness: 0.8 });
const toriiMat = new THREE.MeshStandardMaterial({ color: 0xb33a3a, roughness: 0.7 });
const lanternMat = new THREE.MeshStandardMaterial({ color: 0x9a9a9a, roughness: 0.8 });
const lanternLightMat = new THREE.MeshStandardMaterial({ color: 0xffe08a, emissive: 0xffb040, emissiveIntensity: 0.5 });
const goldMat = new THREE.MeshStandardMaterial({ color: 0xd4af37, roughness: 0.4, metalness: 0.6 });
```

Roof geometry function before materials? Need roofGeo. Define function.

```
function makeRoofGeometry() { ... }
```

Instanced helper:
```
const dummy = new THREE.Object3D();
function makeInstancedMesh(geo, mat, maxCount, useColor=false) { ... }
function addInstanced(mesh, x,y,z, sx,sy,sz, color, rx=0, ry=0, rz=0) { ... }
function finishInstanced(mesh) { ... }
```

Need `makeInstancedMesh` uses `scene.add`. Good.

Create instanced meshes:
```
const groundInst = makeInstancedMesh(unitBox, groundMat, 11000, true);
groundInst.castShadow = false;
const waterInst = makeInstancedMesh(unitBox, waterMat, 600, false);
waterInst.castShadow = false;
const buildingInst = makeInstancedMesh(unitBox, buildingMat, 120, true);
const roofInst = makeInstancedMesh(roofGeo, roofMatInst, 120, true);
const trimInst = makeInstancedMesh(unitBox, darkMat, 120, false);
const trunkInst = makeInstancedMesh(unitBox, trunkMat, 60, false);
const cherryInst = makeInstancedMesh(unitBox, cherryMat, 400, false);
const greenInst = makeInstancedMesh(unitBox, greenMat, 300, false);
const fencePostInst = makeInstancedMesh(unitBox, darkMat, 120, false);
const fenceRailInst = makeInstancedMesh(unitBox, darkMat, 120, false);
```

Need `addBox` helper uses `unitBox`. Good.

Occupancy:
```
const occupied = new Set();
const roadSet = new Set();
const marketSet = new Set();
const bridgeSet = new Set();
function key(x,z) { return x + ',' + z; }
function mark(x,z) { occupied.add(key(x,z)); }
function isFree(x,z) { return !occupied.has(key(x,z)); }
```

Bounds and functions:
```
const GRID_MIN=-50, GRID_MAX=50;
const TOWN_MIN=10, TOWN_MAX=50;
function insideCastleBase(x,z) { return Math.max(Math.abs(x), Math.abs(z)) <= 12; }
function insideMoat(x,z) { const m = Math.max(Math.abs(x), Math.abs(z)); return m >= 13 && m <= 16; }
function isBridge(x,z) { return x === 0 && z >= 16 && z <= 18; }
function heightAt(x,z) { ... }
```

Mark castle/moat/roads/market/shrine:
```
for x=-12..12 for z=-12..12 mark(x,z);
for x=-16..16 for z=-16..16 if (insideMoat(x,z)) mark(x,z);
for z=16..18 { bridgeSet.add(key(0,z)); mark(0,z); }
function addRoad(x,z) { roadSet.add(key(x,z)); mark(x,z); }
function addMarket(x,z) { marketSet.add(key(x,z)); mark(x,z); }
...
const shrineX=48, shrineZ=12;
for x=46..50 for z=10..14 mark(x,z);
```
Need if shrine area overlaps town bounds. Good.

But building placement uses `isFree`, so shrine occupied. Good.

Ground generation after marking. Need colors. Use `const grassColor = new THREE.Color();` etc. For each cell, if water skip. But if water cell occupied, ground generation still loops. Need check `insideMoat && !bridgeSet.has`. Good.

```
for (let x=GRID_MIN; x<=GRID_MAX; x++) {
  for (let z=GRID_MIN; z<=GRID_MAX; z++) {
    const k = key(x,z);
    if (insideMoat(x,z) && !bridgeSet.has(k)) {
      addInstanced(waterInst, x, heightAt(x,z), z, 1,1,1);
      continue;
    }
    const h = heightAt(x,z);
    let color;
    if (insideCastleBase(x,z)) color = new THREE.Color(0x8a8a8a);
    else if (bridgeSet.has(k)) color = new THREE.Color(0x8a8a8a);
    else if (roadSet.has(k)) color = new THREE.Color(0x7a7a7a);
    else if (marketSet.has(k)) color = new THREE.Color(0x9a9a8a);
    else {
      color = new THREE.Color().setHSL(0.3, 0.45, 0.32 + rand()*0.12);
    }
    addInstanced(groundInst, x, h, z, 1,1,1, color);
  }
}
```
Need `heightAt` for water returns 0.2. Good.

Building visual helper:
```
function addBuildingVisual(x,z,w,d,h,color) {
  const top = heightAt(x,z) + 0.5;
  addInstanced(buildingInst, x+w/2, top+h/2, z+d/2, w,h,d, color);
  addInstanced(roofInst, x+w/2, top+h, z+d/2, w+0.4, 0.6, d+0.4, new THREE.Color(0x3b2f2a));
  addInstanced(trimInst, x+w/2, top+0.1, z+d/2, w+0.1, 0.2, d+0.1);
}
```
But for building placement, top should average. Could implement `tryPlaceBuilding` with average and call `addBuildingVisualAt` with top. Simpler: `addBuildingVisual` uses heightAt anchor. For average, modify:
```
function addBuildingVisual(x,z,w,d,h,color, top) { ... }
```
Then tryPlace computes top. For stalls, top = heightAt+0.5. Good.

```
function addBuildingVisual(x,z,w,d,h,color, top) {
  addInstanced(buildingInst, x+w/2, top+h/2, z+d/2, w,h,d, color);
  addInstanced(roofInst, x+w/2, top+h, z+d/2, w+0.4, 0.6, d+0.4, new THREE.Color(0x3b2f2a));
  addInstanced(trimInst, x+w/2, top+0.1, z+d/2, w+0.1, 0.2, d+0.1);
}
```

TryPlace:
```
function tryPlaceBuilding(x,z) {
  const type = rand();
  let w,d,h;
  if (type < 0.6) { w=2; d=2; h=1.5+rand()*1.0; }
  else if (type < 0.85) { w=2; d=3; h=2+rand()*1.0; }
  else { w=3; d=4; h=3+rand()*1.0; }
  for (let dx=0; dx<w; dx++) for (let dz=0; dz<d; dz++) if (!isFree(x+dx,z+dz)) return false;
  let sum=0;
  for ... { mark(x+dx,z+dz); sum += heightAt(x+dx,z+dz); }
  const top = sum/(w*d) + 0.5;
  const color = new THREE.Color().setHSL(0.1, 0.08, 0.82 + rand()*0.12);
  addBuildingVisual(x,z,w,d,h,color,top);
  return true;
}
```
Need ensure x+dx,z+dz within town? We choose within TOWN_MIN..TOWN_MAX, but footprint may exceed TOWN_MAX. Check bounds. Add `if (x+dx > TOWN_MAX || z+dz > TOWN_MAX) return false;`. Good.

Building placement attempts. Need target 80. Could use `let placed=0; for attempts...`. Good.

Market stalls:
```
const stallColor = new THREE.Color(0xf0e6d2);
const stalls = [[26,24,2,2,1.5], ...];
for (const [x,z,w,d,h] of stalls) addBuildingVisual(x,z,w,d,h, stallColor, heightAt(x,z)+0.5);
```
Need ensure not exceed roofInst count. Good.

Trees:
```
function shuffle(arr) { ... }
function addTree(x,z, cherry) { ... }
```
Need `addTree` uses `heightAt(x,z)+0.5`. If tree on water? candidates free, water occupied. Good.

Candidate generation after buildings. Need `isFree` for tree cells. But tree candidate may be on road? occupied. Good.

Cherry candidates: Need include cells along moat outside. But town bounds? Could include all grid. Need not on water. `isFree` false for water. Good. Need near main road. Define:
```
const nearMainRoad = (x === 1 && z >= 18 && z <= 30) || (x === -1 && z >= 18 && z <= 30) || (z === 31 && x >= 0 && x <= 50) || (z === 29 && x >= 0 && x <= 50);
```
But if x=1,z=18..30, some cells may be occupied by buildings? If free, good. Need not on road. Good.

Near moat: `maxAbs >= 17 && maxAbs <= 22`. But cells inside town? Some negative. Good.

Green candidates: town grid. Good.

Need ensure cherry count 40-60. If candidates less, maybe. Could target 45. If not enough, okay. Could add fallback: if cherryCount < 40, add random free town cells. But likely enough. Could implement robust:
```
let cherryCount=0;
for candidates... if cherryCount<45 add.
while (cherryCount < 40) { pick random town free, add; }
```
But random loop could infinite if no free. Use attempts. Good.

Green count target 20. If candidates enough. Good.

Fence helper with rotation:
```
function addFencePost(x,z) {
  const top = heightAt(x,z)+0.5;
  addInstanced(fencePostInst, x, top+0.5, z, 0.2,1.0,0.2);
}
function addFenceRail(x1,z1,x2,z2) {
  const top = heightAt(x1,z1)+0.5;
  const dx=x2-x1, dz=z2-z1;
  const len = Math.hypot(dx,dz);
  const mx=(x1+x2)/2, mz=(z1+z2)/2;
  const ry = Math.abs(dx) > Math.abs(dz) ? 0 : Math.PI/2;
  addInstanced(fenceRailInst, mx, top+0.7, mz, len, 0.1, 0.2, null, 0, ry, 0);
}
```
But if segment diagonal? We'll use axis-aligned. Good.

Fence around shrine:
```
const sx1=46, sx2=50, sz1=10, sz2=14;
for (let x=sx1; x<=sx2; x++) { addFencePost(x,sz1); addFencePost(x,sz2); }
for (let z=sz1; z<=sz2; z++) { addFencePost(sx1,z); addFencePost(sx2,z); }
// rails between perimeter points. Need avoid duplicate posts? okay.
for (let x=sx1; x<sx2; x++) { addFenceRail(x,sz1,x+1,sz1); addFenceRail(x,sz2,x+1,sz2); }
for (let z=sz1; z<sz2; z++) { addFenceRail(sx1,z,sx1,z+1); addFenceRail(sx2,z,sx2,z+1); }
```
Need posts at corners duplicated? okay.

Stone lanterns:
```
function addLantern(x,z) { ... }
addLantern(46,12); addLantern(50,12); addLantern(48,10);
```
But lantern at (48,10) on fence perimeter? okay.

Torii:
```
function addTorii(x,z) { ... }
addTorii(48,14);
```
But torii at shrine entrance, maybe on fence line. Good.

Gate:
```
function addGate() { ... }
addGate();
```
Need gate roof uses `roofMatPlain`. Good.

Bridge railings:
```
function addBridgeRailings() {
  const top = heightAt(0,17)+0.5; // bridge top 2.0
  for (let z=16; z<=18; z++) {
    addBox(darkMat, -0.5, top+0.5, z, 0.1,1.0,0.1);
    addBox(darkMat, 0.5, top+0.5, z, 0.1,1.0,0.1);
  }
  addBox(darkMat, -0.5, top+0.7, 17, 0.1,0.1,3);
  addBox(darkMat, 0.5, top+0.7, 17, 0.1,0.1,3);
}
```
Need top for z=16 maybe heightAt bridge 1.5 top 2.0. Good.

Castle:
```
function buildCastle() { ... }
```
Need base top. Use `addBox` and `addRoof`. Good.

Potential issue: `addRoof` uses `roofMatPlain`, but roof geometry base at y=0. For castle level, position at wall top. Good.

Need `addBox` uses `unitBox`, but `unitBox` is shared. Good.

Potential issue: `addBox` with `roofMatPlain` for gate roof? Use `addRoof`. Good.

Potential issue: `addRoof` for gate at top+3.4, scale w=2.6,h=0.8,d=1.0. Good.

Potential issue: `roofGeo` has base at y=0, top ridge at y=0.5. Scaling y by h. Good.

Potential issue: `roofGeo` with DoubleSide, shadows. Good.

Potential issue: `renderer.shadowMap` with many instanced meshes. Good.

Potential issue: `controls.maxPolarAngle` prevents looking from below, but default okay.

Potential issue: `scene.fog` density 0.006 may obscure distant town. Good.

Potential issue: `dir.shadow.camera` needs update? Set before. Good.

Potential issue: `dir.shadow.bias` maybe. Could set `dir.shadow.bias = -0.0005;` to avoid acne. Good.

Potential issue: `hemi` and `dir` day/dusk. Good.

Potential issue: `button` styling. Good.

Need final answer only code block. Need ensure no extra text. Good.

Let's consider if code can run. Need check syntax.

Full code draft:

```
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Japanese Castle Town in Spring</title>
<style>
html, body { margin: 0; height: 100%; overflow: hidden; background: #000; }
canvas { display: block; }
#ui { position: fixed; top: 12px; right: 12px; font-family: system-ui, sans-serif; }
#toggle { padding: 8px 12px; border: 0; border-radius: 6px; background: rgba(255,255,255,0.85); color: #222; cursor: pointer; }
</style>
</head>
<body>
<div id="ui"><button id="toggle">Toggle Day / Dusk</button></div>
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

function mulberry32(seed) {
  return function() {
    seed |= 0;
    seed = seed + 0x6D2B79F5 | 0;
    let t = Math.imul(seed ^ seed >>> 15, 1 | seed);
    t = t + Math.imul(t ^ t >>> 7, 61 | t) ^ t;
    return ((t ^ t >>> 14) >>> 0) / 4294967296;
  };
}
const rand = mulberry32(1337);

const renderer = new THREE.WebGLRenderer({ antialias: true });
...
```

Need `seed` in mulberry32: using `seed` variable inside function, okay. But `seed` is parameter, modified. Good.

Roof geometry function:
```
function makeRoofGeometry() {
  const hw = 0.5, hd = 0.5, h = 0.5;
  const vertices = [
    -hw, 0, -hd,
     hw, 0, -hd,
     hw, 0,  hd,
    -hw, 0,  hd,
    -hw * 0.4, h, 0,
     hw * 0.4, h, 0
  ];
  const indices = [
    0, 4, 1,
    1, 4, 5,
    2, 5, 3,
    3, 5, 4,
    0, 3, 4,
    1, 2, 5
  ];
  const geo = new THREE.BufferGeometry();
  geo.setAttribute('position', new THREE.Float32BufferAttribute(vertices, 3));
  geo.setIndex(indices);
  geo.computeVertexNormals();
  return geo;
}
```
Good.

Potential issue: `Float32BufferAttribute` expects array, okay.

`makeInstancedMesh`:
```
function makeInstancedMesh(geo, mat, maxCount, useColor = false) {
  const mesh = new THREE.InstancedMesh(geo, mat, maxCount);
  mesh.count = 0;
  if (useColor) {
    mesh.instanceColor = new THREE.InstancedBufferAttribute(new Float32Array(maxCount * 3), 3);
    mat.vertexColors = true;
  }
  mesh.castShadow = true;
  mesh.receiveShadow = true;
  scene.add(mesh);
  return mesh;
}
```
Need `scene` defined before. Good.

`addInstanced`:
```
const dummy = new THREE.Object3D();
function addInstanced(mesh, x, y, z, sx, sy, sz, color, rx = 0, ry = 0, rz = 0) {
  if (mesh.count >= mesh.instanceMatrix.count) return;
  dummy.position.set(x, y, z);
  dummy.scale.set(sx, sy, sz);
  dummy.rotation.set(rx, ry, rz);
  dummy.updateMatrix();
  mesh.setMatrixAt(mesh.count, dummy.matrix);
  if (color && mesh.instanceColor) mesh.setColorAt(mesh.count, color);
  mesh.count++;
}
```
Good.

`finishInstanced`:
```
function finishInstanced(mesh) {
  mesh.instanceMatrix.needsUpdate = true;
  if (mesh.instanceColor) mesh.instanceColor.needsUpdate = true;
}
```

`addBox`:
```
function addBox(mat, x, y, z, w, h, d, cast = true, receive = true) {
  const mesh = new THREE.Mesh(unitBox, mat);
  mesh.scale.set(w, h, d);
  mesh.position.set(x, y, z);
  mesh.castShadow = cast;
  mesh.receiveShadow = receive;
  scene.add(mesh);
  return mesh;
}
```
Need `unitBox` defined before. Good.

`addRoof`:
```
function addRoof(mat, x, y, z, w, h, d) {
  const mesh = new THREE.Mesh(roofGeo, mat);
  mesh.scale.set(w, h, d);
  mesh.position.set(x, y, z);
  mesh.castShadow = true;
  mesh.receiveShadow = true;
  scene.add(mesh);
  return mesh;
}
```

Need `roofGeo` defined before materials? Could define function and call. Good.

Order: define renderer, scene, camera, controls, lights, roofGeo, materials, helpers, instanced meshes, generation. `makeInstancedMesh` uses scene, so after scene. `addBox` uses unitBox, so after unitBox. Good.

Potential issue: `makeInstancedMesh` sets `mat.vertexColors = true` for useColor. If `roofMatInst` already vertexColors true, okay. If `ground