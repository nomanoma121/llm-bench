The user wants a large, polished Three.js scene in a single index.html. I need to plan carefully and write a substantial amount of code. I can't actually run a browser here (no execution tools available in this environment, I believe). So I must be extremely careful to write correct code the first time, since I can't inspect the result.

Let me think about architecture.

Key constraints:
- Single index.html
- Three.js from CDN (use importmap with three module + OrbitControls from unpkg/jsdelivr)
- Deterministic seed 1337
- No external assets
- Interactive, performant

Performance strategy: thousands of geometric elements. Use InstancedMesh heavily for repeated elements (stones, roof tiles, planks, lanterns, fences, blossoms, tree trunks). Merge geometries where possible using BufferGeometryUtils.mergeGeometries. Actually a great approach: build each "building" as a set of boxes, then merge into a single geometry per material group (e.g., one merged geometry for wood-colored parts, one for plaster, one for roof). That drastically reduces draw calls.

Plan: Use a "Builder" class that accumulates boxes (with position, size, rotation, color) and then produces merged BufferGeometry per material. Actually simpler: accumulate per-material arrays of box specs, then create merged geometry using THREE.BoxGeometry translated/rotated and merged via mergeGeometries. With thousands of boxes, mergeGeometries of thousands of BoxGeometry objects is fine at load time (maybe a few hundred ms).

Better: write a custom "BoxCluster" builder that pushes vertices directly into typed arrays — faster and gives full control (including per-vertex color). Let me do that: a builder that appends a transformed box into arrays (positions, normals, colors, uvs not needed). Then create BufferGeometry. Use vertex colors with a single MeshLambertMaterial / MeshStandardMaterial with vertexColors: true. That way one draw call per "group" (e.g., static town geometry) with all colors. That's very efficient.

But shadows: merged geometry still casts shadows fine.

Let me design a core primitive: `addBox(cx, cy, cz, sx, sy, sz, rotY, color)` writing 24 verts/36 indices. Also maybe `addPrism` (triangular wedge) for roofs, `addCylinder` for trunks/lantern posts.

Actually for roofs I want layered, curved silhouettes. Approach for Japanese roofs: build from stacked "layers" of thin boxes forming a hip-and-gable (irimoya) shape: each layer is a slightly smaller box ring... Hmm. A good voxel approach: build roof as a series of stacked slabs, each smaller than the one below, forming a stepped pyramid with overhang — that reads as a Japanese roof if we also add a thick eave edge (a darker, thicker slab at the bottom layer) and upturned corners (small boxes at corners raised slightly). Plus a ridge cap on top (a small elongated box, with ridge-end ornaments (giboshi) at ends).

To get the curved silhouette (concave slope), stack N layers where each layer's inset follows a curve: inset_i = totalInset * pow(i/N, 0.75) maybe, and layer heights decreasing. Actually for concave Japanese roofs, the slope is steep near the ridge and shallow at the eaves. So near the eaves (bottom), the roof surface is shallow → the horizontal inset per layer is large at the bottom and small at the top. So use inset steps that decrease going up: step_i ∝ pow(1 - i/N, k) with k>0.

Also each layer should be a "frame" (hollow) or just a solid box? Solid boxes stacked are fine — only the outer edge is visible. But to save vertices, we can just use full boxes; the hidden interior costs vertices but it's cheap enough. Actually with vertex counts: a box = 24 verts, 36 indices. 20k boxes = 480k verts, 720k indices. That's fine for modern GPUs but memory heavy. Let's target maybe 30k-60k boxes total. Hmm, that could be 1.5M verts. Might be too much for shadow maps? Shadow rendering re-renders all geometry each frame (unless we use a static shadow map... we could render shadow map once and freeze it — but day/night changes light direction; we can re-render on change).

Optimization: since the scene is static, we can set `renderer.shadowMap.autoUpdate = false` and `needsUpdate = true` only when light changes. That's a big win.

Also we can reduce box vertex count: a custom box with 24 verts is standard. We could use 8 verts + flat normals? No, flat shading needs per-face normals → 24 verts. Fine.

Let me estimate: 
- Castle keep: ~600 boxes
- Castle complex (walls, yagura, gates): ~2500 boxes
- Stone fortifications: ~3000-5000 boxes (individual stones)
- Town 130 buildings × ~25 boxes avg = 3250 boxes
- Trees: 90 sakura × (trunk 6 + branches 12 + blossom clusters 40) = ~5000 boxes; 45 green trees × 25 = 1125
- Props: 2000 boxes
- Roads: use merged quads (planes) rather than boxes → cheaper. Or thin boxes.

Total maybe ~20k boxes = 480k verts. That's acceptable. Use one or a few merged meshes.

Actually, better: group merged meshes by region so we can cull? Not needed. Let's do a handful of merged meshes:
- `staticMesh` (town + props + walls) — one material with vertexColors
- `castleMesh`
- `foliageMesh` (tree trunks + blossoms) — maybe separate for transparency? Blossoms opaque is fine.
- `waterMesh` (separate, animated shader-ish material)
- `groundMesh` (terrain)

Terrain: heightfield. Generate with a deterministic function: base plane with castle hill raised, terraced baileys, moat depression, town flat-ish with variation. Build terrain as a grid of quads (voxel-ish: use a "blocky" terrain with stepped plateaus). Given the voxel diorama style, a stepped/terraced terrain looks great. Use a grid of cells (e.g., 128×128 cells over 400×400 units → 3.1 unit cells) with height quantized to steps. Build top faces + side faces where heights differ. That's a classic voxel terrain mesher. Vertex count: 128×128 = 16384 top quads = 65k verts. Fine.

Hmm, but a voxel terrain with 3-unit cells might look too chunky for a diorama. Use cell size ~2 and grid 200×200 = 40000 cells → 160k quads... too many. Let's do cell size 2.5, grid 160 → 25600 cells. Top quads 25600 × 4 verts = 102k verts. Plus sides. OK, acceptable. Actually we can merge into one geometry with vertex colors (grass green, stone, sand, road colors based on region).

Hmm, but roads need to sit on terrain. Simpler approach: make the town area essentially flat (height 0) with gentle variation, and roads are thin boxes slightly above ground. Castle hill is terraced plateaus. Moat is a depression with water plane.

Let me simplify: terrain = heightfield function h(x,z) built from:
- base town plain: 0 + small noise (±0.35)
- moat ring: depression to -2.2
- castle hill: terraced steps up to +14 in center-ish
- shrine hill to the east/northeast: +5 terraced
- river? maybe a river from moat.

Then mesh it with the voxel mesher, coloring by height/slope/region (grass, dark grass, stone for steep slopes, road color where road mask says so).

Roads: I'll define roads as polylines/rects; a function `roadMask(x,z)` returns whether a point is on a road, and terrain colors accordingly (dirt color) and also we can place road props. Buildings placed along road edges.

Actually, to keep roads crisp and voxel-like, I'll place road surfaces as merged thin boxes at the terrain height sampled per segment... but terrain varies. If town plain is nearly flat (±0.35), roads as boxes of thickness 0.5 sunk into ground will look fine. Let me make the town plain exactly flat at y=0 except near the moat/hills, and add gentle noise only in the outer ring/outskirts. That simplifies everything: buildings sit at y=0, roads at y=0.06, etc. Terrain variation comes from the castle hill, shrine hill, moat, terraces, and outer outskirts noise. Good engineering decision.

Hmm, but "small terrain variation" in town is requested. I can add gentle variation in the town via garden mounds and slight yard bumps, plus the town sits on a plain with a few terraced platforms (e.g., samurai district on a +1.2 terrace with a stone retaining step). That gives elevation variety without breaking placement. 

Let me define the world:
- World extent: about 420 × 420 (x from -210 to 210, z from -210 to 210).
- Castle center at (0,0). Castle hill occupies roughly radius 55-70.
- Moat ring around castle at radius ~72-86 (a rounded rect ring).
- Town: south (positive z) and east (positive x) mainly. Let's define "south" as +z. Camera hero views from south-east.
- Districts:
  - Merchant district: south of the main gate, along the main south road (sannaka), x ∈ [-60, 60], z ∈ [95, 175].
  - Market plaza: near (0, 120) — a wide plaza.
  - Samurai district: west side, x ∈ [-170, -80], z ∈ [20, 140], on a raised terrace.
  - Warehouse district: east/southeast near a water basin? Put near river east: x ∈ [95, 165], z ∈ [95, 160].
  - Residential: southwest and south filler.
  - Shrine district: northeast hill (x ~ 95, z ~ -60) with torii approach path descending to a road.
  - Also a temple with a pagoda maybe — a five-story pagoda would be a great silhouette accent. Yes! Add a pagoda on the shrine hill or a temple to the west. Let's add a three-story pagoda in the temple precinct to the northwest for silhouette variety. Hmm, keep scope manageable but this is a "wow" item. I'll add a 5-tier pagoda at the shrine/temple complex to the east-northeast. Good landmark visible from hero views.

- Roads: 
  - Main south avenue: from (0, 88) [bridge over moat] straight south to (0, 200), width 12.
  - Ring road around outside the moat: a rounded rect at radius ~95, width 8.
  - East avenue, west avenue, cross streets, alleys (width 4-5).
  - Shrine approach: from ring road NE up the hill, stepped stone path with torii gates.

Buildings placed along road frontages: I'll implement a block-based generator: define city blocks as rectangles bounded by roads; for each block, place buildings along its perimeter facing the roads, with alleys between. This gives coherent urban fabric.

Simpler robust approach: define a list of "street segments" (axis-aligned lines with width). For each street, place buildings on both sides at intervals with jitter, checking they don't overlap previously placed buildings (occupancy grid). This yields dense, coherent frontage. Plus blocks get rear filler buildings along alleys.

I'll implement an occupancy grid (cell 2 units) over the town; `tryPlace(rect)` checks free + not on road + not in moat + not on castle hill. Then place.

Let me now design the geometry builder.

```js
class GeoBuilder {
  constructor(){ this.pos=[]; this.nrm=[]; this.col=[]; this.idx=[]; this.count=0; }
  box(cx,cy,cz,sx,sy,sz,color, rotY=0, rotX=0, rotZ=0)
  ...
  build(material) -> Mesh
}
```
Writing raw arrays with push into JS arrays for ~500k floats is OK but slow-ish. Use growable Float32Array with manual capacity doubling for speed. I'll implement a simple growable buffer.

Box vertex data: I'll precompute a unit box template (positions for 24 verts, normals, indices 36). Then for each box, transform each vertex by scale, rotation (Y only mostly; allow X/Z tilt for stones), translate.

