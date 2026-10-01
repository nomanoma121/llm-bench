The user wants a large, polished Three.js scene in a single index.html. Let me think carefully about architecture and then write a very substantial implementation.

Key considerations:
- Single index.html, Three.js from CDN (use importmap with three module + OrbitControls from unpkg/jsdelivr).
- Deterministic seeded RNG (mulberry32 with seed 1337).
- Procedural geometry: castle keep (5 levels), yagura, gates, walls, stone fortifications (instanced stones), moat with water, bridges, town buildings (100-160), cherry trees (60-100), green trees, shrine, market, props, lanterns, fences.
- Performance: use InstancedMesh heavily for stones, planks, blossoms, lanterns, fence posts, roof tiles. Merge geometries where possible. Since we can't use BufferGeometryUtils from CDN? Actually we can import from 'three/addons/utils/BufferGeometryUtils.js'. That's an official helper module — allowed.

Let me plan the technical approach:

Use `THREE.InstancedMesh` with per-instance color for most repeated elements. A single big instanced mesh per "material category" with box geometry, and per-instance transforms. That gives thousands of elements at low draw cost. For non-box shapes (roof wedges, cylinders), use separate instanced meshes with those geometries.

Plan: Build a "Builder" class that accumulates instances into buckets keyed by geometry+material, then at the end creates InstancedMeshes. This is the cleanest way to get thousands of elements with few draw calls.

Builder API:
- `box(x,y,z,w,h,d, colorName, rotY?)` — accumulate
- `wedge(...)`, `cyl(...)`, `prism`
- `flush()` → creates InstancedMesh per bucket.

For picking, we need object categories. InstancedMesh raycast gives instanceId. We can store per-instance metadata: category name + a "structure id" and bounding info. On click, we get instanceId → look up category. Highlight: we can recolor the instance (store original color, set highlight color) — but better: highlight the whole structure. To do that, store structureId per instance, and on select, iterate instances of that structure and tint them. That could be many instances but fine (a few thousand max). Actually simpler: create a highlight box (wireframe box or glowing box) around the structure's bounding box, plus tint instances. Let's do: compute structure bounding box at build time (store per structure: min/max), then on select show a `THREE.BoxHelper`-like outline (use LineSegments of EdgesGeometry of a box) plus emissive tint on instances of that structure.

Hmm, tinting instances requires knowing which bucket+instanceId belongs to structure. Store in each bucket an array `structIds[]`. On select, loop all buckets, for each instance with matching structId, set color = original * highlight. Need to store original colors too. Keep `colors` Float32Array per bucket — we already have that. So store `origColors` copy at flush time. Fine.

Let me design the world layout.

Coordinate system: castle at origin. Town extends south (+Z) and east (+X). Let's say +Z is south, +X is east, -Z north, -X west.

Terrain: I'll build terrain as a set of stepped plateaus made of blocks? A voxel look with stepped terrain would be great: use a heightfield function and generate terrain as instanced boxes (like voxel columns) — but that could be huge. Alternative: build terrain from merged box "terraces" — a set of rectangular plateaus at different heights with stone retaining walls. That reads as a handcrafted diorama and is cheap.

I think a hybrid: 
1. Base ground: a large plane (or big box) with a soft green color, at y=0.
2. Castle hill: stepped concentric plateaus (irregular polygons approximated by many boxes) rising to ~26 units.
3. Terraces for town districts: a few large plateaus with slight height differences (1-3 units), with stone retaining edges.
4. Moat: a depression ring — dig by placing water plane at y=-1.2 within a ring, with the ground having a ring-shaped hole. Easier: build ground as tiles (grid of boxes) with per-tile height, skipping/low tiles in the moat ring, and place water surface in the moat.

Actually a grid-of-boxes terrain ("voxel columns") gives the strongest voxel-diorama look and handles moat, terraces, slopes elegantly. Cost: a grid of, say, 120x120 = 14400 boxes at 4 units each = 480x480 world. That's 14400 instances in one InstancedMesh — totally fine (one draw call). But we need the top face colors to vary (grass, road, dirt, stone). Per-instance color handles that. And we only need columns where height > 0... Actually we can render each column as a single box from y=-4 to y=h, so the sides show earth/stone. Per-instance color would then color the whole column. Hmm, we want grass top and earth sides. 

Option: two instanced meshes: "column body" (earth/stone color, slightly darker) and "cap" (top surface, thin box at top with the surface color: grass/road/gravel). That's 2 draw calls, 28800 instances. Fine. Cap thickness 0.3.

Actually with 4-unit cells, the voxel look is chunky. For a "highly detailed handcrafted voxel diorama", cell size ~2 units with a 200x200 world = 100x100 = 10000 cells. Good. Let's set world extent ~ 260x260 with cell 2 → 130x130 = 16900 columns → 33800 instances. Still fine for one/two draw calls. Shadow rendering of 33800 instanced boxes might be heavier but OK. We can limit shadow casting to terrain receiving only (terrain receives shadows, doesn't cast — but then hills won't self-shadow; acceptable, or enable cast too. Let's enable cast on terrain cap only? Hmm, self-shadowing terraces look nice. Let's test conceptually: instanced mesh shadow pass renders all instances; 17k boxes is fine on modern GPU.)

Let me reduce: cell = 2.5, extent 240 → 96x96 = 9216 columns. Good balance.

Height function: define via layered features:
- Castle hill: radial falloff with stepped plateaus (quantize to steps of 2-3 units) → strong terraced look.
- Moat ring: subtract to create depression below water level.
- Town terraces: a few rectangular plateaus with small heights (0.5-2).
- Shrine hill to the east/northeast: small stepped hill.
- General gentle noise.

Then quantize heights to steps (e.g., multiples of 0.6) for a stepped voxel look, with smoothing so slopes become staircases.

Roads: define road polylines; for cells near a road, set height = interpolated along a smooth road elevation and mark surface type = road. Buildings placed along roads.

Hmm, this is getting complex but it's the right approach. Let me simplify to keep it manageable and robust:

**Terrain approach (final):**
- Grid cells of size C=2.4 over N=100 → 240x240 world, centered at origin.
- `baseHeight(x,z)` function combining:
  - castle hill: `hillH = 22 * smoothstep-ish falloff(dist)` with stepped quantization
  - secondary spur hills
  - shrine hill
  - town area: gentle terraces based on region
  - river/moat: negative
- Then a "plateau quantization": for the town region, quantize to steps of 1.2.
- Moat: computed as ring distance from castle outline; inside ring, height = min(height, -1.6) blended.
- Surface type per cell: water (if h < -0.9), road (if near road), stone (castle area), grass, plaza, market, sand/gravel (shrine approach).

Wait — roads need to follow terrain height. Since buildings sit on terrain, we sample terrain height at their position. Roads: just set surface color; height from terrain but flattened along road corridor (average of neighbors) so roads don't kink badly. I'll implement a flattening pass: for cells within road corridor, blend toward a smoothed road height.

Simpler: compute terrain height first (without roads), then for each road cell set height = smoothed average of its own height and neighbors iterated a few times (like a diffusion restricted to road corridor). That gives smooth roads. Good.

**Castle complex design:**

Centered at origin on the hill. Layout (in local coords, +Z = south/front):
- Outer bailey (Sannomaru): ring wall with corner yagura, gates at south, east, west. Radius ~ 62.
- Moat around outer bailey: ring from r=66 to r=78.
- Inner bailey (Honmaru): stone-walled platform at top, radius ~30, height ~20.
- Keep at center: 5 levels, footprint ~ 20x16 at base, total height ~ 46 above the honmaru floor... Let's scale: keep base level 18x15, each level shrinks ~12%, roof thickness etc. Total height maybe 42. With hill 22 → keep top at ~64 world y. Town ground at ~4. Good dramatic scale.

Hmm, careful with scale relative to town buildings (2 stories ~ 6 units tall). A 5-level keep at ~40 units = 6-7x a house. Realistic-ish (Japanese keeps were 30-45m vs houses 5-8m). Good.

Let me define units: 1 unit ≈ 1 meter. Town houses 5-7m tall, streets 8-12m wide, castle hill 20m, keep 42m. World 240m across. That's a compact diorama — good.

**Keep construction:**
For each of 5 levels:
- Wall body: white plaster box, slightly tapered (use a frustum-like shape? Boxes are fine but taper adds realism). I can make a "tapered box" geometry (custom BufferGeometry: box with top face scaled). Let's create a helper `taperedBox(w,h,d,topScale)` producing a geometry. Use instanced? Different sizes per level → separate geometry per level, only ~10 instances. Just use regular meshes for hero structures? That increases draw calls. Better: keep using the builder with a small set of shared geometries and scale via matrix — but non-uniform scale on a tapered box is fine since taper is proportional... Actually if I scale a tapered box non-uniformly, the taper ratio stays proportional in x/z but the top offset scales too. If taper is symmetric (top centered, scaled by s in x and z), then scaling by (sx, sz) gives top of size (s*sx, s*sz) — still a valid tapered box. 

So: one shared "taperedBox" geometry (1x1x1 with top scaled 0.94) can be reused with non-uniform instance scale for all walls. Nice. Similarly a "roof" geometry.

**Roofs — the critical part.** Japanese hip-and-gable (irimoya) roofs. Build roofs from:
- Multiple stacked "eave layers": each layer is a flat-ish slab (box) that overhangs, with progressively smaller ones on top → creates stepped/curved silhouette.
- Thick roof edge: a slightly larger thin slab at the bottom of each layer.
- Ridge: a raised box along the top with ridge-end (onigawara) blocks at the ends.
- Corner upturn: small boxes at the four corners of the eave, raised slightly and rotated.
- Under-eave structure: dark beam boxes under the eave edge, and a shadow-gap band (dark box) between wall and roof.

I'll write `makeRoof(builder, opts)` that supports:
- type: 'irimoya' (hip + gable), 'hip' (yosemune), 'gabled' (kirizuma), 'pyramid' (for yagura/torii? no), 'shed'.
- layers: number of eave layers.
- color: tile dark blue-gray, with slight variation.

Implementation of a hip roof with stepped layers: for layer i of n, from bottom (largest) to top (smallest): a box slab of size (w_i, t, d_i) at height y_i, where w_i shrinks linearly and each slab is a "plate" — stacking plates of decreasing size creates a stepped pyramid which reads as a tiled roof from distance, especially with a slight overhang lip on each. To make it read better, add for each layer a thin "lip" slab slightly larger and darker at its bottom edge → creates strong horizontal lines like eave courses. With 5-7 layers, the silhouette looks like a curved roof. That's the classic voxel-Japanese-roof trick. 

