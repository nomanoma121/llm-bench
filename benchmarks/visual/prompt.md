Write a single `index.html` that renders an interactive 3D scene of a stylized Japanese castle town in spring, built from voxel-style blocks with Three.js.

You get one answer and no tools: you cannot run or view the page. Keep your planning short (a brief outline, not a detailed design) and spend most of your answer on the code. The whole answer must fit in about 20,000 tokens; aim for 500–900 lines of HTML and JavaScript. Finish the file: a complete, working page matters more than any single detail.

## Scene

- A five-level castle keep on a raised stone base at the center, each level smaller than the one below, with curved, layered roofs, white walls and dark wooden trim.
- Stone walls, a moat with water around the castle, at least one bridge and a main gate.
- A castle town of roughly 60–100 small buildings to the south and east: varied houses, shops and a few larger warehouses, with roads, a few narrow alleys and a market square.
- 40–60 cherry blossom trees in pink (concentrated along the moat and main road) and some green trees.
- A small shrine with a torii gate, stone lanterns, fences and a few other props.
- Gentle terrain: the castle sits clearly above the town.

## Look

- Voxel-inspired but with real proportions and layered detail, not a pile of plain cubes.
- Soft spring daylight with shadows, light fog for depth, and a pleasant default camera view of the whole town with the castle as the focal point.

## Interaction

- OrbitControls to rotate, zoom and pan.
- A key or button that toggles between day and dusk lighting.

## Technical rules

- One self-contained `index.html` with HTML, CSS and JavaScript.
- Load Three.js and its addons from a CDN with an import map, for example `https://cdn.jsdelivr.net/npm/three@0.170.0/build/three.module.js` and `https://cdn.jsdelivr.net/npm/three@0.170.0/examples/jsm/`.
- No external models, textures or images: create all geometry in code.
- Use a seeded random generator with seed `1337` so the layout is the same on every load.
- Reuse geometries and materials, and use InstancedMesh or merged geometry for repeated parts so it runs smoothly.
- Handle window resizing.

## Output

Reply with the complete file in one ```html code block and nothing after it.