For performance, support rotY only plus optional rotX/rotZ via a small matrix. I'll just build a THREE.Matrix4 per box (compose from quaternion from Euler + scale + position) and apply to the 24 template verts. 24 matrix ops per box × 25k boxes = 600k matrix-vector ops — fine (fast).

Normals: apply rotation to normal (no scale for normals if we normalize; use normalMatrix or just apply rotation quaternion then normalize).

Colors: per-vertex color = box color (with optional slight per-box tint variation).

Also need a "quad" primitive for terrain top faces and water etc. And maybe a "prism"/wedge for some roofs. I'll add `wedge` (triangular prism) for simple shed roofs on stalls/sheds.

Materials: 
- Main: MeshLambertMaterial? For nicer look use MeshStandardMaterial with roughness ~0.85, metalness 0, vertexColors true. Standard is more expensive but with one draw call it's fine. Let's use MeshStandardMaterial with flatShading? Since our normals are already per-face flat, no need.
- Add fog for depth (FogExp2 or linear fog matching sky).
- Sky: gradient via a large sphere with a custom shader (BackSide) — procedural, allowed. Plus a few procedural clouds? Could add blocky clouds (voxel clouds) — nice for diorama. Maybe simple: a handful of large soft white boxes far away? Might look cheap. Instead use gradient sky + subtle sun disc. I'll do a shader sky dome with gradient + sun glow. And distant mountain silhouettes: procedural low-poly mountain ring in the distance (dark blue-grey), which helps background layering. Yes — a ring of mountain ridges at radius ~600, generated from noise, as a simple extruded shape (or just a few big triangular prisms). Let's do a "mountain ring" mesh: for each of N angles, a cone-ish blocky mountain. Use vertex colors with snow caps? Keep it simple: dark blue-grey mountains with lighter tops, semi-fogged. Good.

Water: moat water plane with a custom shader material: animated ripples via normal perturbation, fresnel-ish reflection of sky color, transparency. Simple approach: MeshStandardMaterial with color dark teal, roughness 0.1, metalness 0.3, transparent opacity 0.85, plus animated small vertex displacement in a custom onBeforeCompile. Or a ShaderMaterial computing: sky gradient reflection + sun specular + noise ripples + depth-ish. I'll write a custom ShaderMaterial (no env map needed) — deterministic and cheap. Include fake reflection of the castle? Too complex. Use fresnel between deep water color and sky color + specular sun + animated ripple normals. Looks good.

Also add reflections? Skip.

Lighting:
- HemisphereLight (sky, ground)
- DirectionalLight with shadow (PCFSoft, mapSize 2048 or 4096, ortho box covering ~260 units)
- Ambient small
- Night: dim blue-ish hemisphere + moon directional + lantern emissive. Lanterns: since geometry is merged with vertex colors, emissive per-instance is tricky. Solution: separate lantern-glow meshes (small boxes with MeshBasicMaterial emissive-like, additive glow sprites) that are toggled visible at night. Plus a few PointLights? Point lights with shadows are expensive; use a few non-shadow point lights (maybe 4-6) near the market/gate for night mood. Or just emissive quads + fake glow planes (billboards with additive material). I'll do: lantern glow boxes (basic material, bright warm color) + additive glow billboards (procedural radial gradient via canvas? No external textures — canvas-generated textures are procedural, allowed? "Do NOT use downloaded textures / image assets" — a canvas-generated gradient is generated in code, which should be fine. But to be safe, I can generate glow via a ShaderMaterial on a plane computing radial falloff — fully procedural, no canvas. Let's do shader-based glow billboards.)

Day/night/twilight: three modes with lerped transitions (animate over ~1.2s). Update sky shader uniforms, light colors/intensities, fog color, lantern visibility, water uniforms.

UI: overlay panel with title, mode buttons, camera preset buttons, FPS, mesh/triangle counts, selected info. Modern glassy dark UI with Japanese accent (vermilion). Use CSS. Keep it in a corner (top-left) and a bottom bar for presets. Also a small legend/hint.

Camera presets:
1. Isometric Hero: high SE aerial looking at castle.
2. Castle Front: from south, low, across the bridge & main street, castle towering.
3. Street View: street level in merchant district looking down the street toward the castle.
4. Sakura Scenic: low angle under sakura branches near the moat with castle reflected-ish across water.
5. Top View: near-overhead.
6. Shrine/Pagoda view (extra).
7. Twilight aerial? Extra: "Market" view.

Implement smooth camera transitions (tween position + target with easing).

Picking: raycast against a set of "pickable" invisible proxy boxes? Better: keep a list of pickable objects with bounding boxes (each building/yagura/etc. registered with a Box3 and a label + category). Raycast against a dedicated invisible mesh list? Simplest: maintain `pickables` array of {box: Box3, label, category, highlightBox}. Raycast manually: ray.intersectBox for each (fast enough with ~200 boxes). On hit, show info and draw a highlight: a Box3Helper-like wireframe box (use LineSegments of EdgesGeometry of a box, scaled) plus a subtle emissive tint? Simplest robust: a `THREE.Box3Helper` with vermilion color, plus animate. Also could re-render the selected structure with an outline... Keep Box3Helper + a ground ring marker. Good.

Actually a nicer highlight: a translucent box shell with additive material + animated pulse. I'll do both: wireframe edges + faint fill.

Now the meat: procedural architecture.

### Roof system (key visual)

Function `buildRoof(b, opts)`:
- opts: {x,y,z, w, d, h, style: 'hip'|'gable'|'irimoya'|'pyramid', tileColor, ridgeOverhang, layers, cornerLift}
Implementation (voxel layered):
1. Eave layer: a slab of size (w + 2*eaveOverhang) × eaveThickness × (d + 2*eo), positioned at base y. Dark tile color, slightly darker underside layer (a second thinner slab below in wood color = the "fascia"/rafters). Add rafter ends: small boxes along the underside edge at intervals (visible under-roof structure) — great detail.
2. Then N stacked layers, each inset by `inset_i` on all sides and raised by `dy_i`, each a slab of thickness `dy_i + overlap`. Colors: tile color with slight darkening toward the eaves (AO-ish).
3. For gable style: the top layer becomes a ridge beam; add gable end boards (chidori-hafu) as small triangular wedges on the ends, and a karahafu curve at the front for hero buildings (approximate with 3 stacked boxes forming a curved gable — doable as a small layered arc).
4. Ridge cap: a box along the ridge line, slightly raised, with ends extending (chigi crossbars at ends for shrines, giboshi ornaments as small stacked boxes).
5. Corner lift: at the four corners of the eave layer, add small boxes raised by 0.06-0.12 to suggest upturned corners.

For hip roofs, the stacked-inset approach naturally forms a hip. For the top, the last layer should be a ridge (a long thin box) rather than a full slab — handle by making the final layer's depth small (like 1.2) so it reads as a ridge.

I'll implement `buildRoof(b, {cx, cy, cz, w, d, h, dir, style, tile, trim, detail})` where dir indicates ridge orientation (along x or z).

Let me define the layered hip roof precisely:
- layers L = 4..6 depending on h.
- total inset per side: for hip roof, insetX = w/2 - ridgeHalfLen... Actually for a hip roof with ridge along X: bottom layer footprint w×d; top layer is a ridge line of length `ridgeLen` along X at height h, with depth ~0. So the layers interpolate: layer i at t=i/L: depth d*(1-t), width w*(1 - t*(1 - ridgeLen/w)). With concave curve: use t' = pow(t, 0.65) for height fraction? Let's parametrize by height: y_i = h * (i/L) with the plan dims shrinking nonlinearly: at height fraction u, halfDepth = (d/2)*(1-u)^1.0? For a concave (muku) roof, the eaves are shallow: near the bottom, a large horizontal change per unit height. So the plan shrinks fast at the bottom and slow at the top: shrink = 1 - pow(1-u, 2)? Let's check: u=0 → shrink 0; u=0.5 → 1-0.25=0.75 (75% of the shrink already done at half height) → yes, fast shrink at bottom = shallow slope at bottom, steep at top. Correct for Japanese roofs. Use `f = 1 - Math.pow(1-u, 2.2)`.

Each layer is a slab from y_i to y_{i+1} with plan dims at the mid-height. That gives a stepped pyramid approximating a concave curve. With 5-6 layers and tile color banding, it reads as a tiled roof. Add per-layer slight outward lip: make each layer slightly larger than the pure interpolation (like +0.06) so the steps read as overlapping tile courses (like onigawara rows). Nice.

Also add a "thick eave edge": the bottom layer is thicker (0.5) and darker with a distinct fascia.

Good.

### Building system

`buildMachiya(b, x, z, w, d, rot, opts)`:
- Foundation: stone plinth (few small stones or a grey box with slight bevel).
- Ground floor: plaster walls (white/off-white) with a wooden post grid: posts at corners and intervals (0.25×0.25 boxes), a recessed wall plane inset by 0.12, horizontal nuki beams, windows (dark inset boxes with lattice slats: 3-5 thin vertical boxes), a doorway with a frame and a step stone.
- Upper floor (if 2 stories): slightly smaller footprint, plaster, a row of windows with wooden shutters (dark boxes), a balcony with railing (posts + 2 rails) for larger buildings.
- Roof: layered hip/gable with eaves overhanging 0.6-0.9.
- Details: ridge ornaments, downspout, a small chimney or vent box, noren (colored cloth box) over the door for merchant houses, signboard, awning (sunshade) as a slanted thin box over the front, barrels/crates in front.

Facade orientation: buildings have a `facing` (0=+z, 1=+x, 2=-z, 3=-x). I'll build in local space with front = +z and then rotate the whole thing by rotY. To do that with the raw builder, I need a transform stack. Implement `b.push()`/`b.pop()` with a current Matrix4 (local→world), and `b.translate/rotateY/scale`. Then `addBox` transforms local→world. That's clean and flexible.

Cost: one matrix multiply per vertex (world = parent * local). Precompute per-box matrix = parent * localBoxMatrix. Fine.

### Castle keep