For gabled roofs (town houses): two sloped planes made of stepped plates + gable end triangles + ridge. Stepped plates on each side: for i in 0..n: a box of width (halfSpan_i) placed at offset from ridge, at descending heights, forming a staircase slope. With 4-6 steps it reads as a slope. Plus a top ridge cap.

Actually a cleaner approach for slopes: use a rotated thin box (a slab rotated about the ridge axis) — a plane at an angle. Combined with a thick edge lip. A single rotated slab per side is smooth but "one slab" — the prompt says don't use a single wedge for major roofs. For town houses (background detail) a rotated slab with a lip and ridge is fine and looks better than stairs. For hero roofs, use layered plates.

Hybrid: `roofSlabSide` = rotated box with a lip at the lower end and purlin beams under it. For hero roofs, add 2-3 overlapping stepped eave courses at the bottom edge (the eave is the most visible part) plus a gable/ridge ornament. That gives layered depth.

I think: implement `roofIrimoya(w, d, h, opts)` composed of:
- lower hip skirt: stepped plates (3 layers) around the perimeter forming the flared eave (this gives the characteristic thick, flared Japanese eave).
- upper gabled section: two rotated slabs + gable triangles + ridge beam + ridge caps + onigawara (ogive-end ornaments as small boxes) at both ridge ends.
- underside: dark band + bracket blocks (tokyō) as small boxes along the eave underside.

That's ~40-80 instances per hero roof. 

For town houses: `roofGable` = 2 rotated slabs + ridge cap + eave lip + gable ends (~10-14 instances). Good.

**Materials:** Use MeshStandardMaterial with flatShading? For voxel look, flat shading on boxes is default (boxes already flat-shaded). Use a small palette of colors and per-instance color. One material with vertexColors=true and instanced color attribute. Roughness ~0.85, metalness 0.

Number of materials: keep to ~4-6 (main opaque, water, glow/emissive for lanterns, foliage maybe same). Since per-instance color handles hue, one opaque material suffices for nearly everything. Emissive lanterns need a separate material with emissive — but emissive can't be per-instance easily... Actually with `MeshStandardMaterial` + instanceColor, the emissive is uniform. Trick: use `MeshBasicMaterial` (unlit) with instanceColor for lantern glow parts — they'll look bright regardless of light. At night, we can change the color of those instances (dim in day, bright at night). That's a neat approach: lantern glow instances in a separate bucket with MeshBasicMaterial; day → muted color (paper-ish), night → bright warm. We can just set material.color and/or update instance colors on toggle. Simplest: keep two materials for the same geometry bucket? Or store the bucket reference and swap `mesh.material` between dayMat (paper cream, MeshLambert) and nightMat (emissive basic bright). Swapping material on an InstancedMesh is easy. 

Let's do: bucket 'glow' uses `glowMatDay` (MeshStandardMaterial, color 0xd8c9a8, roughness 1) and at night swap to `glowMatNight` (MeshBasicMaterial color 0xffd9a0) + add point lights? Point lights are expensive; instead use a few (max ~8) point lights near the market and gate at night, plus emissive materials. Actually with MeshBasicMaterial the lanterns glow but don't light surroundings. Add maybe 6-10 point lights at key spots (market, main street, gate, keep) enabled only at night. That's acceptable performance-wise if shadows off for them.

Also fireflies/particles at night? Could add a subtle Points system of drifting petals (sakura petals) — beautiful and cheap. Yes! Falling sakura petals as Points with a custom shader or just PointsMaterial with a small square texture... no textures allowed (well, we can generate a canvas texture procedurally — that's code, not an external asset; allowed since "downloaded textures" are banned). A tiny canvas-generated radial gradient texture is fine and adds a lot. I'll generate petal sprite via canvas.

**Water:** Mesh with a custom shader? Simpler: MeshStandardMaterial with transparent, roughness 0.15, metalness 0.3, color dark teal, plus animated normal-ish effect via a custom onBeforeCompile or just a ShaderMaterial with simple animated ripples + fresnel + reflection of sky color (use scene.environment? no env map). Let's do a ShaderMaterial: mix of deep color and sky color by fresnel, plus animated ripple lines, plus a subtle noise. Deterministic and cheap. Also add reflection of the castle? Too expensive. Fresnel + sky gradient + sun specular streak will look great.

Also add a `PMREMGenerator` with `RoomEnvironment`? That would give nice reflections but changes look. Instead, I'll create a gradient sky via a large sphere with a shader (sky gradient + sun glow), and use it as scene.background. Optionally generate an env map from the sky shader using PMREM on a small scene — that would give lovely IBL. Let's do: render the sky shader into a cube via `PMREMGenerator.fromScene(skyScene)` once per lighting mode. That's a nice touch and cheap (done once). Yes, do it — improves material realism a lot.

Hmm, PMREMGenerator.fromScene requires the scene to have a background or objects. I'll make a `Sky` mesh (BackSide sphere with ShaderMaterial) added to a temp scene, generate PMREM, assign to scene.environment. Then also set scene.background = the same sky mesh? Can't use a mesh as background. Options: set scene.background = a large sky sphere mesh in the main scene (just add the mesh itself, BackSide, depthWrite false, frustumCulled false, big radius). And separately generate PMREM from a temp scene containing a clone. That works.

Actually simpler: add sky sphere to main scene, and generate PMREM from a temp scene with a second sky mesh sharing the material. Fine.

**Shadows:** DirectionalLight with shadow camera covering ~ 260 units. 2048 shadow map. Bias tuned. Terrain receives; buildings cast+receive. With instanced meshes, shadows work (instanced shadow supported).

Since nearly everything is in a handful of InstancedMeshes, we can enable castShadow on the main buckets. Shadow pass cost = number of instances; ~100k instances might be heavy. Let's estimate total instances:
- terrain columns: ~9000 + caps ~9000
- stones: ~2500
- roof plates: town 130 buildings × ~14 = 1800; castle ~600
- walls/frames/windows: 130 × 25 = 3250
- trees: 90 sakura × (trunk 3 + branches 8 + blossom 40) = ~4600; green trees 45 × 12 = 540
- props: ~1500
Total ≈ 30k instances. Shadow pass on 30k boxes is fine.

Draw calls: maybe 25-40. 

**Picking:** raycast against a curated list of "pickable" meshes (the main buckets + water?). InstancedMesh raycast returns instanceId. We map (mesh, instanceId) → {category, structureId}. To limit raycast cost, raycast only against a few buckets (buildings bucket, roofs bucket, trees bucket, terrain excluded). Actually raycasting an InstancedMesh with 30k instances does 30k ray-box tests — that's fine for a single click (few ms). OK.

Better approach: maintain a list of "selectable structures" with bounding boxes; on click, raycast against pickable instanced meshes, get instanceId → structureId → structure info (name, category, bbox). Then highlight: draw a box outline + tint instances.

Tinting: for each bucket, we have `colors` array and `structIds` array. On select, loop all buckets' structIds (30k iterations) — trivial. Set colors to highlight blend, mark needsUpdate. On deselect restore. Good.

**Camera presets:** 5 presets with position+target, animated tween (lerp with easing) for smoothness.

1. Isometric Hero: from south-east high, distance ~180, looking at castle.
2. Castle Front: low, from south across the bridge and moat, looking up at the keep, with sakura in foreground.
3. Street View: at street level in the merchant district, looking down the street toward the castle.
4. Sakura Scenic: close-ish, framed by two big sakura branches, castle behind.
5. Top View: straight-ish above, showing town plan.
6. (extra) Shrine/Dusk view.

I need to compute these positions after generating the town so they align with actual streets. I'll place the main street deterministically and hardcode preset coordinates relative to known landmarks (variables computed at build time). Good — compute from landmark positions.

**Town layout:**

Districts:
- Merchant district (south of the bridge, along the main south road) — dense machiya rows.
- Market plaza (south-east of bridge) with stalls.
- Samurai residence district (west side, larger lots, walls/hedges, bigger gates).
- Residential district (east / south-east).
- Warehouse district (near the water/east, near a canal? maybe near the moat's east side with a water gate).
- Shrine district (east on a small hill, with torii approach path leading from the town).

Road network: 
- Main South Road (Saiden-michi): from bridge south gate (0, ~85) going south to (0, 120), then continues to (0,150)? World half-extent 120. Let's set world extent 260 (half 130). Main road from gate at z=78 south to z=128.
- Ring road around the moat at r≈88, an octagon-ish loop connecting east and west.
- Secondary streets branching off the ring and the main road in a slightly irregular grid.
- Alleys: narrow streets between building rows.

Implementation: define roads as polylines with widths. Then:
1. Mark cells as road if within width/2 of any segment (with slight noise on width).
2. Place buildings: for each road segment, place buildings on both sides at a distance = width/2 + depth/2 + small jitter, stepping along the segment, skipping if overlapping existing buildings or road cells or water. This "buildings face streets" approach yields coherent urban blocks with interior voids (which we fill with courtyards/gardens/props).

Need an occupancy grid to prevent overlaps. Use a coarse grid (cell 2) occupancy boolean; when placing a building rect, mark cells; reject if any needed cell is occupied or is a road cell or water.

This is a solid procedural city generator. Let's implement:

```
function placeAlongRoad(seg, opts) {
  const len = length(seg); 
  let t = startOffset;
  while (t < len - 4) {
    const w = rand between min,max;
    const d = rand;
    center = point along + normal*(width/2 + d/2 + gap)
    if (fits(center, w, d, rotY)) { place building; mark; }
    t += w + gap;
  }
}
```
Buildings rotate to align with the road (rotY = atan2 of direction). Since terrain is voxel-stepped, buildings on slopes need a foundation: add a foundation box (plinth) from terrain min height to terrain max height under the building, colored wood/stone. Compute terrain height at 4 corners; foundation height = max - min + extra; place building at max height. Good — handles slopes.

**Terrain flattening under buildings:** also flatten the terrain cells under the building footprint to the building's base height so it doesn't poke through. Do that during generation (before finalizing terrain heights). So order: 
1. Build road mask + heights.
2. Place buildings (needs terrain height sampling — use the pre-building height function).
3. Flatten terrain under buildings.
4. Generate terrain mesh.
5. Place props, trees (avoid occupied cells).

Trees: need to avoid roads/occupancy too.

**Stone fortification generation:** For castle walls: a wall is a polyline; build:
- Stone base (ishigaki): sloped retaining wall made of rows of individual stones. For a wall segment of length L and height H: rows of stones from bottom to top, each row inset slightly (batter), each stone a box with random size/rotation/tone. Count: for H=18, rows of 1.2 → 15 rows; for L=40, stones of ~2 → 20 per row → 300 stones per segment. With ~10 segments → 3000 stones. That's a lot but within budget (instanced). Maybe reduce: rows of 1.5, stones ~2.4 → 12 rows × 17 = 200/segment. Total maybe 2000-2500. Good — "hundreds of stone elements" satisfied.

- Above the wall: white plaster wall with timber framing (vertical dark posts + horizontal beams), then a dark tiled roof cap with overhang, and small windows/triangular openings. Plus a walkway.

**Yagura (towers):** 2-3 story towers with hip roofs, white walls, dark wood bands, at wall corners. Vary: 
- Tatsumi yagura (corner, 3 story, hip roof)
- Inui yagura (2 story + small)
- A "moon tower" (tsukimi yagura) with a balcony, on the keep's side.
- Gate towers (2 story with gate below).
- A drum/tower near the keep.

**Gate (Otemon):** on the south: stone walls flanking, a wooden gate house with 2 stories, a hip-and-gable roof, a large door with plank detail, small windows, a antechamber (karamon style). Bridge leading to it.

**Bridges:** 
- Main south bridge (wooden, planks, railings, posts, ~24 long over the moat).
- East bridge (smaller).
- West bridge (smaller, stone arch? do a stepped stone bridge).
- Small footbridges in the town over a stream/garden.

**Shrine:** torii gates (2-3 along an approach), stone lanterns rows, steps up a hill, main hall (honden) with hip roof and chigi-like ridge ornaments (crossed bars at ridge ends) + katsuogi (horizontal logs on ridge), haiden, fence (tamagaki) with repeated posts, purification basin (chozuya), omikuji racks. Plus sakura.

**Market:** plaza with stalls (counter, awning of striped... we can vary colors: indigo/white stripes as separate thin boxes), crates, barrels, carts with wheels (cylinders), goods (small colored boxes), banners, lanterns, a well.

**Props:** lanterns (stone lanterns: base, shaft, fire box with glow, cap, finial — 6-8 instances each; ~40 lanterns = 300 instances), fences (posts + 2 rails, instanced), barrels (cylinder), crates (box + slats), carts, wells, wood piles, stepping stones, awnings, signboards, banners, stone paving.

That's a LOT of code. Let's be systematic and write helper functions.

Let me now think about the builder implementation in detail.

```js
class Builder {
  constructor(scene){ this.buckets = new Map(); this.scene=scene; }
  bucket(geoKey, geo, matName){...}
  add(geoKey, matrix, color, structId)
}
```
Geometries registry:
- 'box' : BoxGeometry(1,1,1) translated so origin at center? For placement convenience, keep centered; I'll pass center coords.
- 'boxBottom' : BoxGeometry(1,1,1) translated y+0.5 (so y = base). Useful.
- 'taper' : custom frustum-ish box (top scaled 0.93 in x,z) with bottom at y=0, height 1.
- 'cyl8' : CylinderGeometry(0.5,0.5,1,8) bottom at 0.
- 'cyl6'
- 'cone4' : pyramid (4-sided) bottom at 0 — for roofs? maybe for spires.
- 'wedge' : triangular prism (roof slope) — maybe not needed.
- 'oct' : CylinderGeometry(0.5,0.5,1,8) rotated.
- 'slab' = box.

For instanced meshes with different geometries, each bucket = one InstancedMesh.

Colors: use `instancedMesh.setColorAt`. Need `instanceColor`. Material must have `vertexColors`? No — for InstancedMesh, `instanceColor` is automatically used when set (three.js handles it via USE_INSTANCING_COLOR). Yes, setColorAt works with standard materials without vertexColors flag.

Important: setColorAt multiplies material.color by instance color. So set material.color = white.

Also color space: use `THREE.SRGBColorSpace` for renderer output; colors defined via `new THREE.Color(hex)` — in three r15x, `Color.setHex` assumes sRGB by default? `new THREE.Color(0xff0000)` sets in working color space if `THREE.ColorManagement.enabled` (default true) — yes, Color constructor converts from sRGB to linear-srgb working space. setColorAt uses the color as-is (linear working). Good, consistent.

Let me pick a Three.js version: r160+ via unpkg with importmap:
```html
<script type="importmap">
{"imports":{"three":"https://unpkg.com/three@0.160.0/build/three.module.js","three/addons/":"https://unpkg.com/three@0.160.0/examples/jsm/"}}
</script>
```
r160 has `outputColorSpace`, `useLegacyLights` deprecated. Fine. I'll use r160.

Actually let me use r162 or r160 — both fine. I'll use 0.160.0 (stable, widely cached).

Hmm, one risk: unpkg availability. Use jsdelivr as it's reliable: `https://cdn.jsdelivr.net/npm/three@0.160.0/build/three.module.js` and `https://cdn.jsdelivr.net/npm/three@0.160.0/examples/jsm/`. I'll go with jsdelivr.

**Performance of building the scene:** generating ~30k instances in JS at load — should take well under a second or two. Fine.

**InstancedMesh count:** allocate exact counts by two-pass or by using dynamic arrays and creating at flush. I'll accumulate arrays of matrices (as flat numbers) and colors, then create InstancedMesh with exact count at flush.

**Frustum culling:** InstancedMesh culls by boundingSphere of the geometry (not instances) unless we compute. Call `mesh.computeBoundingSphere()`? InstancedMesh has `computeBoundingSphere()` that accounts for instances (yes, in recent versions InstancedMesh.computeBoundingSphere uses instance matrices). Call it after flush. Or set frustumCulled=false for big buckets (simpler, avoids culling benefit but avoids disappearing). I'll compute bounding spheres properly.

**Shadow map:** directional light, shadow.camera ortho covering the scene (-140..140), mapSize 2048, bias -0.0008, normalBias 0.6 (helps a lot with instanced boxes). Position light direction from sun angle.

**Day/Night/Twilight:** define 3 lighting presets: 
- day: sky top #6fb3e8 → horizon #cfe6f5, sun warm #ffe9c4 intensity 2.6, hemi sky #bcd8f5 ground #6f6a52 intensity 0.75, fog light blue.
- twilight: sky top #3b3f7a → horizon #ff9a5c, sun #ff9d55 low angle intensity 2.2, hemi #6a6fa8/#4a3f52 0.5, fog #b07a8a-ish. Lanterns on.
- night: sky #0b1230 → #22305e, moon light #9fb6e8 intensity 0.55, hemi #2b3a66/#1a2038 0.35, fog deep blue. Lanterns on, warm point lights.

Transition: animate over ~1.2s lerping colors and intensities. Nice touch. Also update PMREM env per mode (regenerate on switch — PMREM generation is ~ms, fine, but do it once per mode and cache).

Fog: THREE.FogExp2 with low density to add depth (aerial perspective) — great for the "diorama" and cinematic feel. Density ~0.0022 day. Careful not to wash out.

**Post-processing?** Bloom would help night lanterns a lot. UnrealBloomPass requires EffectComposer from addons — allowed. But adds complexity/perf cost. A subtle bloom at night/twilight would boost beauty significantly. Let's include EffectComposer + RenderPass + UnrealBloomPass with low strength (0.35 day, 0.9 night) and output pass. Risk: version API differences. In r160, `UnrealBloomPass` exists; `OutputPass` exists. Use `renderer.toneMapping = ACESFilmic`, and OutputPass handles color space conversion when using composer. I'll include it but guard with try/catch fallback to direct rendering. Good engineering.

Hmm, careful: with EffectComposer, need `composer.setSize` on resize and pixel ratio. And OutputPass applies tone mapping + sRGB. Let me set renderer.outputColorSpace = SRGB (default) and toneMapping ACESFilmic with exposure ~1.0. With composer + OutputPass, the RenderPass renders in linear to a HalfFloat target and OutputPass does tone mapping + color space. That's the standard r15x+ pipeline. OK.

Bloom threshold 0.9, radius 0.5, strength 0.25 day / 0.75 night. Should be safe.

Actually, risk of the bloom making the scene look hazy. Keep strength low. I'll add it with a toggle? Not required. Keep it subtle and always on, tuned per mode.

Let me reconsider: is the composer worth the risk? Bloom on night lanterns and sunset is a big beauty win. I'll implement with try/catch: if composer creation fails, fall back to `renderer.render`.

**UI:** HTML overlay: top-left title card with Japanese-ish typography (use system fonts + maybe a Google font? External font is an external asset... "Do NOT use downloaded textures / image assets". Fonts are arguably fine but let's avoid external dependencies; use system font stack with a nice serif for the title: `Georgia, 'Times New Roman', serif` plus letter-spacing. Fine.)

UI elements:
- Title: "桜城 — Sakura-jo" / "A Castle Town in Spring".
- Buttons: Day / Twilight / Night.
- Camera presets: 5-6 buttons.
- Selected info panel (category, name, detail line).
- Stats: FPS, instances, draw calls, structures.
- Small legend/hint text: drag to orbit, scroll to zoom, click to inspect.
- Loading overlay while generating (with progress? generation is sync; just show then hide).

Style: dark translucent glass panels with subtle border, small caps, accent color sakura pink. Keep it in corners.

**Now let me write the code.** It'll be long. Let me structure:

```
<!DOCTYPE html>
<html>
<head> meta, title, style </head>
<body>
  <div id="app"><canvas id="c"></canvas></div>
  <div id="ui"> ... </div>
  <div id="loader">...</div>
  <script type="importmap">...</script>
  <script type="module">
    import * as THREE from 'three';
    import {OrbitControls} from 'three/addons/controls/OrbitControls.js';
    // ---- RNG
    // ---- Palette
    // ---- Geometry registry
    // ---- Builder class
    // ---- Terrain system
    // ---- Road system
    // ---- Architecture kit: walls, roofs, windows, railings, stone
    // ---- Castle: keep, yagura, gates, walls, moat, bridges
    // ---- Town: districts, archetypes
    // ---- Nature: sakura, trees, shrubs, petals
    // ---- Shrine, Market, Props
    // ---- Lighting & sky
    // ---- Camera presets & tween
    // ---- Picking
    // ---- UI wiring
    // ---- Animation loop
  </script>
</body>
</html>
```

Let me now write details.

### RNG
```js
function mulberry32(a){return function(){a|=0;a=a+0x6D2B79F5|0;let t=Math.imul(a^a>>>15,1|a);t=t+Math.imul(t^t>>>7,61|t)^t;return((t^t>>>14)>>>0)/4294967296}}
const rng = mulberry32(1337);
const rand=(a=1,b=0)=>b+(a-b)*rng(); // careful
```
Better: `rnd(min,max)`, `rndi(min,max)`, `pick(arr)`, `chance(p)`.

### Palette
```js
const C = {
  plaster: 0xf2ece0, plasterShade:0xdcd3c4, plasterWarm:0xe8dcc6,
  woodDark: 0x4a3527, wood: 0x6b4a30, woodMid:0x8a6242, woodLight:0xa87e52,
  tile: 0x35404f, tileDark:0x242c38, tileLight:0x465466,
  stone: 0x8d8578, stoneDark:0x6f6a60, stoneLight:0xa8a093,
  gold: 0xc9a227,
  paper: 0xf0e2bd,
  leaf: 0x4e7a3a, leafDark:0x3a5f2c, leafLight:0x6f9a4a,
  sakura: 0xf6c6d4, sakuraLight:0xfde3ea, sakuraDeep:0xe8a2b8, sakuraPale:0xfff2f6,
  ground: 0x7f8a5c, grass:0x6f8a4d, grassDark:0x5b7440,
  dirt:0x9c8763, road:0xb0a086, roadDark:0x9a8a70, gravel:0xc4bda9,
  water: 0x2f5a63,
  roofTown: 0x4a4a52 ...
};
```
Add slight per-instance jitter: `tint(color, ±0.05)`.

### Geometry registry
```js
const GEO = {};
GEO.box = new THREE.BoxGeometry(1,1,1);
GEO.boxB = new THREE.BoxGeometry(1,1,1); GEO.boxB.translate(0,0.5,0);
GEO.taper = makeTaper(0.9);  // bottom at y=0, unit 1x1x1, top scaled
GEO.cyl = new THREE.CylinderGeometry(0.5,0.5,1,8); GEO.cyl.translate(0,0.5,0);
GEO.cyl6 = ...
GEO.cone = new THREE.ConeGeometry(0.5,1,4); rotateY(PI/4); translate(0,0.5,0)
GEO.octa = new THREE.CylinderGeometry(0.5,0.35,1,8)
GEO.sphere8 = new THREE.SphereGeometry(0.5, 6, 4) // for blossom blobs? maybe use icosahedron for low-poly foliage
GEO.ico = new THREE.IcosahedronGeometry(0.5, 0) // low poly blob
GEO.dodeca = new THREE.DodecahedronGeometry(0.5,0)
```
For blossoms, a mix of icosahedron and boxes gives a nice blocky-but-organic cluster. The prompt says "blocky or low-poly blossom clusters" — so icosahedra scaled non-uniformly + some boxes. 

makeTaper: build a BufferGeometry from vertices:
bottom rect (-0.5,0,-0.5)...(0.5,0,0.5); top rect scaled by s at y=1. Build 6 faces (4 sides + top + bottom) with proper normals via computeVertexNormals with non-indexed geometry for flat shading. Easiest: create non-indexed with explicit triangles then computeVertexNormals.

Let me write a helper `quad(a,b,c,d)` pushing triangles.

### Builder

```js
class Builder{
  constructor(){ this.b={}; }
  _b(key){ if(!this.b[key]) this.b[key]={geo:null,mat:'',m:[],c:[],s:[],n:[]}; return this.b[key]; }
  push(key, mat, mtx, col, struct){ const k=this._b(key+'@'+mat); ... }
}
```
Key = geoName + '@' + materialName. Store matrix elements (16 floats) and color (3 floats) and structId.

Simplify API: `B.add(geo, mat, pos{x,y,z}|(x,y,z), size{x,y,z}, rotY, color, structId)`.

I'll write:
```js
add(geoName, matName, x,y,z, sx,sy,sz, ry=0, color=0xffffff, tilt=null)
```
Compose matrix: use a reusable THREE.Matrix4 + Quaternion from Euler(rx,ry,rz).

Also `addBox(...)` shorthand.

At flush: for each bucket create InstancedMesh(geo, mat, count), set matrices & colors, computeBoundingSphere, add to scene, record bucket structIds array for picking, and register in pickables if mat is pickable.

Structures registry:
```js
const structures = []; // {id, name, category, min, max, detail}
function newStruct(name, category, detail){...}
```
Each builder call can pass a struct id; we also track bbox per struct by updating min/max on each push (cheap-ish: 30k * few ops, fine).

### Terrain

Grid: `const CELL=2.4, GN=104;` extent = 249.6. Let's use CELL=2.5, GN=100 → 250 units. half = 125.

Height function:
```js
function baseH(x,z){
  let h=0;
  // base plain gentle noise
  h += 1.2*Math.sin(x*0.035+1.1)*Math.cos(z*0.031-0.4) + 0.6*Math.sin(x*0.09-2.0)*Math.sin(z*0.07+0.7);
  // castle hill
  const d = Math.hypot(x*1.0, z*1.05);
  h += hillProfile(d);
  // shrine hill at (72, -18)? 
  ...
  // river to the east/south
}
```
Castle hill profile: want plateau steps. 
```
function hillProfile(d){
  const R=70;
  if(d>R) return 0;
  let t = 1 - d/R; // 0..1
  let v = Math.pow(t, 0.85);
  return 26*v;
}
```
Then quantize: for the castle region, quantize to steps of 2.2 → terraces. Actually quantizing the whole hill creates concentric terraces — exactly the look of Japanese castle earthworks (kuruwa). 

But we also need the moat: a ring depression at d in [66, 80]. Inside d<62 the hill rises. So:
- moat ring: d from 64 to 78 → depth -2.2 relative to surroundings.
- Outside d>78: town plain at ~1-3.
- Inside d<64: hill rising to 26 at center.

The transition from hill base (d=64, h≈26*(1-64/70)^0.85 = 26*0.086^0.85 ≈ 26*0.125 = 3.3) down to moat -2.2 then up to town 2.0. Good: moat is a real depression.

Hmm, but the hill at d=64 is only 3.3 — the steep rise happens closer in. Let's use R=62 for the hill so at d=62 h=0 and the moat ring at 64-78 sits on the plain. Then the hill base is right at the moat edge — the hill rises directly from the moat, which looks great (moat at the foot of the hill). But then the outer bailey (Sannomaru) needs to be inside... Let's restructure:

- Honmaru (innermost): top plateau at h≈24, radius ~26.
- Ninomaru: terrace at h≈14, radius ~42.
- Sannomaru: terrace at h≈7, radius ~58.
- Moat: ring 62..76, floor at h≈-1.8.
- Town plain: h≈1.5..3.5 outside.

Implement as a stepped profile function:
```js
function castleTerrace(d){
  if(d<24) return 24;
  if(d<40) return 15;
  if(d<56) return 7.5;
  if(d<61) return 4.5;
  if(d<77) return -1.8;  // moat
  return null; // outside
}
```
But hard steps everywhere look artificial; blend with smoothstep transitions over ~3 units, and add noise. Also the ring radii should be irregular (use a radius function of angle: R(θ) = R0 * (1 + 0.12*sin(3θ+..) + 0.06*sin(7θ))). That makes organic, non-circular baileys. 

So: `dEff = d / Rnorm(θ)` then compare to thresholds. Let me define:
```js
function ringR(theta, base){ return base*(1 + 0.10*Math.sin(3*theta+0.6) + 0.05*Math.sin(5*theta+2.1) + 0.03*Math.sin(2*theta+4)); }
```
Then `u = d / ringR(theta, 1)`... careful: define `f = d / (1 + 0.10 sin(3θ+...))` giving a "normalized distance", then compare f to thresholds 24, 40, 56, 61, 77.

Then smooth transitions: use smoothstep between levels over ~4 units of f. And add small noise (±0.5) except in flat plateau interiors where we want flat-ish ground for buildings (quantize plateaus to 0.8 steps? Actually plateaus should be fairly flat with gentle variation).

Also the moat should be a proper channel; the water surface at y = -0.6, moat floor at -2.2.

Then town terraces: outside the moat, add district plateaus: e.g., the samurai district on a slight rise (h+2), warehouse district near the water level (h+0.5). Use rectangular plateau functions with smoothstep edges and quantization to 1.0 steps for a terraced look.

Also a river: from the moat at the east, a river flowing out to the south-east edge, at h=-1.5, width ~10, with the town around it. Nice for composition (bridges over it). Let's add a river along a polyline.

Then final: quantize heights to 0.5 steps for the voxel look? Full quantization everywhere gives a strong stepped look. Let's quantize to 0.5 with a tiny per-cell dither of ±0.15 for texture. Hmm, dither on the top surface creates noise; better to keep quantized clean and vary the cap color instead.

Actually a fully quantized heightfield with 0.5 steps and 2.5 cells gives nice "rice terrace" micro-relief. But roads need smoothness. I'll do: compute continuous height, then quantize with `Math.round(h/0.6)*0.6`, then smooth the quantized field slightly near roads? Let's instead: roads flatten — for cells within road corridor, set target = smoothed road height (diffused), then quantize. Buildings flatten their footprint to a single height (no quantization issue).

I think it'll look good. Let me also add "retaining wall" caps: where a cell's height differs from a neighbor by more than ~1.2, the side faces show. To make the terraces read as stone walls in the castle area, set the cap color to stone and add a row of instanced stones along the terrace edge (the castle plateaus get stone walls; town edges get wooden fences/hedges). That's a nice detail: detect edge cells (height diff > 1.0) in the castle region and place stones along them. Could be many stones; limit by sampling every other cell.

Simpler and more controlled: explicitly build stone retaining walls along the defined terrace rings (using the same ring radius function) at known radii — I can generate a wall polyline along the ring at angle steps, with the wall base at the lower terrace height and top at the upper terrace height. That gives clean, hand-placed ishigaki following the irregular ring. 

Let me do that: `buildStoneWallRing(radiusBase, fromAngle, toAngle, topH, bottomH)` — but the terrain height varies; better to sample terrain: at each step along the ring, sample terrain height at r+1.5 (upper) and r-3 (lower)... Hmm, the ring is at the terrace edge. Let me define the wall along the ring at radius R_ring where the terrain drops outward. Sample `hIn = terrainHeight(x + n*3, z + n*3)` (inside, higher) and `hOut = terrainHeight(x - n*3, z - n*3)` (outside, lower). Build stones from hOut to hIn along the ring with batter. That's robust and adapts to actual terrain. 

I'll write a generic `buildIshigaki(path, {height, inwardOffset, ...})` that samples terrain.

OK. And the terrain itself: since the terrain is quantized, the wall sits on top of it and the visual gap is hidden by the wall's own stones extending down.

### Roads

Define road segments as arrays of points. Let me lay out:

Landmarks:
- Castle center (0,0). Main gate at south: at radius ~59 along +Z → point (0, 59) roughly; but the ring is irregular. Let me force the gate to be at (0, GATE_Z) where GATE_Z ≈ 58, and locally flatten/normalize the ring near the gate. To keep it simple, I'll make the ring radius function have a "notch" near θ=90° (south, +Z) so the wall line is straight there. Alternatively, place the gate exactly on the ring polyline computed from the function — I'll compute the wall ring polyline first, then find the point nearest to (0, +Z) and place the gate there, orienting it along the local tangent. That's robust. 

Let me define the wall ring as a polyline: `ringPath(baseR, steps)` returns points. Then the gate is placed at the path point nearest to direction (0,1). Then the bridge extends outward along the outward normal, crossing the moat, landing on the town side where the main south road begins. 

Main roads:
1. `saiden`: from bridge outer end straight south to (bx, 120) — actually along the direction of the bridge axis. Since the bridge is at the nearest point to +Z, it's approximately (0, ~78) → south.
2. `ring road` at r≈84 (outside the moat) — a loop, but broken where the town extends. Let's make a full ring road outside the moat (r=84) — classic castle town "sotobori" road.
3. From the ring road, 4-6 radial roads going out to the world edges (south, east, west, SE, SW).
4. A grid in the merchant/residential district: streets parallel/perpendicular to the main road, offset slightly, with a diagonal street for interest.
5. Shrine approach: from the east ring road to the shrine hill.
6. Market plaza: a rectangular open area at, say, (30, 95) with stalls.

Districts:
- Merchant (south main road, both sides, x in [-40,40], z in [80,125])
- Market plaza (east of main road, around (34, 92))
- Residential (east: x in [45,110], z in [40,120])
- Samurai (west: x in [-110,-45], z in [20,100]) — larger lots, hedges, gates
- Warehouse (east near river, x in [70,110], z in [-10,45])
- Shrine (NE hill at (62,-30)? or east at (78, 20)). Let's put the shrine on a hill to the east-northeast at (70, -8) with an approach road from the ring road.

Hmm, town should extend south and east primarily. So: shrine to the east at (74, 12) on a small hill; temple to the southeast. Samurai district to the west (that's traditional). Warehouse near the water on the east/southeast.

Let me finalize the map (x east+, z south+):
- Castle: r<62.
- Moat: 62..78 ring.
- Ring road: r≈84.
- Main S road: x≈0, z from 78 → 126.
- Market plaza: center (30, 96), size 34x26.
- Merchant district: along main road x∈[-42,42], z∈[80,126].
- East main road: from ring at (84,0) east to (124, 6) — shrine approach continues to shrine at (96, 14)? Let's put shrine hill centered at (98, -6) hmm that's near the edge (half=125). Shrine at (92, -10) with approach from ring road going NE.

Hmm, maybe better: keep the shrine closer so it's visible near the castle: shrine hill at (78, -34) — northeast, on a rise of +8 above plain, with a torii gate at the approach and steps. Visible from the isometric hero view along with the castle. 

- Residential district: east-south-east, x∈[50,118], z∈[30,118].
- Samurai district: west, x∈[-118,-46], z∈[10,105].
- Warehouse district: south-east near river, x∈[60,110], z∈[70,115].
- River: enters moat at east, flows SE to the edge. Actually simpler: a river from the north-east flowing into the moat? Water flow doesn't matter visually. Let's have a river from the moat's SE point flowing SE to the world edge, width 9. Bridges over it.

Total buildings target ~140. Placement along roads will produce that; I'll tune counts.

### Building archetypes

`makeHouse(B, opts)` with params: w, d, floors(1-2), roofType('gable','hip','irimoya','L'), wallColor, roofColor, style flags: plaster/wood, shopfront (with noren/awning), balcony, storehouse annex, fence, garden.

Detail components (reusable functions):
- `plinth(x,z,w,d,h)` — stone/wood base.
- `wallWithFrame(w,h,d, opts)` — plaster box + dark timber frame: vertical posts at intervals, horizontal beams at floor lines, corner posts, a shadow reveal.
- `windowsOnFace(...)` — small dark boxes + lattice (2 thin bars) + protruding bay window for 2nd floor (mushi-kōko).
- `doorway(...)` — recessed dark box + sliding door panels + frame + step stone.
- `roofGable(w,d,h,opts)` — as described.
- `roofHip(w,d,h,opts)`.
- `roofIrimoya(w,d,h,opts)`.
- `eaveBeams(...)` — under-eave brackets.
- `ridgeOrnament(...)`.
- `railing(len, posts)` — for balconies.
- `fenceRun(path, n)` — posts + rails.
- `awning(w)` — striped boxes projecting over the shopfront.
- `noren` — small colored cloth boxes.
- `signboard`, `barrel`, `crate`, `cart`, `lanternStone`, `lanternPaper`, `well`, `woodpile`, `steppingStones`, `stoneLion`? etc.

### Keep design (hero)

Levels (from bottom):
1. L1: 22 x 18 footprint, height 9. Stone base below it (the honmaru platform).
2. Roof1: big hip roof with flared eaves extending 3 beyond walls.
3. L2: 18.5 x 15, height 7.
4. Roof2.
5. L3: 15 x 12, h 6.
6. Roof3.
7. L4: 11.5 x 9, h 5.
8. Roof4 (irimoya).
9. L5: 8 x 6.5, h 4.5.
10. Top hip roof with chigi + katsuogi + gold shachihoko on the ridge ends.

Plus: 
- Stone base (ishigaki) under the keep: sloped, ~7 tall, made of ~400 stones.
- Kotsube (small white wall with tile cap) around the top platform.
- Balconies (tairo) on levels 2 and 4 with railings.
- Windows: rows of rectangular and triangular (katomado) openings — approximate triangles with a small wedge or stepped boxes.
- Entrance porch (karahafu-like curved gable? A curved gable is hard in voxels; approximate with a stepped triangular pediment over the entrance with a small roof).
- Dark timber banding, corner posts, plaster reveals.
- Under each roof: a dark shadow band + bracket rows (tokyō) as small blocks.

Total keep instances: maybe 700-1000. Great for "hundreds of components".

### Yagura
- Tatsumi yagura (SE corner of inner bailey): 3 levels, hip roof, 12x10.
- Inui yagura (NW corner): 2 levels + attached long gallery (tamatsubure) to the keep! That's iconic. A covered corridor with a tiled roof connecting keep to a yagura.
- Waki yagura (west of keep): 2 levels, 10x8.
- Tsukimi yagura (moon viewing, attached to keep NE): 2 levels with an open balcony and a small hip roof.
- Gate towers: Otemon (main, 2 story), Karamon (covered gate, 1 story with curved roof), Toranomon? Let's have: Otemon (south main), Aoto gate? Provide 4 gates: Otemon (S), Kurobanda gate (E), Nishi-omon (W), and a water gate (NE).

That's ≥4 secondary yagura. 

### Walls
Ring walls along the inner bailey edge (r≈26 top plateau) and the second bailey (r≈42), plus the outer sannomaru wall at r≈58 (with gates). Each wall: ishigaki + plaster + timber + tile cap roof.

### Water
Moat water: build as a ring mesh. Easiest: since terrain cells in the moat are below water level, place a single large plane at y=waterLevel but only where the moat is... A big plane would show over land. Options: build water per-cell (instanced thin boxes at water level for cells whose terrain height < waterLevel). That handles any shape automatically! Use instanced boxes 2.5x0.2x2.5 at y=waterLevel for each water cell. With a shader material using world position for ripples — need world coords; with instancing, use `instanceMatrix` to get world offset in the vertex shader. Doable: in the vertex shader, `vec4 wp = instanceMatrix * vec4(position,1.0); wp = modelMatrix * wp;`. I'll write a custom ShaderMaterial for water with instancing support (`#ifdef USE_INSTANCING` handled manually). Simpler: write the shader to always use instanceMatrix (the water bucket always has it).

Water shader: 
- base deep color, fresnel to sky color, animated ripple bands using sin(wp.x*0.3 + t) etc., specular highlight toward the sun, plus a subtle "shore foam" where depth is small (can't easily know depth). Add reflection of the castle as a fake: a vertical gradient? Skip.
- Also add darker patches near edges by using a "depth" attribute? We can pass per-instance the terrain depth (waterLevel - cellHeight) as an instance color! Then color mix by depth. Nice: shallow = lighter teal, deep = dark blue. 

Let's do that: water bucket uses instanceColor = depth-based tint, and the shader reads `vColor`.

Also add a few water lily/lotus pads? Maybe reeds along the moat edges (thin green boxes) — nice detail. Yes: reeds instanced along moat edges.

### Trees

Sakura: trunk (tapered cylinder, 2-3 segments with slight lean), 4-7 branches (cylinders rotated), then 25-45 blossom blobs (icosahedron scaled 1.2-2.6, colors from sakura palette, positioned in a dome/cloud with a slightly flattened top and gaps). Total per tree ~35 instances. 80 trees → 2800 instances. Good.

To make them read as lush from above, cluster blobs in a hemisphere of radius 3-5 for big trees.

Green trees: 
- Pine (matsu): stylized Japanese pine — trunk with 2-3 bends + 3-5 flat cloud layers of icosahedra (dark green). Very fitting for castles.
- Deciduous: trunk + 3-5 blobs.
- Maple small.
- Bamboo grove: thin tall cylinders in clusters + small leaf boxes. Nice for a district corner.

Shrubs: small icosahedra; hedges: rows of boxes (instanced) for samurai gardens.

Grass tufts: tiny boxes scattered — maybe too many instances; use a limited count (1500) in a single bucket. Could be nice for close-up detail. Let's add ~1200 small grass tufts near paths and gardens only.

Petals: THREE.Points with canvas sprite, ~1200 particles drifting in a slow deterministic flow, concentrated over the town. Beautiful.

### Props placement logic
- Lanterns: along the main road every ~14 units on both sides (stone lanterns), plus rows at the shrine, market, and gate.
- Fences: around samurai lots, castle base, market, shrine.
- Crates/barrels: near merchant storefronts and warehouses.
- Carts: on the market plaza and main road occasionally.
- Well: in a residential courtyard.
- Wood piles: behind houses.
- Stepping stones: in gardens and shrine approach.
- Signboards & banners: on merchant buildings (part of the archetype).
- Awning: shopfront buildings.
- Stone paving: main road surface cells colored gravel + occasional paving stones (instanced flat boxes) — nice detail.

### Camera presets (computed after build)
1. `hero`: pos (110, 78, 150) target (0, 26, 10) — SE high, sees front + town.
2. `front`: pos (6, 14, 150) target (0, 24, 40) — across the moat at low height, bridge in foreground.
   Better: pos (0, 12, 132), target (0, 26, 20).
3. `street`: place at the main road south, y = terrain+2.5, looking north toward the castle down the street. pos (2, groundY+3, 118) target (0, groundY+8, 40).
4. `sakura`: pos near a big sakura cluster with the castle behind: e.g., pos (-38, 12, 74) target (2, 30, 20) — foreground blossoms.
5. `top`: pos (0, 230, 30) target (0, 0, 12).
6. `shrine`: view of the shrine with castle in the background.
7. `aerial-west`: cinematic.

I'll compute street preset from actual road data.

### Picking categories
Assign struct categories when building: 'Castle Keep', 'Gate', 'Yagura', 'Wall', 'House', 'Merchant Building', 'Warehouse', 'Samurai Residence', 'Shrine', 'Temple', 'Market Stall', 'Cherry Tree', 'Tree', 'Bridge', 'Lantern', 'Torii', 'Prop', 'Terrain'.

Info panel shows name + category + a flavor description + component count.

### FPS counter
Simple rolling average.

### Draw call / instance count
`renderer.info.render.calls`, and total instances tracked by builder.

Now, let me write the actual code. I need to be careful and complete. This will be long — maybe 2500+ lines. Let me write it carefully in one pass, then mentally review.

Let me start writing.

```html
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sakura-jō — Castle Town in Spring</title>
<style> ... </style>
</head>
<body>
<canvas id="scene"></canvas>
<div class="ui"> ... </div>
<script type="importmap">...</script>
<script type="module"> ... </script>
</body>
</html>
```

CSS: glassy panels, sakura accent (#f2a7bd), dark ink (#12131a). Title with vertical Japanese text accent maybe. Buttons with hover.

Let me write the JS now in detail.

```js
import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
import { EffectComposer } from 'three/addons/postprocessing/EffectComposer.js';
import { RenderPass } from 'three/addons/postprocessing/RenderPass.js';
import { UnrealBloomPass } from 'three/addons/postprocessing/UnrealBloomPass.js';
import { OutputPass } from 'three/addons/postprocessing/OutputPass.js';
```

RNG + helpers.

```js
const SEED = 1337;
let _s = SEED >>> 0;
function rnd(){ _s |= 0; _s = _s + 0x6D2B79F5 | 0; let t = Math.imul(_s ^ _s>>>15, 1|_s); t = t + Math.imul(t ^ t>>>7, 61|t) ^ t; return ((t ^ t>>>14)>>>0)/4294967296; }
const rr=(a,b)=>a+(b-a)*rnd();
const ri=(a,b)=>Math.floor(a+(b-a+1)*rnd());
const pick=a=>a[Math.floor(rnd()*a.length)];
const chance=p=>rnd()<p;
```

Noise: simple value noise with hashing (deterministic).
```js
function hash2(x,y){ let h = x*374761393 + y*668265263; h = (h ^ (h>>13)) * 1274126177; return ((h ^ (h>>16))>>>0)/4294967295; }
function vnoise(x,y){ const xi=Math.floor(x), yi=Math.floor(y); const xf=x-xi, yf=y-yi;
  const u=xf*xf*(3-2*xf), v=yf*yf*(3-2*yf);
  const a=hash2(xi,yi), b=hash2(xi+1,yi), c=hash2(xi,yi+1), d=hash2(xi+1,yi+1);
  return (a*(1-u)+b*u)*(1-v)+(c*(1-u)+d*u)*v; }
function fbm(x,y,oct=3){ let s=0,a=0.5,f=1; for(let i=0;i<oct;i++){s+=a*vnoise(x*f,y*f); f*=2; a*=0.5;} return s; }
```

Note: hash2 with negative x — Math.imul handles ints; use `x|0`. Fine.

### Terrain module

```js
const CELL=2.5, GN=100, HALF=CELL*GN/2; // 125
const terrainH = new Float32Array(GN*GN);
const terrainT = new Uint8Array(GN*GN); // surface type
const occupied = new Uint8Array(GN*GN);
const idx=(i,j)=>j*GN+i;
const cx=i=>-HALF+i*CELL+CELL/2;
const iz=j=>-HALF+j*CELL+CELL/2;
const gi=x=>Math.floor((x+HALF)/CELL);
```

Surface types: 0 grass,1 dirt,2 road,3 stone,4 plaza,5 gravel(shrine),6 sand,7 water,8 wood boardwalk.

Height:
```js
function ringNorm(theta){ return 1 + 0.09*Math.sin(3*theta+0.7) + 0.05*Math.sin(5*theta+2.3) + 0.035*Math.sin(2*theta-1.1); }
function castleBase(x,z){
  const d=Math.hypot(x,z); if(d<0.001) return 0;
  const th=Math.atan2(z,x);
  const f=d/ringNorm(th);
  // smooth stepped profile
  const steps=[[26,24.5],[42,15.5],[57,8.0],[62,4.2],[79,-2.0]];
  ...
}
```
Implement as piecewise with smoothstep between control points:
```js
function profile(f){
  const pts=[[0,25],[24,25],[26,24],[30,16],[41,16],[43,15],[47,8.5],[56,8],[58,5],[62,4],[64,-1.6],[77,-1.8],[79,0.6],[84,2.0]];
  // linear interp with smoothing
}
```
Let me define control points (f, h) and interpolate with smoothstep between consecutive points:
```
[0,25],[23,25],   // honmaru plateau
[26,16.5],[28,16.5], // drop
[39,16.5],  // ninomaru plateau
[42,9],[44,9], // drop
[55,9],  // hmm
```
Let me restructure cleanly:
- honmaru: f 0..24 → h 25
- slope 24→27 down to 16
- ninomaru: 27..40 → 16
- slope 40→43 down to 8.5
- sannomaru: 43..58 → 8.5
- slope 58→62 down to 3.0
- berm/counterscarp 62..64 → 2.0
- moat: 64..76 → -2.2 (with a slight bowl: -2.6 at center)
- outer slope 76..80 → up to 2.2
- plain: >80 → 2.2 + noise

Water level: -0.4. Moat floor -2.6. Good depth.

Then add noise: outside the castle (f>80) add `fbm*2.2 - 0.6` and district plateaus. Inside plateaus add small noise (±0.35) but keep flat-ish.

District plateaus (outside moat): 
```js
function districtH(x,z){
  let h=0;
  h += plateau(x,z, -80, 55, 70, 60, 3.0);  // samurai rise (west)
  h += plateau(x,z, 92, -10, 34, 34, 7.5);  // shrine hill (east)
  ...
}
function plateau(x,z,cx,cz,rx,rz,hgt){ const dx=Math.abs(x-cx)/rx, dz=Math.abs(z-cz)/rz; const d=Math.max(dx,dz); if(d>=1) return 0; const t=smoothstep(1,0.72,d); return hgt*t; }
```
For the shrine hill, add a conical shape: `hgt*(1-d)` with steps → terraced hill with a flat top. Use `Math.min(hgt, ...)`.

Also a river: define a polyline; cells within width → h = min(h, -1.6) and type water. River from the moat's SE (around (55,55) direction) heading SE to the map edge. Let's define river path points: [(60,58),(78,72),(92,92),(104,124)] with width ~9 tapering. Hmm, the moat at 45° is at f≈70 → d≈70*ringNorm. Let's just start the river at the moat edge computed as point on ring at 45°: d = 70*ringNorm(45°) ≈ 70*1.05 ≈ 73 → point (52,52). River path: (52,52) → (72,66) → (90,88) → (100,125). Width 10.

Then the town's SE warehouse district sits near the river with a bridge.

Also maybe a small pond in a garden. Skip.

Road flattening: after computing base heights, for road cells set target height and diffuse. Implementation:
1. Compute `h0` for all cells (base terrain).
2. Compute road mask (distance to road polylines < w/2).
3. For road cells: `h[i] = average of h0 in a 3x3 (or 5x5) window` — precompute a blurred version of h0 and assign to road cells. Then do 2 iterations of smoothing restricted to road cells + their immediate neighbors.
4. Also flatten building footprints.
5. Quantize: `h = Math.round(h/0.5)*0.5` but only outside roads? Quantizing roads too is fine if the steps are small (0.5) — a road would have tiny steps, which looks voxel-appropriate. Yes, quantize everything to 0.5. Actually for smooth roads, quantization creates visible steps along the road. With 0.5 steps over 2.5 cells, a road climbing will look like a staircase from the side but fine from above. It's a voxel diorama — stairs are on-brand. Keep it.

Hmm, but buildings placed on quantized terrain then get a foundation. Fine.

Water: cells where h < WATER_Y (-0.4) → type water, and we place water instances.

Also: the moat must not be blocked by roads — bridges cross it. Road cells in the moat (bridge approach) would be flattened to road height, creating a causeway. Need to exclude the moat from road flattening: if a cell is in the moat ring (f between 64 and 77), keep it low; bridges are separate objects placed above. So: road mask excludes moat cells. And the bridge deck spans from the shore to the gate.

Let me define `moatMask` and apply after everything: `if(moat) h = min(h, -2.2 + bowl)`.

Order:
1. h = base terrain (castle profile + districts + noise)
2. river carve
3. road flatten (blurred heights on road cells)
4. building flatten (done during placement — but placement needs heights... chicken-and-egg. Solution: place buildings using heights from step 3 (sampled), record footprints, then apply flatten, then quantize.)
5. moat mask (force low) — but must not override building flatten; buildings aren't in the moat anyway.
6. quantize.

Good.

Terrain mesh build:
For each cell: 
- column box from y = bottomY to h. bottomY = min(h - 1.0, -4) — just use a fixed bottom of -5 for all so sides look deep; but then columns overlap in height variance — fine, they're boxes; overlapping is invisible. Use bottom = -6 always, so column height = h+6. For water cells, the column top is the floor.
- cap: thin box at top (h to h+0.18) with surface color. Cap slightly larger than cell (CELL+0.02) to avoid seams? Overlapping caps cause z-fighting on the sides but tops are coplanar... Adjacent caps at the same height with size CELL+0.02 overlap by 0.02 — coplanar top faces at exactly the same y → z-fighting! Better: cap exactly CELL size, and columns CELL-0.05 (small gaps hidden by cap overhang). Hmm, gaps between columns show dark lines — actually that's a nice voxel look. Let's do: column size CELL*0.99, cap size CELL*1.0 exactly, cap top at h+0.16. Adjacent caps share edges exactly (no overlap, no gap). Coplanar tops at the same height but distinct faces — no z-fighting since they don't overlap. Good.

Cap colors by type with per-cell noise variation. Road cells: gravel color with variation; add paving stones occasionally.

Also: to reduce instance count, skip columns whose height is tiny and surrounded by same-height neighbors? Not needed.

Actually 100x100 = 10000 columns + 10000 caps = 20000 instances. Shadow casting from terrain: let's set terrain columns castShadow=false, receiveShadow=true; caps castShadow=false, receiveShadow=true. Hills won't cast shadows on the town, but the castle will cast onto them. Acceptable and saves perf. Hmm, but the castle hill's shadow on the town would be nice... The keep's shadow will cover a lot anyway. Let's enable castShadow on caps only? Caps are flat boxes; a hill made of caps at different heights — the caps would cast shadows of their own footprint only, not the cliff faces. Not useful. Let's enable castShadow on columns too and see; 10k extra boxes in the shadow pass is fine. Actually the shadow camera renders all instances regardless of whether they're in view. 20k boxes = 240k triangles — trivial for a GPU. Enable both.

Hmm, but self-shadowing artifacts (acne) on the terrain. Use normalBias ~0.8 and bias -0.0005. Should be OK.

### Roads definition

```js
const roads=[]; // {pts:[[x,z],...], w, type:'main'|'street'|'alley', name}
function addRoad(pts,w,type,name){roads.push({pts,w,type,name});}
```
Build the network:
- `ring`: circle at r = 84*ringNorm(θ), 40 segments, w=9.
- `saiden`: from bridge outer landing (bx,bz) south to (bx, 126), w=11.
- `mainE`: from ring at east (θ=0) to (122, 4), w=9.
- `mainW`: from ring west to (-122, -6), w=8.
- `mainSE`: from ring at 45° to (110,120), w=8.
- `mainSW`: from ring at 135° to (-108,112), w=8.
- merchant grid: streets perpendicular to saiden at z = 88, 100, 112, 124 spanning x∈[-46,46], w=6 (alleys w=4).
- east residential grid: streets at x=60, 80, 100 from z=40..120 (w=6), cross streets at z=52,72,92,112 (w=6).
- samurai district: fewer, wider-spaced streets w=7 with big lots: x=-60,-84,-106 from z=16..100; cross at z=40,66,92.
- shrine approach: from ring at θ≈-30° (NE) to shrine base, then a straight axis to the shrine steps, w=7, type 'approach' (gravel).
- market: the plaza is an open rect; roads around it.

I need to make sure roads don't run through the moat/castle. All town roads are at d>80 mostly. The ring at 84 is fine.

Also paths inside the castle courtyards (gravel/stone).

### Building placement

```js
function tryPlace(cx,cz,w,d,ry, allowRoadAdj){ ... check cells ... }
```
Occupancy: mark cells within the rotated rect. Use a simple approach: compute the rect's AABB in cell space, test each cell center against the rotated rect (transform into local space). Mark occupied.

Also keep a "clearance" of 1 cell around buildings for alleys? Roads already excluded via road mask (buildings can't be placed on road cells). Add margin: check cells within the rect expanded by 0.6.

Placement along a road segment:
```js
function lineBuildings(seg, opts){
  // for each side
  for(const side of [-1,1]){
    let t = opts.start ?? 0;
    while(t < len - 6){
      const bw = rr(opts.wMin,opts.wMax), bd=rr(opts.dMin,opts.dMax);
      const off = seg.w/2 + bd/2 + rr(0.5,2.5);
      const p = pointAt(t + bw/2), n = normal*side;
      const x = p.x + n.x*off, z = p.z + n.z*off;
      if(fits(x,z,bw,bd,ang) && !tooCloseToOtherRoad(x,z,bw,bd)) { place(...) }
      t += bw + rr(opts.gapMin,opts.gapMax);
    }
  }
}
```
`tooCloseToOtherRoad`: ensure the building doesn't cover other road cells (fits() checks occupancy but roads aren't in occupancy). So add road cells to the occupancy grid as "blocked" (value 2) before placing buildings. Then fits() rejects. 

But then buildings along road A might be blocked by road B's cells — correct behavior.

Also mark moat/water cells as blocked.

After placing all buildings, we have ~140. I'll tune parameters and count; if too few, add infill pass: scan cells in district rects, place small buildings where free.

District config: each district has a list of roads with building style params.

### Building archetypes implementation

Let me write `buildMachiya(B, o)` etc. Common core:

```js
function townBuilding(B, o){
  // o: {x,z,ry,w,d,floors,style,roofColor,wallColor,...}
  const y = o.y; // base ground (already flattened top)
  // foundation
  // ground floor walls
  // upper floor (inset by 0.4)
  // roof
  // details
}
```

Styles:
- 'merchant': plaster upper, wood lower with shopfront (open front with lattice, awning, noren, signboard), gable roof, maybe a second story with a balcony and a `mushikoko` window band.
- 'machiya': narrow, 2 story, deep, tiled roof, fireproof storehouse at the back (white plaster box with a small hip roof).
- 'house': 1 story with a hip-gable roof, fence, garden, varied roof color, small entry.
- 'samurai': walled compound: perimeter wall/hedge + gate + main hall with hip roof + storehouse + garden with stepping stones and a lantern.
- 'warehouse': plaster box, dark timber frame, small windows, heavy tiled roof with thick eaves, loading door, near the water.
- 'workshop': open-sided with a big roof, smoke stack? (kiln), tools.
- 'teahouse': small, elevated floor, veranda, thatch-ish roof (use a warmer brown roof color), near a garden.
- 'shrineBuilding': raised floor, hip roof with chigi/katsuogi, railings, steps.
- 'templeBuilding': 2-story gate, big hip roof, curved eaves, pillars, stairs.

Roof colors: tiles dark slate; some houses with thatch (warm brown) or bark shingle (grey-brown). Variation by district.

### Roof functions (detailed)

```js
function roofGable(B, {x,y,z,w,d,h,ry,tile,ridgeCap=true,overhang=0.55,layered=false,thatch=false}){
  // local space: width along x, depth along z, ridge along x
  // Build with a rotation group: we can't rotate groups in the builder, so compute rotated positions manually.
}
```
Problem: the builder places instances with a single rotY. For a roof with rotated slabs, I need to compose the roof's local rotation with each part's local offset. Solution: implement a small transform helper:

```js
class Xf {
  constructor(){ this.m = new THREE.Matrix4(); }
}
```
Better: give the builder a "current transform" stack: `B.push(xform)` / `B.pop()` where xform is a Matrix4 that is multiplied with each part's local matrix. Then all sub-builders work in local space. 

```js
B.pushT(x,y,z,ry) // translate+rotate
B.pushM(matrix)
B.pop()
```
Internally: `topMatrix`. On add: `final = top * local`. 

This makes everything much cleaner. Let's implement:
```js
const _m1=new THREE.Matrix4(), _m2=new THREE.Matrix4(), _q=new THREE.Quaternion(), _e=new THREE.Euler(), _v=new THREE.Vector3();
class Builder{
  constructor(){ this.stack=[new THREE.Matrix4()]; }
  push(){ const m=this.stack[this.stack.length-1].clone(); this.stack.push(m); return m; }
  translate(x,y,z){...}
  rotateY(a){...}
  pop(){ this.stack.pop(); }
  add(geo,mat,x,y,z,sx,sy,sz,rx,ry,rz,color,struct)
}
```
Use `Matrix4.compose(pos, quat, scale)` for local then multiply by top.

I'll write a fluent helper:
```js
B.push(); B.pos(x,y,z); B.ry(a);  // modifies top
... parts in local coords ...
B.pop();
```
where `B.pos(x,y,z)` does `this.top.multiply(new Matrix4().makeTranslation(x,y,z))`.

Good.

Roof construction (local, ridge along X, width w along X, depth d along Z, base at y=0):

`roofGable(w,d,h,opts)`:
- Two slopes: each slope is a slab rotated about the X axis by angle ±a, where tan(a) = h/(d/2 + overhang). Slab length L = sqrt(h² + (d/2+oh)²). Slab thickness t (0.35). Position: center at (0, h/2, ±(d/2+oh)/2) rotated by ∓a... Let me define: slope goes from ridge at (0,h,0) to eave at (0,0,±(d/2+oh)). Direction vector (0,-h,±(d/2+oh)). The slab's local Y is its thickness; we want the slab's local Z axis along the slope direction. Use rotation about X: for the +Z slope, rotate about X by angle θ where the slab (a box of size (w+2*oh, t, L)) is rotated so its Z axis points along (0,-h,e). Rotation about X by angle φ maps Z→(0,-sinφ,cosφ). We need (0,-sinφ,cosφ) ∝ (0,-h,e)/L → sinφ=h/L, cosφ=e/L → φ=atan2(h,e). So rotate X by +φ for the +Z slope, and -φ for the -Z slope. Position center at (0, h/2, ±e/2).
- Eave lip: a box at the eave end, slightly larger, darker (fascia board), plus a row of "rafter tails" (small boxes) under the eave at intervals — great close-up detail.
- Ridge cap: box (w+2*oh+0.3, 0.35, 0.9) at (0,h+0.1,0), darker tile color; plus a second thinner cap above.
- Gable ends (kirizuma): the triangular gable wall — approximate with 3-4 stacked boxes of decreasing width (stepped triangle) in plaster color, inset slightly. Or use a custom triangle geometry. Stepped boxes read as voxel; but for a clean look, I'll build the gable as a few stacked boxes.
  Actually for a gable roof on a rectangular house, the wall itself must rise to a triangle. I'll build the wall as: main box up to eave height, then 3 stacked boxes forming the triangle (widths w, 0.66w, 0.33w). Plaster with a timber frame line. Good.
- Optional layered eave: add 1-2 stepped courses under the eave edge (boxes running along the slope bottom) to thicken the eave.

`roofHip(w,d,h,opts)`:
- 4 slopes. Use rotated boxes for the two long sides (trapezoid-ish) and triangular-ish ends. Approximation with boxes: 
  - Long sides: slabs as in gable but shorter in X (length w - 2*setback at the top... the top ridge is shorter than w). Use a slab of width (w - (d/2+oh)*? ) hmm.
  - Simplest robust approach: build hip roofs from stacked plates (stepped pyramid). For layer i=0..n-1: plate size (w - i*dw, t, d - i*dd) at y = i*(h/n). With n=5, dw=w/(n+1)... The result is a stepped pyramid roof — reads well in voxel style, especially with a darker "lip" under each plate edge. Then a ridge cap on top (a short box along X).
  - To improve: make each plate's edge lip a separate thin box slightly larger and darker → strong horizontal tiling lines.
  - For a ridge (not a point), shrink X slower than Z: dw_x = (w - ridgeLen)/n, dw_z = (d)/n... For a hip with ridge length rl: plate i has w_i = w - i*(w-rl)/n, d_i = d - i*d/n (last plate d_n ≈ 0 → make it d*(1/n) so it's a ridge box). Good.
  
  Stepped hip roofs with 5-6 layers look great and are cheap. Use for hero roofs too (with more layers, e.g., 7, plus corner upturns and detailed eave brackets).

`roofIrimoya(w,d,h,opts)`: lower hip skirt (2-3 stepped plates flaring outward) + upper gabled section on top. This is the classic look for gates and keeps.

`roofKarafu`: skip (curved gable too hard) — approximate a "chidori" small roof over entrances.

Corner upturn: at each eave corner, add a small box raised by 0.15 and rotated slightly — subtle. For hero roofs, add "onigawara" (demon tile) blocks at ridge ends: a small box + a smaller box on top, gold/dark.

Chigi (crossed finials) for shrine: two thin boxes crossing at the ridge ends, tilted. Katsuogi: 4-6 short cylinders/logs along the ridge.

### Castle wall function

```js
function castleWall(B, path, {height, hasGate, ...}){
  // walk along path in steps of ~2.2
  // at each step: sample terrain inside/outside
  // build: ishigaki stones (sloped), plaster wall above, timber frame, tile cap roof, inner walkway
}
```
Let me define the wall cross-section (from outside to inside):
- Stone base from ground_out to (top_of_stone = ground_in - 0.5), sloped: outer face batter ~60°.
- Plaster wall: from ground_in to ground_in + wallH(≈5), thickness 1.6, with the outer face slightly sloped (use a tapered box or a few stacked boxes with inward offsets).
- Timber: vertical posts every 3 units, a horizontal beam at mid-height, a dark band at the base.
- Tile cap roof: a small hip/gable roof along the top, overhanging 0.7 both sides, dark tiles, with a stepped course.
- Inside: a walkway of gravel/stone (terrain type) and a wooden railing on the inner side.

For the plaster wall with batter, use the 'taper' geometry scaled: taper has top scaled 0.9 — for a wall of height 5 and thickness 1.6, the batter would be 0.08 total — too subtle. Make a dedicated geometry `wallBatter`: bottom 1x1, top scaled to (0.86, 1, 0.86)? For a long wall, only the thickness (z) should taper, not the length. So make a geometry that tapers only in Z: bottom z from -0.5..0.5, top z from -0.42..0.42 (i.e., top inset 0.08 on each side). Then scale x by length, y by height, z by thickness. The inset scales with thickness — for thickness 1.6, inset = 0.128 per side over height 5 → batter angle ~83°, realistic-ish. Good. Let me make the inset 0.12 per side (top z from -0.38..0.38) → for thickness 2 and height 5, inset 0.24 → ~76°. Reasonable.

I'll create `GEO.batt = batterBox(insetTopZ=0.12)` and `GEO.batt2` for stone walls with more batter (0.22).

### Ishigaki (stone wall) generator

```js
function stoneWallRun(B, path, opts){
  // path: array of {x,z} ; for each step along path:
  //   compute inside height (hIn) and outside ground (hOut)
  //   rows from hOut up to hIn, each row inset by 0.28 from the previous
  //   stones: width 1.6-2.6, height rowH*rr(0.8,1.0), depth 1.2-2.0
}
```
Position each stone at the path point offset outward by (rowInset) and rotated to face along the path tangent. Stone color: gray with variation, some warmer.

To make it look like a wall rather than a pile, stones per row should tile along the tangent: place stones at intervals of their width along the path, each rotated to the tangent, depth ~1.4 (so they overlap into the wall interior). Rows stack with slight random vertical offsets and horizontal offsets (running bond).

Count control: for the castle, total wall length maybe 900 units → at 2.2 per stone per row and ~6 rows average → 900/2.2*6 ≈ 2450 stones. Plus keep base ~400. OK, ~3000 stones. Fine.

Also scatter loose stones/boulders around the base.

### Courtyards
Inside the honmaru: gravel surface (set terrain type to stone/gravel in a radius), a few trees (pines), a well, a daimyo-style garden with raked gravel (rows of light boxes), stone paving, the keep, yagura, storehouses, a stage (noh stage?), a walled garden.

Ninomaru: a secondary courtyard with a gate, long galleries (tamatsubure), a garden with a pond? Maybe a small pond in the ninomaru garden — nice reflection of the keep. Yes! A garden pond in the ninomaru reflecting the keep would be gorgeous. Add water there (reuse water bucket).

### Now, the code volume. Let me start writing it out properly.

I'll write it in sections with clear comments. Let me be careful about correctness (no undefined vars).

Let me draft the code.

```js
/* ============ 1. CORE SETUP ============ */
const canvas = document.getElementById('scene');
const renderer = new THREE.WebGLRenderer({canvas, antialias:true, powerPreference:'high-performance'});
renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
renderer.setSize(innerWidth, innerHeight);
renderer.shadowMap.enabled = true;
renderer.shadowMap.type = THREE.PCFSoftShadowMap;
renderer.toneMapping = THREE.ACESFilmicToneMapping;
renderer.toneMappingExposure = 1.05;
const scene = new THREE.Scene();
const camera = new THREE.PerspectiveCamera(42, innerWidth/innerHeight, 0.5, 2000);
camera.position.set(120, 90, 170);
const controls = new OrbitControls(camera, renderer.domElement);
controls.enableDamping = true; controls.dampingFactor=0.06;
controls.maxPolarAngle = Math.PI*0.495;
controls.minDistance=8; controls.maxDistance=420;
controls.target.set(0,22,10);
```

Sky: 
```js
const skyUniforms = { topColor:{value:new THREE.Color()}, midColor:{...}, botColor:{...}, sunDir:{value:new THREE.Vector3()}, sunColor:{...}, sunSize:{value:0.9} };
const skyMat = new THREE.ShaderMaterial({side:THREE.BackSide, depthWrite:false, uniforms:skyUniforms, vertexShader, fragmentShader});
const skyGeo = new THREE.SphereGeometry(900, 32, 16);
const sky = new THREE.Mesh(skyGeo, skyMat); sky.frustumCulled=false; scene.add(sky);
```
Fragment: gradient by normalized direction y, plus sun glow disc, plus a few procedural clouds? Clouds via fbm in the shader would be lovely — cheap enough on a sky sphere. Let's add simple layered noise clouds using a hash-based noise in GLSL. That adds beauty. Keep it subtle.

Also fog: `scene.fog = new THREE.FogExp2(color, 0.0016)`. The sky sphere is affected by fog if the material has fog enabled — ShaderMaterial doesn't include fog by default, so the sky stays clean. Good, but then the horizon must match the fog color. Set fog color ≈ horizon color. Good.

Lighting:
```js
const hemi = new THREE.HemisphereLight(0xbfd8f2, 0x6b6248, 0.9);
const sun = new THREE.DirectionalLight(0xffe6c2, 2.4);
sun.castShadow=true; sun.shadow.mapSize.set(2048,2048);
const sc=sun.shadow.camera; sc.left=-150; sc.right=150; sc.top=150; sc.bottom=-150; sc.near=1; sc.far=600;
sun.shadow.bias=-0.0004; sun.shadow.normalBias=0.7;
const fill = new THREE.DirectionalLight(0xbcd4ff, 0.35); // sky fill from opposite
```
Also an ambient light for night.

Modes:
```js
const MODES = {
 day: {sunDir:[0.45,0.72,0.62] (normalized, coming from SE above), sunColor, sunInt, hemiSky, hemiGround, hemiInt, fog, fogD, sky:{top,mid,bot,sunGlow}, exposure, bloom, lanterns:false, ambient},
 dusk: {...}, night:{...}
};
```
Sun direction: for spring morning/afternoon, sun from the south-west so the castle front (south) is lit and shadows fall to the NE. Let's set sun position = (-90, 120, 90) direction-ish → light comes from SW-high. Hmm, the front view looks north from the south; if the sun is in the SW, the front is lit and the shadow goes NE (away from camera). Good.

For dusk: sun low in the west: position (-160, 34, 40) → long shadows across the town toward the east, warm rim on the castle. Beautiful.

Night: moon from the NE: (60, 120, -80), cool blue.

Transition: lerp over 1.5s using a tween of all numeric/color params.

PMREM env: generate from a temp scene with the sky mesh clone. Do it after each mode transition completes (or at the start of the transition using the target mode). Regenerating per frame during transition would be costly; do it at the transition start with the target sky params, and also update the sky material each frame. Slight mismatch during the transition is fine. Actually simpler: regenerate the env map at the end of the transition. Let's just regenerate whenever the mode changes (using target params) — set skyUniforms to target immediately? No, we want a smooth sky transition. Compromise: transition the sky uniforms smoothly, and regenerate the PMREM at the transition's end. Fine.

PMREM cost: `pmrem.fromScene(skyScene, 0, 1, 1000)` — renders a cubet + mip chain, maybe 5-15ms. Once per mode switch is fine.

Careful: PMREMGenerator.fromScene renders the given scene; the sky mesh must be inside. I'll create `envScene = new THREE.Scene(); envScene.add(new THREE.Mesh(skyGeo, skyMat))` — sharing the material means the env map reflects the current transition state. That's fine; regenerate at the end.

Hmm, one gotcha: `fromScene` with a huge sphere radius 900 and near/far params — signature `fromScene(scene, sigma=0, near=0.1, far=100)`. Default far=100 < 900 → the sphere would be clipped! Pass far=2000. Good catch.

### Water material

```js
const waterMat = new THREE.ShaderMaterial({
  transparent:true, uniforms:{uTime, uDeep, uShallow, uSky, uSunDir, uSunColor, uCamPos, uFog...},
  vertexShader: `...`, fragmentShader:`...`
});
```
Need fog support manually: compute fog factor in the fragment and mix with fogColor uniform. I'll add `uFogColor`, `uFogDensity` and apply FogExp2 formula. Also update these when the mode changes.

Vertex (instanced):
```glsl
attribute vec3 instanceColor; // provided automatically? 
```
With InstancedMesh + setColorAt, three defines USE_INSTANCING_COLOR and declares `attribute vec3 instanceColor;` in the common chunks — but for a raw ShaderMaterial, those chunks aren't included. I'd need to declare it myself, and three might also declare it → duplicate declaration error. Safer: don't use instanceColor; instead pass depth via `instanceMatrix`? No.

Alternative: use `THREE.RawShaderMaterial`? Then I control everything but must provide projection matrices as attributes/uniforms manually. Hmm.

Simplest: use a custom attribute on the geometry: `geometry.setAttribute('aDepth', new THREE.InstancedBufferAttribute(...))`. Wait, the geometry is shared across buckets... I'll create a dedicated geometry for water (a box) and add an InstancedBufferAttribute to it. Since only the water bucket uses that geometry, it's fine. In a ShaderMaterial, custom attributes are declared as `attribute float aDepth;` — no conflict. 

But careful: InstancedMesh sets `instanceMatrix` attribute automatically and the shader chunk `#include <begin_vertex>` + `<project_vertex>` handles it if `USE_INSTANCING` is defined. For ShaderMaterial, three defines USE_INSTANCING when the object is an InstancedMesh (yes — in WebGLProgram, `instancing: object.isInstancedMesh` is part