5 levels + stone base. Each level:
- Stone base/ishigaki for the whole hill (separate).
- Level platform (wooden floor edge / shibi-gawara tiled edge).
- Walls: white plaster, slightly tapered (each level's walls slope inward slightly — do with 2-3 stacked bands inset).
- Wooden substructure: dark brown band at the base of each wall (itajii/rammed earth), corner posts, horizontal beams.
- Windows: rows of sangyo-chiri (triangular/rectangular) dark openings.
- Balcony (karan-kō style with railing) on some levels.
- Roof: hip-and-gable (irimoya) layered, with thick eaves, shibi-gawara (tile edge at the wall top), onigawara (demon tiles at corners = small dark boxes), chidori hafu (gable) on the front, karahafu over the entrance.
- Golden shachihoko on the top ridge (gold-colored boxes) — a great focal accent.
- Gold ornaments on the lowest roof gables (karahafu gold trim).

Levels: 
- L1: 26×22 footprint, wall height 7
- L2: 22×18, h 6
- L3: 18×15, h 5.5
- L4: 14×11.5, h 5
- L5: 10×8.5, h 4.5
Roofs between levels: each roof has h ~3-4 and overhangs.
Total height above the keep base: base platform at y=16 (top of stone hill) + sum ≈ 16 + (7+3.5) + (6+3.2) + (5.5+3) + (5+2.8) + (4.5+3.2) ≈ 16+43.7 ≈ 60. That's a big keep (real ones ~30-45m). Scale: if 1 unit = 1m, 60m is too tall vs. houses of 6m. Realistic proportions: keep ~40m tall including stone base ~15m. Let's target total ~52 units with houses 5-8 units. Camera scale accordingly. Fine — castles do dominate.

Let me set the hill top (main bailey) at y=18, with the stone walls from 0 to 18 (terraced). Keep base at 18, keep top at ~18+38 = 56. Town houses ~6 tall at y=0. Ratio 56/6 ≈ 9 — visually dominant. Good.

World scale: town blocks with 6-unit houses, streets 8-12 wide, town radius ~180. Total ~400 units. Shadow camera must cover ~400 → shadow resolution suffers. Solution: two-tier shadows — main directional shadow covers the central 220 units at 4096 map (or 2048). Distant town gets no shadow (acceptable, fog + AO). Or set shadow camera to cover 300 units with 4096 map = 0.073 units/texel — decent. Let's use 4096 if supported (check maxAnisotropy / just try; 4096 is widely supported). I'll use 2048 for safety with tight bounds (240 units) → 0.117/texel. Hmm, for crisp close-up detail, 4096 over 260 units = 0.063. I'll use 4096 with a fallback check on `renderer.capabilities.maxTextureSize`.

Also: static shadow map (autoUpdate=false, needsUpdate on light change) — big perf win.

But wait: with autoUpdate false, the shadow map renders once; that's fine since geometry is static. Water doesn't cast shadows.

### Stone walls (ishigaki)

`buildStoneWall(builder, path/segment, from y0 to y1, slope)`: fill the face with individual stones in rows. Rows follow the slope; each row has stones of random widths (1.2-2.6), heights (0.7-1.1), depth protrusion (0.05-0.35), slight rotation (±0.06 rad), color variation (grey-warm palette). Use a "no-grout" tight packing with tiny gaps (0.06). This yields hundreds of stones per wall. 

For the castle hill: build terraced retaining walls around polygon rings. The hill: a rounded-rect (or circular) footprint with 3 terraces. For each terrace edge, generate a wall band (a ring of quads) and fill with stones. Implementation: for a ring defined by a polygon (list of points), for each edge, generate rows from base y to top y, with the face sloped (top inset by slope*height).

Vertex budget: 4000 stones × 24 verts = 96k verts. OK.

Also add "sangizumi" (rough) vs "surugazumi" (regular) variety: lower terraces rougher/bigger stones, upper more regular.

### Walls (castle)

`buildCastleWall(b, from, to, height)`: 
- Stone base (short ishigaki, 2-3 rows of stones)
- White plaster wall with slight taper (2 bands), wooden base cladding (dark band with vertical planks), 
- Roof cap: layered dark tile roof along the top with a ridge.
- Openings/slits: small dark boxes.
- Corner turrets (sumi-yagura): small towers at corners with a hip roof.

### Yagura

`buildYagura(b, opts)`: 2-3 story tower: stone base, plaster walls with taper, wooden bands, windows, hip roof with big eaves, sometimes a half-timbered top. Vary: size, stories, roof style, balcony, attached smaller roof. 4-6 of them, each different.

### Gate (Otemon)

Composite: a base stone wall, main gate body with a thick timber frame, a large recessed doorway with heavy doors and studs, a karahafu on the front, a hip-and-gable roof, plus a small attached gable (kara-yagura) on top with a small roof, and side walls connecting to the castle walls. Hero detail.

### Bridge

`buildBridge(b, from, to, width)`: stone abutments (stones), timber piers or a beam structure, planks (many individual planks with slight variation), side rails (posts + 2 beams), and end posts. Slight arch (raise the middle by 1-1.5 units with planks stepping).

### Trees

Sakura: trunk (tapered stack of boxes with slight lean), 3-5 branches (boxes at angles), blossom clusters: 25-45 clusters per tree, each cluster a box of 1.2-2.6 size with slight rotation and pink/white color variation, arranged in a dome/cloud shape. To make them "lush", cluster centers distributed on a rough sphere shell with jitter, plus some interior. Colors: palette of 6 pinks (#ffdcea, #ffc9dc, #ffb3cf, #fff0f5, #f7c8dd...). Add a few darker pink accents.

Green trees: similar but conical/rounded, darker greens, some as cedar/pine shapes (tiered cones = stacked boxes decreasing) — pine with layered flat clusters reads very Japanese. Add bamboo? Maybe a few bamboo clumps (thin tall cylinders + small leaf boxes) in the garden. Nice touch.

Trees should be instanced? They're merged into the foliage builder — fine.

### Props

- Lanterns: stone lantern (base, shaft, platform, light box with glow window, cap, finial) and paper lantern (chochin: red box/cylinder + glow). Place along streets, shrine approach, market.
- Fences: bamboo fence panels (posts + horizontal rails) and wooden palisades; place along samurai district walls, shrine grounds, gardens.
- Barrels, crates, carts (wheels as octagonal prisms? use boxes rotated 45°), firewood stacks, wells (stone ring + roof on posts), benches, signboards, awnings, banners (nobori: tall thin colored boxes), stepping stones, garden stones, market stalls with awnings and goods.

### Market

A plaza with ~14 stalls arranged in rows: each stall = counter box, canopy (slanted striped awning made of alternating color slats), support poles, goods (small colored boxes/bowls), crates, barrels, a cart, lanterns on poles. Ground colored as packed dirt with a stone well in the middle.

### Shrine

- Torii gates (2-3 along the approach): two slightly inward-leaning pillars, a curved kasagi (top lintel with upward curve via layered boxes), shimaki, nuki, gakuzuka.
- Stone path (paving slabs with variation), stone steps up the hill.
- Stone lantern rows along the approach (dozens!) — great for night.
- Honden (main hall): vermilion + white walls, layered cypress-look roof (hipped, with chigi crossbars and katsuogi logs on the ridge), a front balcony/staircase, railings.
- Kagura-den with a drum, offering box, a sacred tree (large green tree), fences (tamagaki: posts + rails), komainu (stylized lion-dog blocks — maybe skip or abstract).
- Pagoda (5 stories) nearby: square tiers with big eaves, a sorin (finial spire with rings). Beautiful landmark.

### Water features

- Moat ring around the castle with stone embankments.
- A river along the east/south connecting to the moat, with a water basin/harbor for the warehouse district (boats! simple wooden boats = great props).
- A pond in the shrine garden? Maybe skip.

Boats: 3-5 simple hulls (tapered boxes) + a mast + cargo. Nice.

OK. Now, code structure. This will be a big file (~3000-4500 lines). I need to write it carefully and correctly without being able to test. Let me be disciplined: keep helper APIs small and consistent.

Let me define the core builder:

```js
const _m4 = new THREE.Matrix4(); ...
class MeshBuilder {
  constructor(){ this.pos = new Float32Array(65536*3); ... }
}
```
Simpler: use plain arrays and push. For ~600k floats pushes, JS arrays handle it but memory churn. Let's implement growable typed arrays:

```js
class Builder {
  constructor(name){
    this.name=name;
    this.posArr=new Float32Array(3*4096); this.norArr=new Float32Array(3*4096); this.colArr=new Float32Array(3*4096);
    this.idxArr=new Uint32Array(6*4096);
    this.vCount=0; this.iCount=0;
    this.stack=[]; this.M=new THREE.Matrix4(); // current transform
  }
  _ensure(v,i){...}
  pushBox(cx,cy,cz,sx,sy,sz,color,rx=0,ry=0,rz=0)
  pushQuad(p0,p1,p2,p3,color) // for terrain
  build(mat){...}
}
```

For the transform stack: `this.M` is current matrix (identity by default). `pushTransform(m)` → stack.push(M.clone()); M.multiply(m). `pop()`.

Helper: `b.at(x,y,z,ry,fn)` → push translate+rotate, call fn, pop.

Box template: 24 verts with normals, 36 indices. Precompute arrays.

For color: accept a THREE.Color or a packed hex int; store r,g,b. Add optional jitter param handled by caller.

Also implement `pushBoxTop` variant? Not needed.

Terrain mesher: separate builder with `pushQuad` (2 triangles, 4 verts) with computed normal.

Let me write the terrain:

```
const CELL = 2.0, GRID = 210; // 420 x 420 world
height(x,z) function
```
210×210 = 44100 cells. Top quads 44100 → 176k verts, plus side faces maybe 30k quads. ~300k verts for terrain. Hmm, that's a lot but fine (one draw call). Could reduce with cell=2.5, grid=168 → 28224 cells. Let's use CELL=2.4, GRID=176 → 422.4 world size, 30976 cells. Good.

Actually, since the town is flat, I could coalesce flat regions... but simpler: keep the grid. 31k cells × 4 verts = 124k verts + sides. Fine.

Height function:
```
function terrainHeight(x,z){
  // base plain
  let h = 0;
  // outskirts noise
  const r = Math.hypot(x,z);
  const outer = smoothstep(150, 260, r);
  h += outer * (noise2(x*0.02,z*0.02)*6 + noise2(x*0.06,z*0.06)*1.6);
  // moat
  const md = moatDistField(x,z); // signed distance to moat centerline
  if (Math.abs(md) < moatHalf) h = min(h, -2.4 * cos(...))
  // castle hill terraces
  h = max(h, hillHeight(x,z));
  // shrine hill
  ...
}
```
Terraces: quantize hill height to steps of 3.

I need a simple deterministic value-noise. Implement a hash-based value noise with smoothstep interpolation, seeded.

The moat: define as a rounded-rect ring around the castle: use a superellipse distance. Let me define `castleShape(x,z)` = rounded-rect SDF with half-extents (62, 54) and radius 22. Then:
- hill: sdf < 0 → inside hill.
- moat: 8 < sdf < 26 → moat trench (depth -2.6 at center of band).
- outer ring: sdf in 0..8 → the outer earthwork (counterscarp) rising slightly.

Actually the castle hill: the innermost bailey at height 18, terraces down to 0. Let me define hill bands by sdf:
- sdf < -8 → h = 18 (main bailey)
- -8..6 → h = 12 (second bailey)
- 6..16 → h = 7
- 16..24 → h = 3.5
- 24..34 → h = 1.2 (outer bailey / town edge)
- 34..42 → 0
- 42..? moat: 42..58 → water at -2.2, ground dips to -3.
- beyond 58 → back to 0 (town plain).

Hmm, that makes the moat far from the keep. Let's scale: castle complex radius ~ 60-70 units, moat band at 70-88. Town starts at ~92.

Let me define with a rounded-rect SDF with half extents (58, 48), corner radius 20. Then bands:
- s < -14: h=19 (main bailey, where keep stands)
- -14..-2: h=13 (Go-no-maru)
- -2..10: h=8 (Wa-no-maru)
- 10..22: h=4 (outer wall base)
- 22..34: h=1.5
- 34..44: 0 (land between castle and moat outer? no...)

Hmm, I want the moat immediately outside the castle walls. Let's do:
- s < -16: 19
- -16..-4: 13.5
- -4..8: 8.5
- 8..18: 4.5
- 18..26: 1.6 (dry outer terrace / base)
- 26..34: 0 → this is the top of the inner scarp / the moat edge... 

Actually the moat should be a trench: from s=30 to s=48, depth -3.0 with sloped sides. Then s>48 → 0 (town plain). And the outer bank (s 48..56) rises to 0.8 (the outer earthwork/ring road embankment).

The water surface at y = -1.4 (so the trench bottom -3.2, water top -1.4, ground around 0 → water appears as a sunken band). Good.

Bridges cross the moat at s≈39 (center of band) at 3-4 locations: south main (big), east, west, north (small).

Where bridges cross, the moat water must be continuous; bridges just span it.

The town: outside s > 56.

Now the town layout: The town occupies roughly the region outside the moat, biased south and east. Let me define roads in the region x ∈ [-190, 190], z ∈ [-170, 200], excluding the moat ring and the castle.

Road network design (axis-aligned mostly, with a couple of diagonals for interest):
- Saka-michi (main south avenue): x=0, from z=56 (bridge end... actually bridge ends at s=48) to z=196, width 13.
- Ring road (Sotobori-dori): follows outside the moat — a rounded rect at s≈62, width 9. Following a curve means non-axis-aligned segments; I'll approximate with a polygon polyline of ~24 segments and place buildings along it with rotation matching the segment. That's doable: my building placement supports arbitrary rotation.

Hmm, complexity. Let me simplify: the ring road is an octagon/rounded-rect approximated by 12 axis-aligned-ish segments. Buildings along it rotate to match. I'll support arbitrary angle rotation in placement (rotY = segment angle).

- East avenue: x=110, z from -120 to 190, width 10.
- West avenue: x=-110, z from -110 to 170, width 9.
- North road: z=-100, x from -140 to 140, width 8.
- Cross streets (south): z=110, z=150, z=185 across x ∈ [-150,150], width 7.
- Market street: z=95, x ∈ [-40,60], width 16 (plaza).
- Alleys: width 4.5, spaced ~26 within blocks.
- Shrine approach: from (110, -60) diagonal to shrine at (150, -110)? Let's put the shrine hill at (135, -95) with a path going southwest down to the east avenue.

Let me reconsider the composition. Hero camera views:
1. Isometric hero: camera at (170, 130, 210) looking at (0, 25, 0) — sees castle, town to the south-east, moat, sakura.
2. Castle front: camera at (0, 12, 175) looking at (0, 26, 0) — down the main avenue, through the gate, up to the keep. Long depth. The main avenue should be lined with machiya and lanterns, sakura framing.
3. Street view: camera at (-24, 4.5, 140) looking toward (6, 14, 20) — down a diagonal street with the castle in the distance.
4. Sakura scenic: camera near the moat at (70, 6, 96) looking at (0, 24, 0) with sakura branches in the foreground.
5. Top view: (0, 320, 1) → (0,0,0).
6. Shrine/pagoda: from (95, 12, -20) looking at (140, 20, -95)? Something like that.
7. Market: (30, 6, 130) → (0, 4, 100).

I'll tune numbers as I write.

Given I can't visually verify, I should make the composition robust: castle at center, town south/east, shrine NE, mountains far background.

Now, let me think about the amount of code and write it in a structured way.

File structure:
1. HTML head, CSS (UI).
2. Body: canvas container, UI overlay.
3. importmap for three + OrbitControls + (maybe BufferGeometryUtils not needed since custom builder).
4. Script module:
   - RNG (mulberry32 seeded 1337), noise.
   - Palette constants.
   - Builder class.
   - Roof/arch primitives library.
   - Terrain.
   - Castle (hill walls, keep, yagura, gates, walls, courtyards).
   - Moat + water.
   - Bridges.
   - Town (roads, blocks, buildings, districts).
   - Shrine/temple + pagoda.
   - Market.
   - Nature (trees, grass, rocks, bamboo).
   - Props library.
   - Lighting + sky + fog + mountains.
   - Camera presets + tween.
   - Picking.
   - UI + FPS.
   - Animation loop.

Let me now write the code in detail.

**RNG**
```js
function mulberry32(a){return function(){a|=0;a=a+0x6D2B79F5|0;let t=Math.imul(a^a>>>15,1|a);t=t+Math.imul(t^t>>>7,61|t)^t;return((t^t>>>14)>>>0)/4294967296;}}
const rng = mulberry32(1337);
const rand=(a=1,b)=> b===undefined? rng()*a : a+rng()*(b-a);
const randi=(a,b)=>Math.floor(rand(a,b+1));
const pick=arr=>arr[Math.floor(rng()*arr.length)];
```
Careful: `rand(a,b)` with one arg → rand()*a.

Also a separate `rng2` for decoration so ordering changes don't shift layout? Not needed; just be deterministic.

**Noise**
```js
function hash2(x,y){ let h = x*374761393 + y*668265263; h=(h^(h>>13))*1274126177; return ((h^(h>>16))>>>0)/4294967295; }
function vnoise(x,y){ const xi=Math.floor(x), yi=Math.floor(y); const xf=x-xi, yf=y-yi;
 const u=xf*xf*(3-2*xf), v=yf*yf*(3-2*yf);
 const a=hash2(xi,yi),b=hash2(xi+1,yi),c=hash2(xi,yi+1),d=hash2(xi+1,yi+1);
 return (a*(1-u)+b*u)*(1-v)+(c*(1-u)+d*u)*v; }
function fbm(x,y,oct=3){...}
```
Note: Math.imul for 32-bit multiply to avoid precision issues.

**Builder**

```js
const BOX_V = [...]; // 24 positions (unit cube from -0.5 to 0.5)
const BOX_N = [...]; // 24 normals
const BOX_I = [...]; // 36 indices
```
I'll generate these programmatically at init:
```js
function makeBoxTemplate(){
  const dirs=[[1,0,0],[-1,0,0],[0,1,0],[0,-1,0],[0,0,1],[0,0,-1]];
  ...
}
```
Simpler: hardcode via building faces with u,v axes:
For each face with normal n, u axis, v axis: verts = n*0.5 + u*0.5*sx... Actually template in unit space: for face i, corners at (±0.5). Let me write:

```js
const FACES=[
 {n:[1,0,0], u:[0,0,-1], v:[0,1,0]},
 {n:[-1,0,0],u:[0,0,1],  v:[0,1,0]},
 {n:[0,1,0], u:[1,0,0],  v:[0,0,-1]},
 {n:[0,-1,0],u:[1,0,0],  v:[0,0,1]},
 {n:[0,0,1], u:[1,0,0],  v:[0,1,0]},
 {n:[0,0,-1],u:[-1,0,0], v:[0,1,0]},
];
```
Check winding: for face +Z (n=[0,0,1]), u=[1,0,0], v=[0,1,0]: corners in order (-u-v, +u-v, +u+v, -u+v) → CCW when viewed from +Z. Yes, correct (counter-clockwise). Triangles (0,1,2),(0,2,3). Good.

For each face, 4 verts: p = n*0.5 + u*0.5*su + v*0.5*sv where su,sv ∈{-1,1}. So template positions in unit cube (size 1) and we scale by (sx,sy,sz) at push time.

pushBox(cx,cy,cz,sx,sy,sz,color,rx,ry,rz):
```
const m = tmpM.compose(pos, quat.setFromEuler(euler.set(rx,ry,rz)), scale.set(sx,sy,sz));
m.premultiply(this.M);
```
Then for each of 24 verts: transform by m (applyMatrix4), normal = normal transformed by rotation only (use tmpM3 from the rotation quaternion, then normalize — but non-uniform scale in the parent M could skew normals; parent M is only translate+rotate in our usage, so fine).

To speed up: precompute rotation matrix as Matrix4 without scale for normals: `nm.compose(pos0,quat,scale1).premultiply(this.M)`.

That's 2 matrix ops per box + 48 vector transforms. For 30k boxes = 1.4M vector transforms. Fine (~50ms).

Also add per-box AO-ish shading: darken the bottom faces and lighten the top? A nice cheap trick: multiply color by a factor based on face normal (top faces +6%, sides 0, bottom -20%). This gives strong voxel readability. I'll add a flag `shadeByNormal` applied at build time (multiply vertex color by a factor from normal.y). Actually I'll apply it in pushBox: for each vertex, color * (0.82 + 0.18*max(0,n.y)) roughly. Hmm, careful with already-dark colors. Let's do: top ×1.08, sides ×0.94 (n.y==0), bottom ×0.7. This mimics AO and makes the diorama pop. I'll make it optional per builder (default on).

Also add subtle random tint jitter per box (±3%) to break flatness — controlled by a param.

**Colors palette** (hex ints):
```
PLASTER 0xf2ece0 / 0xe8e0d0
PLASTER_SHADE 0xd8cfbe
WOOD_DARK 0x4a3527 / 0x3b2a1e
WOOD_MED 0x6b4a30
WOOD_LIGHT 0x8a6a45
TILE_DARK 0x2f3a46 / 0x26303a
TILE_MID 0x3b4a58
GOLD 0xd9a441 / 0xf0c060
STONE_1..5 greys with warm tint: 0x8e8b82, 0x7d7a72, 0x9a968c, 0x6f6c66, 0xa8a49a
GRASS 0x7fa25a, GRASS_DARK 0x6b8f4c, DIRT 0xb09a72, ROAD 0xc2ad86, ROAD_DARK 0xa8946f
SAND 0xd8c9a0
WATER handled by shader
VERMILION 0xc4453a / 0xb8402f
SAKURA palette
GREENS 0x4e7a3a, 0x5d8c42, 0x3f6a33
ROOF_TILE 0x35414e
```

Spring grass should be fresh green with a hint of yellow. Add path stones.

**Roof builder**

```js
function roof(b, o){
  // o: {w,d,h, layers, tile, tileDark, wood, ridge:'x'|'z'|null, overhang, style}
  // built in local space centered at origin, base at y=0
}
```
Details:
```
const L = o.layers || 5;
const ov = o.overhang ?? 0.7;
const w0 = o.w + ov*2, d0 = o.d + ov*2;
// eave fascia
b.box(0, -0.18, 0, w0+0.15, 0.3, d0+0.15, dark);
// rafters under eave along front/back
for x in steps: b.box(x, -0.42, ±(d0/2-0.25), 0.16,0.22,0.5, wood)
// layers
for i in 0..L-1:
  u0=i/L, u1=(i+1)/L
  f0=1-pow(1-u0,k), f1=1-pow(1-u1,k)
  wA = lerp(w0, ridgeW, f0), etc.
  y0 = h*u0, y1=h*u1
  box at (y0+y1)/2 with size (wMid+lip, (y1-y0)+0.12, dMid+lip)
```
Where ridgeW = (ridge==='x') ? w0*0.34 : 0.6; ridgeD = (ridge==='z') ? d0*0.34 : 0.6. For a hip roof with ridge along x: top layer width = ridgeLen, depth = 0.5. For pyramid (no ridge): both → 0.5.

Hmm, the interpolation should shrink width and depth with different factors if the ridge is long. Let me parametrize: at fraction f (0 bottom → 1 top), halfW = (w0/2)*(1-f) + (ridgeLen/2)*f, halfD = (d0/2)*(1-f) + (ridgeThk/2)*f. With f = 1-(1-u)^k, k≈2.0.

Then the ridge cap: box at y=h+0.12, size (ridgeLen+0.9, 0.34, ridgeThk+0.7) in a slightly darker tile, plus end caps (small boxes) and giboshi (2 small stacked boxes at each end).

Corner lift: at the 4 corners of the eave, add small boxes at y=+0.1, size 0.7³, rotated 45°, colored tile — reads as upturned corners. Maybe better: extend the bottom layer corners slightly outward and up. I'll add small "corner tiles" boxes raised 0.12 at each corner.

Onigawara (demon tiles): at the 4 corners of the roof base for hero buildings, add a slightly larger dark box.

Chidori-hafu (triangular gable): a small triangle made of stacked boxes on the front face — implement `hafu(b, w, h, color)` as 4-5 stacked shrinking boxes forming a triangle, plus a white bargeboard edge. Place on the front of hero roofs. Karahafu: a curved gable — approximate with a triangle with rounded top: 3 boxes forming a wide flat arc + a small peak. I'll implement `karahafu(b, w, h)` as a shallow arc of 7 boxes following a curve, in gold/white/tile colors.

**Building archetypes**

`facadeDetails(b, w, h, d, opts)`: posts, beams, windows, door.

Let me write a generic `makeHouse(b, o)` with parameters:
```
o = {w, d, floors, wallH, roofH, roofStyle, wallColor, woodColor, tile, style:'machiya'|'warehouse'|'samurai'|'workshop'|'inn'|'tea', facing, seedish params}
```
Construction:
1. Plinth: stone box (w+0.3, 0.35, d+0.3) dark grey + a few individual stones at the front.
2. For each floor: 
   - wall box (w, fh, d) plaster, inset 0.
   - corner posts (4) wood 0.22² full height, protruding 0.06.
   - mid posts on front/back every ~2.2 units.
   - horizontal beam at floor top (w+0.16, 0.22, d+0.16) wood.
   - sill/rim at floor bottom.
   - windows: on front (and sides if visible): dark inset box 1.0×0.9 at height, plus 3 lattice bars, plus a lintel.
   - door on front: dark box 1.1×2.0 recessed, frame, step.
3. Top: eave beam ring (w+0.3, 0.25, d+0.3) wood.
4. Roof.
5. Extras by style: balcony, awning, noren, sign, chimney, rooftop vent, side annex (small lean-to with shed roof), fence/garden for samurai.

To avoid a repetitive look, vary: w ∈ [4.5, 9], d ∈ [4, 7.5], floors 1-2 (some 3 for big merchant), wallH per floor 3.0-3.8, roofH 1.8-3.0, roofStyle hip/gable/pyramidal-ish, colors slightly varied.

**Placement system**

```js
const occ = new Map(); // key `${cx},${cz}` cell of 2 units → true
function claim(x0,z0,x1,z1){...}
function free(x0,z0,x1,z1){...}
```
Also `isOnRoad(x,z)` via road rects list (with rotation? keep roads axis-aligned for the town grid, plus the ring road as segments — for occupancy, I'll rasterize all road segments into the occ grid as "blocked" cells with a small margin). That's elegant: rasterize roads into the blocked grid, then buildings just need `free()` + not overlapping other buildings.

Rasterizing a rotated segment: sample along the segment, mark cells within halfWidth+margin.

Then building placement along a street: for each side, walk along the street from start to end with a cursor; pick a random building depth/width; check the footprint rect is free; if yes, place and advance; else advance by 2.

Footprint rect for a segment with direction (dx,dz) and side normal (nx,nz): building front at offset = halfWidth + margin from the centerline; footprint = front offset to front+depth. For axis-aligned, easy. For rotated, compute the 4 corners and rasterize into occ (mark cells covered by the rect). Use the AABB of the rotated rect for occupancy (slightly conservative for 45° roads — acceptable).

I'll keep most roads axis-aligned; the ring road will be an octagon with 45° corners where I'll place fewer buildings (or just place them with AABB occupancy).

**Districts**: define a function `districtAt(x,z)` returning a type; building archetype and palette chosen by district. Districts:
- merchant: along the main avenue and market street (2-story machiya, awnings, signs, lanterns).
- residential: smaller houses with gardens, fences, hedges.
- samurai: on the west terrace: walled compounds with gates, long roofs, gardens inside, bamboo fences.
- warehouse: near the east water basin: plain plaster walls, small windows, big doors, gable roofs, few decorations.
- shrine/temple: on the NE hill, mostly the complex itself + a few priest houses.
- artisans/workshops: southwest with chimneys and yards.

**Numbers**: aim ~130 buildings. I'll count and log; if the generator produces fewer, add rear-fill passes. I'll add a final "filler" pass that tries to place small sheds in remaining free cells within town bounds until count ≥ 110.

Since I can't run it, I should make the generator robust: it should produce a good number regardless. I'll design block sizes so ~130 fit.

**Courtyards / plazas**: reserve rects (blocked for buildings) for the market plaza, the shrine grounds, the castle approaches, and a couple of small plazas with wells/benches/trees.

**InstancedMesh usage**: The prompt suggests it. My merged-geometry approach is arguably better, but I can use InstancedMesh for a few very repeated things: e.g., blossom clusters? They vary in size/rotation/color — InstancedMesh supports per-instance matrix + color. That's a good fit and reduces vertex count massively (a box = 24 verts shared). Let's use InstancedMesh for:
- blossom clusters (thousands) — big win.
- stones (thousands) — big win! Stones vary in size/rotation/color → perfect for instancing.
- roof tiles? no.
- fence posts, lantern parts, planks? Could, but merged is fine.

Hmm, but instanced meshes with per-instance color require `instanceColor` (setColorAt). Supported. And shadows work with InstancedMesh.

Decision: use InstancedMesh for stones and blossoms and grass tufts and paving stones (the highest-count repeated items), and merged geometry for architecture. That's a good hybrid and demonstrates both techniques.

But careful: instanced boxes with rotation — set matrix via compose with quaternion from Euler. Fine.

For the terrain, merged quads.

Let me define an `InstanceBuilder` helper:
```js
class InstanceGroup {
  constructor(geometry, material, max){ this.mesh=new THREE.InstancedMesh(geo,mat,max); this.i=0; }
  add(matrix, color){ this.mesh.setMatrixAt(this.i, matrix); this.mesh.setColorAt(this.i, color); this.i++; }
  finalize(){ this.mesh.count=this.i; this.mesh.instanceMatrix.needsUpdate=true; if(this.mesh.instanceColor) this.mesh.instanceColor.needsUpdate=true; }
}
```
Use a shared BoxGeometry(1,1,1). Note: InstancedMesh with vertexColors? Use material with color white and instanceColor multiplies. Yes, instanceColor multiplies material color.

Cap: allocate max generously (e.g., 12000 stones, 8000 blossoms).

Memory: 12000 instances × (16 floats matrix + 3 color) = fine.

**Water shader**

```glsl
// vertex: pass world pos, uv
// fragment: 
vec3 n = normalize(vec3(rippleGrad, 1.0));
float fres = pow(1.0 - max(dot(n, viewDir),0.0), 3.0);
vec3 skyCol = mix(uHorizon, uZenith, ...);
vec3 deep = uDeepColor;
vec3 col = mix(deep, skyCol, fres*0.85);
// sun specular
float s = pow(max(dot(reflect(-uSunDir, n), viewDir),0.0), 60.0) * uSunI;
col += s;
// shoreline foam: based on distance to bank? use a uniform texture-free approach: use the depth from a precomputed "waterDepth" attribute? 
```
Simplify: alpha varies with a noise; add subtle color bands. Also add a slight reflection of "castle silhouette"? Skip.

I'll compute ripple normals from 2 sine waves in world space + noise via sin combos. Cheap and looks decent.

Water geometry: rather than one big quad, generate the moat water as a ring mesh matching the moat band shape. Easiest: a large plane covering the whole world at y=-1.4, but then water would appear outside the moat (where terrain is at 0, it'd be hidden under the terrain — since terrain top is at y=0 > -1.4, water is hidden except in depressions!). That's a neat trick: one big plane at y=-1.4, hidden wherever terrain ≥ -1.4. But the town plain is exactly 0, so the plane is 1.4 below and invisible. And in the moat trench (bottom -3.2), the water shows. Also the river/basin. 

But: transparent water under opaque terrain — depth test handles it (terrain is above and opaque, so it occludes). Yes. And the plane's edges at the world boundary would be visible beyond the terrain edge — make the terrain extend beyond the water plane, or make the water plane smaller than the terrain. Terrain is 422 wide; water plane 400 wide. Also mountains at radius 600 sit beyond; the water plane edge at ±200 would be visible from a high camera as a straight line where terrain is below -1.4... but terrain outside is ≥ 0 mostly (outskirts noise can dip below -1.4? noise amplitude up to 6 upward; I'll clamp outer terrain to ≥ 0.5). Let me make the outer noise only positive: h += outer * abs(noise)*7 + 0.6. Then no water leaks. 

Also the water plane needs to not be visible from below (camera can't go below ground if we clamp maxPolarAngle). I'll clamp OrbitControls maxPolarAngle to ~1.52 (just under horizon) so the camera stays above ground level... Actually the camera orbits around a target; clamping polar to < π/2 keeps it above the target's horizontal plane. Good enough.

Also add a river: a channel from the moat going east/south to a water basin near the warehouse district. Terrain: carve a channel with the same depth. Water plane handles it. Boats on the basin. 

**Sky**: large sphere (radius 900) with BackSide ShaderMaterial: gradient from horizon color to zenith color + sun disc glow + a few procedural blocky clouds? Clouds: I'll add separate voxel cloud clusters far away (large soft white boxes at y=180-240, radius 500-700) — they'd look like floating blocks. Hmm, risky. Alternative: procedural clouds in the sky shader using a hash noise on the dome — smooth, cheap, looks nice. Let's do a simple fbm cloud band in the fragment shader with a soft threshold, colored by mode. That's elegant and procedural.

**Mountains**: ring at radius 520-700, generated as a merged mesh of blocky peaks: for i in 0..N, angle, radius r, height 40-120, made of 3-5 stacked boxes (voxel mountains) with vertex colors (dark blue-green, lighter/snow at top). They'll be fogged. Good background layering. Also a distant mountain range behind the castle for the front view (Fuji-like silhouette? A conical peak with snow cap at radius 600 in the north — nice!). Let's add one prominent snow volcano to the north-northwest (visible in the isometric hero view from the SE). Yes, that's a strong composition element.

**Fog**: THREE.FogExp2 with color matching the horizon, density ~0.0016 (at 400 units → e^-(0.64)=0.53 — too strong?). Let's use linear Fog(near=250, far=1100) so the town stays crisp and the mountains fade. Actually for a diorama, distant fog on the town edges helps. Use Fog(color, 320, 1400). Mountains at 600 → 50% fogged. Good.

**Post-processing**: skip (no EffectComposer to keep it simple/robust). Instead: renderer.toneMapping = ACESFilmic, toneMappingExposure ~1.0, outputColorSpace sRGB. Careful: with vertex colors and ACES, colors get desaturated; use exposure ~1.15 and slightly saturated palette. I'll use `THREE.ACESFilmicToneMapping` — it gives a cinematic look. Hmm, ACES can wash out pastel pinks. Maybe use `NoToneMapping` with carefully chosen colors, or `ReinhardToneMapping`? I'll go with ACES at exposure 1.15 and boost palette saturation a bit. Actually, safer: use LinearToneMapping? Not available in newer three? `THREE.LinearToneMapping` exists. Let me just use ACESFilmic — it's well-behaved and cinematic.

Hmm, one risk: with `renderer.outputColorSpace = SRGBColorSpace` (default in r152+), colors set via `new THREE.Color(hex)` are converted... Actually `Color.setHex(hex)` assumes sRGB and converts to linear-srgb working space when `ColorManagement.enabled` (default true in r155+). Vertex colors written directly into a buffer are treated as linear working-space values. So if I do `color.setHex(0xff0000)` then read `color.r`, I get the linear value (0.735). Writing that into the vertex buffer is correct (working space = linear). Good — so I should use `new THREE.Color(hex)` and read .r/.g/.b for vertex colors. That's correct color management.

For InstancedMesh setColorAt: `setColorAt(i, color)` — in recent three, it does `color.toArray(...)` without conversion? Let me check: `InstancedMesh.setColorAt(index, color)` → `this.instanceColor.setXYZ(index, color.r, color.g, color.b)`. And the shader multiplies diffuse by instanceColor. Since the material color is also converted from sRGB to linear, and our Color objects are already linear (from setHex with ColorManagement), it's consistent. Good.

Version: use three r168+ from unpkg with importmap:
```html
<script type="importmap">{"imports":{"three":"https://unpkg.com/three@0.160.0/build/three.module.js","three/addons/":"https://unpkg.com/three@0.160.0/examples/jsm/"}}</script>
```
r160 is stable and widely cached. OrbitControls from `three/addons/controls/OrbitControls.js`. Good.

Note: in r160, `renderer.outputColorSpace` default is SRGB. `useLegacyLights` default false in r155+. Light intensities need to be physically-ish: DirectionalLight intensity ~2-3, Hemisphere ~1-2 with the new lighting mode. I'll tune: sun 2.6, hemi 1.2, ambient 0.35. With ACES exposure 1.0. Should be reasonable.

Hmm, risk of over/under exposure without testing. Let me pick: sun intensity 2.2, hemi 0.9, ambient 0.25, exposure 1.05. MeshStandardMaterial roughness 0.9, metalness 0. Direct diffuse: color * albedo * intensity * NdotL / π? In three's physically-correct mode, `DirectLightUtil`... For a directional light, the diffuse = albedo * lightColor * intensity * NdotL * RECIPROCAL_PI? Actually in three, for MeshStandardMaterial with a directional light: `irradiance = light.color * light.intensity * dotNL` then `reflectedLight = irradiance * BRDF_Lambert(diffuseColor)` where BRDF_Lambert = RECIPROCAL_PI * diffuseColor. So a white surface lit by intensity π (3.14) at dotNL=1 gives radiance 1. So sun intensity ~2.5 gives ~0.8 → good with exposure 1.1. Hemisphere: irradiance = mix(groundColor, skyColor, ...) * intensity → also divided by π in irradiance handling? For hemisphere light, three computes irradiance directly (not divided by π): `irradiance = mix(groundColor, skyColor, hemiDiffuseWeight) * intensity`... Actually in `lights_fragment_begin`, `getHemisphereLightIrradiance` returns `mix(groundColor, skyColor, hemiDiffuseWeight) * intensity` — hmm, in r155+ with physically correct lights, hemisphere irradiance = color * intensity * PI? Let me recall: In r155, `getHemisphereLightIrradiance` returns `mix(groundColor, skyColor, hemiDiffuseWeight) * intensity` and then it's multiplied by BRDF_Lambert (÷π). With useLegacyLights=false, ambient/hemisphere intensities are multiplied by PI internally? There was a change: "Lighting mode" — with physically correct lights, AmbientLight intensity is used directly. I'll just set hemi ~1.5 and ambient ~0.6 and accept. Since I can't test, I'll aim for a mid-brightness look and add an exposure slider? No — keep it simple but choose safe values: sun 2.4, hemi 1.6, ambient 0.5, exposure 1.0. If slightly bright, ACES compresses highlights gracefully. Good.

Actually, let me reduce risk: use MeshLambertMaterial for most geometry? Lambert in three also uses the same light math (BRDF_Lambert). Same exposure. Standard adds specular which we don't need much. Lambert is cheaper. But Standard with roughness gives nicer sky ambience... I'll use MeshLambertMaterial for the bulk (fast, no specular noise) — hmm, but Lambert has no roughness/fresnel and looks flat-ish. Given we already have normal-based shading variation and AO baked in vertex colors, Lambert is fine and fast. Let me use MeshPhongMaterial? No.

Decision: MeshLambertMaterial for merged architecture/terrain (fast, stable), MeshStandardMaterial only for water (custom shader anyway) and maybe gold ornaments (metalness 0.6, roughness 0.3, emissive slight) to make the shachihoko sparkle. Gold as separate small mesh with Standard material + emissive.

Hmm, Lambert in three r160 does support emissive, fog, shadows. Good.

**Shadows**: castShadow on the merged architecture meshes (castle, town, trees), receiveShadow on terrain + architecture. With one giant merged mesh, self-shadowing works but the shadow camera must cover everything. I'll split into a few meshes:
- `gTerrain` (receive only)
- `gCastle` (cast+receive) — the hero
- `gTown` (cast+receive)
- `gNature` (cast+receive)
- `gProps` (cast+receive)
- `gWater` (no shadow)
- instanced: stones (cast+receive), blossoms (cast+receive), etc.

Shadow map: 4096, ortho ±150 centered on the castle area (the town outskirts beyond ±150 lose shadows — acceptable, and I can shift the shadow camera to follow the camera target... but with static shadow map updates, that breaks. Keep static, cover ±170 with 4096 → 0.083/texel. Good.)

Actually, since the sun direction changes only on mode switch, static shadow map is fine.

Hmm: with `shadowMap.autoUpdate=false`, we must set `needsUpdate=true` after the scene is built and after each light change. Also the shadow camera needs `updateProjectionMatrix()`.

One more consideration: shadow acne on merged geometry — set `shadow.bias = -0.0008`, `normalBias = 0.6`. With voxel geometry (axis-aligned faces), normalBias 0.5-1.0 works well.

**Night mode**: 
- Sun → moonlight: color 0x8fb4ff, intensity 0.5, direction from a different angle.
- Hemi: sky 0x2a3a66, ground 0x141a24, intensity 0.55.
- Ambient: 0x223055, 0.35.
- Sky shader: deep blue gradient + stars (procedural hash stars in the shader!) — nice touch.
- Lantern glow meshes visible (MeshBasicMaterial with warm color, plus additive glow billboards).
- Window glow: I could add small emissive quads in windows at night — a great night look. Implement: during town generation, for ~40% of windows, register a "glow quad" into a separate `glowBuilder` (a merged mesh with MeshBasicMaterial, warm color, visible only at night). Cheap and dramatic. Yes, do it.
- Fog color darker blue.

**Twilight**: sun low, warm orange (0xff8a4a), intensity 2.6, long shadows; sky gradient orange→violet; fog pinkish; lanterns on; blossoms tinted by warm light. Beautiful.

Transitions: lerp over 1.5s between mode parameter sets (colors, intensities, fog, sky uniforms, exposure). Implement `applyMood(t)` interpolating between from/to.

**Picking**: register pickables during generation:
```js
pickables.push({box, label, category, meta})
```
Raycast: `raycaster.setFromCamera(mouse, camera)` then loop `ray.intersectBox(box, target)` and pick the nearest hit with the smallest distance... `ray.intersectBox(box, target)` returns the hit point or null. Choose min distance from ray origin. Good.

Highlight: a `Box3Helper`-like custom: create a LineSegments box (unit box edges) and set position/scale from the box; animate a pulse via material opacity. Plus a translucent fill box. Plus a ground ring? Keep: edges + fill.

Info panel: show category, name, and a short description line (e.g., "Five-tiered keep · 1610"). Add flavor text per category. Nice polish.

**FPS counter**: compute over 0.5s windows.

**Mesh/triangle counts**: renderer.info.render.triangles + total objects.

**UI design**: 
- Top-left: title card "桜 SAKURA-JŌ · Castle Town Diorama" with a thin vermilion accent bar, small subtitle.
- Top-right: stats (FPS, tris, draws, buildings/trees counts).
- Bottom-center: camera preset pills.
- Right side or bottom-left: mode buttons (Day / Twilight / Night) + toggles (auto-rotate, shadows?).
- Bottom-left: selected object card (slides in).
- Hint text: "drag rotate · scroll zoom · right-drag pan · click to inspect".
- Loading overlay with a progress bar while generating (generation is synchronous; I'll chunk it with setTimeout/requestAnimationFrame steps so the progress bar animates and the browser doesn't appear frozen). That's a nice touch: a staged builder with `yield`-like steps. Implement as an array of step functions run one per frame with progress updates. Good idea — avoids the "page unresponsive" look and lets me show a nice loading screen with Japanese styling.

Let me implement the generation as a list of named steps, each executed on a frame, updating the progress bar, then final reveal (fade out the loader, camera intro animation: a slow dolly from a wide shot into the hero view).

Intro camera animation: start far/high, ease into the hero preset over 3s. Nice.

**Auto-rotate**: OrbitControls.autoRotate toggle button, slow.

OK. Now let me write the actual code. I'll be systematic and careful.

Let me start writing.

```html
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Sakura-jō — Castle Town Diorama</title>
<style> ... </style>
</head>
<body>
<div id="app"><canvas id="c"></canvas></div>
<div id="loader">...</div>
<div id="ui">...</div>
<script type="importmap">...</script>
<script type="module"> ... </script>
</body>
</html>
```

CSS: dark glass panels, backdrop-filter blur, font stack with a serif for the title (Georgia/'Hiragino Mincho'), vermilion accent #e2543f, gold #d9b26a. Rounded 10px, subtle borders rgba(255,255,255,.12). Buttons with hover states, active state.

Now the JS. Let me write it out.

```js
import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
```

Constants and utils... then Builder... then world gen.

Let me carefully write the Builder class.

```js
const _e = new THREE.Euler(), _q = new THREE.Quaternion(), _v = new THREE.Vector3(), _s = new THREE.Vector3(), _m = new THREE.Matrix4(), _m2 = new THREE.Matrix4();
const _c = new THREE.Color();

class Builder {
  constructor(name, opts={}) {
    this.name = name;
    this.vCount = 0; this.iCount = 0;
    this.cap = 4096;
    this.pos = new Float32Array(this.cap*12);
    this.nor = new Float32Array(this.cap*12);
    this.col = new Float32Array(this.cap*12);
    this.idx = new Uint32Array(this.cap*6);
    this.M = new THREE.Matrix4();
    this.stack = [];
    this.ao = opts.ao !== false;
    this.jitter = opts.jitter || 0;
  }
  _ensure(nv, ni){
    if(this.vCount+nv <= this.pos.length/3 && this.iCount+ni <= this.idx.length) return;
    let cap = this.cap;
    while(cap*4 < (this.vCount+nv)*2) cap*=2;   // hmm
  }
}
```
Let me simplify capacity: track `capV` (vertices) and `capI`. Grow by doubling until fits.

```js
_ensure(nv, ni){
  if(this.vCount+nv > this.capV){
    let c=this.capV; while(this.vCount+nv>c) c*=2;
    const p=new Float32Array(c*3); p.set(this.pos.subarray(0,this.vCount*3)); this.pos=p;
    ... same for nor, col
    this.capV=c;
  }
  if(this.iCount+ni > this.capI){
    let c=this.capI; while(this.iCount+ni>c) c*=2;
    const a=new Uint32Array(c); a.set(this.idx.subarray(0,this.iCount)); this.idx=a; this.capI=c;
  }
}
```

pushBox:
```js
box(x,y,z,sx,sy,sz,color,rx=0,ry=0,rz=0){
  this._ensure(24,36);
  _e.set(rx,ry,rz);
  _q.setFromEuler(_e);
  _v.set(x,y,z); _s.set(sx,sy,sz);
  _m.compose(_v,_q,_s).premultiply(this.M);
  _m2.compose(_ZERO,_q,_ONE).premultiply(this.M); // for normals
  const cr = color.r ?? color, ...
```
I'll accept colors as THREE.Color instances (pre-created constants) to avoid allocation. Also allow a number → convert with a cached map? Converting per box is a `setHex` call (cheap-ish, does sRGB→linear via a lookup pow). Let me pre-create all palette colors as THREE.Color constants and pass those. For jitter, multiply by a factor.

Writing verts:
```js
const p=this.pos,n=this.nor,c=this.col; let vi=this.vCount*3;
for(let f=0;f<6;f++){
  const base=f*4;
  // for each of 4 corners
  ...
}
```
I'll precompute template arrays `TPOS` (24×3), `TNOR` (24×3), `TIDX` (36). Then:
```js
for(let i=0;i<24;i++){
  _tv.fromArray(TPOS,i*3); _tv.applyMatrix4(_m);
  p[vi]=_tv.x; p[vi+1]=_tv.y; p[vi+2]=_tv.z;
  _tn.fromArray(TNOR,i*3); _tn.applyMatrix4(_m2).normalize();  // applyMatrix4 on a direction is wrong (translation) — but _m2 has no translation, so OK.
  n[vi]=_tn.x; ...
  const sh = shadeFor(_tn.y);
  c[vi]=cr*sh*j; ...
  vi+=3;
}
```
Wait: applyMatrix4 with a matrix that has no translation is fine for directions. But `premultiply(this.M)` where M may have translation → _m2 would have translation. So I must build the normal matrix without translation: use `_m3.makeRotationFromQuaternion(_q).premultiply(this.M)` then zero the translation, or use Matrix3.getNormalMatrix. Let me use a Matrix3: `_m3.getNormalMatrix(_m)` — but _m includes non-uniform scale (sx,sy,sz) which the normal matrix correctly handles! And this.M (rigid) too. So `_m3.getNormalMatrix(_m)` is exactly right. Then `_tn.applyMatrix3(_m3).normalize()`. 

Indices: `idx[iCount+k] = vCount + TIDX[k]`.

Then `this.vCount+=24; this.iCount+=36;`

Shade function: `sh = 0.86 + 0.20*Math.max(0,ny) + (ny<-0.5? -0.12:0)`. Let me define: if ny>0.5 → 1.06; if ny<-0.5 → 0.62; else → 0.88 + 0.06*|nx| ... simpler: `sh = 0.90 + 0.16*ny` clamped, giving top 1.06, sides 0.90, bottom 0.74. Sides facing away from a fixed light direction could get extra variation: add `+ 0.05*(nx*0.7+nz*0.7)` to fake directional ambient. Let's do: `sh = 0.92 + 0.15*ny + 0.05*(nx*0.6 - nz*0.8)`. This gives faces a subtle directional read even in shadow. Nice for voxel art. Clamp to [0.6, 1.12].

Jitter: multiply by (1 + (rng()-0.5)*jitter).

Also add `boxC(x,y,z,...)` that takes a hex number and converts — I'll just always pass Color objects.

Transform stack:
```js
push(){ this.stack.push(this.M.clone()); }
pop(){ this.M.copy(this.stack.pop()); }
translate(x,y,z){ this.M.premultiply(new THREE.Matrix4().makeTranslation(x,y,z)); }
```
Careful with order: this.M maps local→world. Applying a local transform T means newM = M * T → `this.M.multiply(T)`. Yes: `this.M.multiply(mat)`.

So:
```js
move(x,y,z){ _m.makeTranslation(x,y,z); this.M.multiply(_m); }
rotateY(a){ _m.makeRotationY(a); this.M.multiply(_m); }
```
And a convenience:
```js
local(x,y,z,ry,fn){ this.push(); this.move(x,y,z); if(ry) this.rotateY(ry); fn(this); this.pop(); }
```
Allocation: `push()` clones a Matrix4 each time — thousands of allocations, fine (GC handles it), but let me pre-allocate a stack array of Matrix4 objects and grow.

Also `count` of boxes for stats.

Additional primitives:
- `quad(a,b,c,d,color)` for terrain (world-space points, no transform).
- `prism(...)`: a triangular prism for shed roofs / simple gables. Params: (x,y,z, sx,sy,sz, axis) making a wedge. I'll implement as a custom 6-vert/... Let me skip prisms and use rotated boxes for slopes (rotated boxes look fine for awnings and shed roofs).

Terrain meshing needs quads with arbitrary heights: I'll write a dedicated terrain function that writes into a Builder via a `rawQuad(p0,p1,p2,p3,color)` method that computes the normal from the cross product.

OK.

Now the world generation code. Let me lay out the constants.

```js
const W = {
  cell: 2.4, grid: 176, // terrain
  hill: { hx:0, hz:-6, rx:56, rz:48, cr:20 },  // rounded rect SDF params, center slightly north
  moat: { inner: 30, outer: 50 }, // sdf band
  waterY: -1.5,
};
```
Rounded-rect SDF:
```js
function rrSDF(x,z, rx,rz, cr){
  const qx=Math.abs(x)-rx+cr, qz=Math.abs(z)-rz+cr;
  const ox=Math.max(qx,0), oz=Math.max(qz,0);
  return Math.hypot(ox,oz)-cr + Math.min(Math.max(qx,qz),0);
}
```
Standard rounded box SDF (2D): 
```
d = length(max(vec2(|p|-(b-r)),0)) + min(max(px-r,pz-r),0) - r
```
where b = half extents, r = radius. Let me write:
```js
function rrSDF(px,pz,bx,bz,r){
  const ax=Math.abs(px)-(bx-r), az=Math.abs(pz)-(bz-r);
  const mx=Math.max(ax,0), mz=Math.max(az,0);
  return Math.hypot(mx,mz) + Math.min(Math.max(ax,az),0) - r;
}
```
Yes.

Castle center: let me put the castle at (0, -8) so the town has more room to the south. Define `cx0=0, cz0=-6`. s = rrSDF(x-cx0, z-cz0, 54, 46, 18).

Terrain height bands (with s):
- s < -18 → 20 (main bailey)
- s < -6 → 14.5
- s < 4 → 9.5
- s < 14 → 5.5
- s < 22 → 2.4
- s < 28 → 0.8
- 28..34 → ramp down to 0 (edge of the outer earthwork)
- 34..52 → moat trench: depth profile -3.4 at center (43), rising to 0 at edges. Use smooth cosine.
- 52..58 → ramp up to 0.6 (outer bank / ring road embankment)
- >58 → 0 + outskirts noise.

But the terraces should be smooth-ish steps with steep risers (the stone walls sit on the risers). With a grid of 2.4 units, a step from 20 to 14.5 across one cell is a vertical cliff — exactly what we want (the stone wall covers it). But the terrain mesh will produce a vertical face there, which I'll color as earth/stone, and then place the actual stone wall geometry over it. Good.

To avoid z-fighting/poke-through, the stone wall face should be offset outward by ~0.3 from the terrain step. Since the terrain step is a vertical cliff at the terrace edge, and my stone wall is built along the terrace polygon slightly outside it, it will cover the cliff. I'll make the stone wall's inner face overlap the terrain (build it so it intersects the hill) — safe.

Also, the moat banks: the trench profile should be smooth-ish; with 2.4 cells it'll be blocky, which fits the voxel style.

Let me define the height function with a helper `moatProfile(s)`.

Also: the bridges need dry crossings — the water plane is at -1.5 and the bridge deck at ~0.6, fine.

Also the castle's inner area: the terrain inside the baileys is flat at 20/14.5/9.5/5.5. Buildings (keep, yagura) sit on those.

Now, the roads crossing the moat: at the south, the road at x≈0 crosses the moat; the terrain there would be a trench. That's correct (the bridge spans it). But the road surface on either side continues. Fine.

Also the east bridge at z≈-6, x≈+? The moat band in the +x direction: s=34..52 corresponds to x ≈ 54+34=88 to 106 (roughly, since along the axis s ≈ x - bx for the flat sides). So the moat spans x∈[88,106] on the east side. Bridge from x=84 to x=110.

Let me now define the town grid precisely.

Town region: outside s>58. On the +z (south) side, the moat outer edge is at z ≈ -6+46+52 = 92. So the town starts at z≈98 south. On +x: x ≈ 0+54+52 = 106 → town starts x≈112. Hmm, that's a big empty ring. The ring road at s≈60 (radius ~112 from center on the axes) hugs the moat. Buildings line the ring road. Good — the ring road is the town's inner boundary.

Let me define the ring road as a rounded rect polyline at half-extents (bx=54+60-18? ...). Simpler: define the ring road as a rounded rect path with half-extents (112, 104) and corner radius 40, sampled into ~40 points → segments. Buildings along it face outward (or inward where appropriate). Actually buildings facing the moat (inward) would be nice for the "castle view" — but the outer bank is between. Let's have buildings face outward mostly, plus a row facing inward along the inner side near the bridges.

Hmm, generating buildings along a curved road with rotation is more code. Alternative: make the ring road an octagon with 8 segments (4 axis-aligned long sides + 4 diagonal corners). Buildings on the axis-aligned sides are easy; on the diagonals, rotate by 45°. I'll write the generic segment-based placer that handles any angle (using AABB occupancy). It's not much extra code.

Let me define roads as a list:
```js
const roads = []; // {x1,z1,x2,z2,w,name,type}
function road(x1,z1,x2,z2,w,type='street'){ roads.push({x1,z1,x2,z2,w,type}); }
```
Types: 'avenue' (13), 'street' (8), 'alley' (4.5), 'path' (3, dirt/stone).

Town roads:
- Main south avenue: (0,96)→(0,200) w=14. Actually the bridge ends around z=98. Let's start the avenue at z=94.
- Cross streets: 
  - z=118: x from -120 to 130, w=9
  - z=150: x from -140 to 140, w=8
  - z=182: x from -120 to 120, w=7
  - z=-100 (north of castle): x -100..100, w=6 (behind the castle, sparse)
- East avenue: x=130, z from -90 to 195, w=10
- West avenue: x=-130, z from -80 to 180, w=9
- North-south connectors: x=-60 (z 100..190, w=7), x=60 (z 100..190, w=7), x=95 (z 60..190, w=6)
- Alleys: several short ones within blocks: e.g., x=±30, ±90, ±160 segments; z=105,132,165,172,195 segments.
- Shrine approach: from (130,-40) to (168,-92) diagonal w=6, then stone steps.
- West gate road: from (-112, 20) west to (-160, 20) w=7.
- North-east road to the pagoda.

Hmm, I need to be careful that roads don't run through the moat/castle. Roads outside s>58 only. Let me place the town mostly in the south (z from 96 to 200) and east (x from 112 to 190) and west (x from -190 to -112) and a bit north.

Wait — with the moat outer edge at ~106 on the axes, the town band from 112 to 190 is only ~78 deep. That's fine for 2-3 rows of buildings per block. Total town area: a ring around the castle. The south gets the most.

Hmm, the world is 422 wide (±211). Let me expand: make the castle smaller relative to the world. Reduce the castle: half-extents (44, 38), r=16; moat band s∈[26,42]; so the moat outer edge on the axes is at 44+42=86 (east) and z = -6+38+42 = 74 (south). Hmm, asymmetric. Let me center the castle at (0,0) exactly and use half-extents (46,42), r=16.

Moat: s ∈ [26, 44] → water band. Outer edge: x=90, z=86. Town from ~92 outward to ~200. That's a 108-deep town band. 

Castle terraces: s<-16 → 20; s<-6 → 15; s<2 → 10; s<10 → 6; s<18 → 2.6; s<24 → 1.0; s<26 → 0.4; moat 26..44 (depth -3.6 center at 35); 44..50 → 0.8 bank; >50 → plain.

The main bailey (s<-16) is a rounded rect of half-extents (46-16, 42-16) = (30,26) plus radius... roughly 60×52 units. The keep footprint 26×22 fits with room for a courtyard and yagura. Good.

Second bailey (s<-6): half-extents (40,36) → 80×72 area at height 15. Third (s<2): (48,44) at height 10.

Hmm, the terraces are quite narrow (6 units between rings) — the stone walls are ~5 tall each. That's OK but the courtyards are thin. Let me widen: 
- s<-20 → 20 (main bailey: half-extents (26,22) → 52×44 — keep is 26×22, leaving a courtyard ring of ~10. OK.)
- s<-8 → 14.5 (half-extents (38,34))
- s<4 → 9 (half-extents (50,46))
- s<14 → 4.5 (half-extents (60,56))
- s<22 → 1.6 (half-extents (68,64))
- s<26 → 0.5
- moat 26..44
- 44..50 bank 1.0

Distances between terrace rings: 12, 12, 10, 8 units. Height drops 5.5, 5.5, 4.5, 3. Slopes ~26° — reasonable for ishigaki (they're usually steeper, 35-45°, but with terraces it's fine). Actually I want steeper walls: the stone wall face goes vertically from the lower terrace to the upper terrace edge, and the terrain between is a slope. Let me make the terrain step happen over ~2 units (near-vertical) and place the stone wall on it. So the terrace edge is at the ring boundary and the riser is right at the boundary. With the grid at 2.4, the height function should transition over one cell. I'll implement bands with a hard step at the boundary (quantized), so the riser is at the boundary. Then the stone wall is built along the boundary polygon, vertical, covering the riser.

But the moat needs sloped banks. Use a smooth profile there.

OK. Let me write `terrainHeight`:

```js
function castleTerrace(s){
  if(s < -20) return 20;
  if(s < -8)  return 14.5;
  if(s < 4)   return 9;
  if(s < 14)  return 4.5;
  if(s < 22)  return 1.6;
  if(s < 26)  return 0.5;
  return null;
}
function moatDepth(s){ // s in 26..44
  const t=(s-26)/18; // 0..1
  return -3.8*Math.sin(Math.PI*t)**0.7; // 0 at edges, -3.8 center
}
```
Then:
```js
function baseHeight(x,z){
  const s = castleSDF(x,z);
  let h;
  const t = castleTerrace(s);
  if(t!==null) h=t;
  else if(s<44) h = moatDepth(s);
  else if(s<50) h = 1.0*smoothstep(44,50,s)... 
  else h = 0;
  // outskirts
  const r = Math.max(Math.abs(x),Math.abs(z));
  const out = smoothstep(150,215,r);
  h += out*(fbm(x*0.012,z*0.012)*10 + 1.5);
  // shrine hill (east-northeast)
  h = Math.max(h, shrineHill(x,z));
  // river channel
  ...
  return h;
}
```
Careful: `Math.max(h, shrineHill)` in the moat region would fill the moat — the shrine hill is far from the moat, fine.

Shrine hill: center (140, -78), a terraced mound: radius 46, height 12, 3 terraces. Use distance-based steps. Also it should be inside the world (x up to 186 < 211 ✓).

River: a channel from the moat's east side going south-east to the world edge, plus a basin near the warehouse district at (150, 120). Implement as a distance-to-polyline channel: `riverDepth(x,z)` = -3.6 where dist < 9, ramping to 0 at dist 14. Take min with the base height (but not below -3.8). And the water plane at -1.5 fills it.

River path: from (90, 20) [east moat] → (130, 40) → (150, 80) → (145, 130) basin → (170, 190) → edge. Hmm, this cuts through the town. A river through a town is fine and adds bridges (nice!). But it complicates building placement (occupancy: mark the river as blocked). I'll mark it blocked via a `blockedAt(x,z)` check in the placer.

Actually, let me simplify: the river comes from the north-west, passes under the north moat? No. Let me do: a river along the east side, from (100, -60) curving south to (170, 200), width ~10, with a wider basin at (150, 110). Bridges cross it on the east roads. Boats in the basin. Warehouse district hugs the basin. That's a great composition (water + boats + warehouses).

But the shrine hill is at (140,-78) — the river at (100,-60) would be adjacent. Move the shrine hill to (150, -110)? Then it's near the world corner. Let me restructure the compass layout:

- Castle at center (0,0).
- Town: south (z>92) main, east (x>92), west (x<-92), north sparse.
- Shrine hill: northeast at (135, -95), radius 40.
- River: enters from the northwest (-150,-150)? and flows to the southeast corner (200, 200)? It would cut the whole map diagonally. Instead: river enters from the north at (60, -210), flows south to (75, -120), then east-south to (150, -40)... conflicts with the shrine.

Simpler: river along