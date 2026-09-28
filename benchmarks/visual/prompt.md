You are being evaluated as an autonomous coding agent.

Your task is to build a complete, interactive, high-detail 3D scene in the browser representing a stylized Japanese castle and a large surrounding castle town.

This is an intelligence-oriented benchmark, not a speed benchmark.

Do not rush.

Take the time needed to reason carefully, design the architecture, implement the scene, inspect the result, identify weaknesses, and improve it.

Do not ask clarifying questions.

Make reasonable engineering and artistic decisions yourself.

The final deliverable must be a working `index.html`.

---

# Primary objective

Create a visually impressive Japanese castle town entirely from procedurally generated geometry.

The scene should use a block-based / voxel-inspired visual language, but it must NOT look like a simplistic Minecraft build or a collection of primitive cubes.

The intended style is:

**a highly detailed, handcrafted voxel architectural diorama with realistic proportions, layered construction, strong visual depth, cinematic composition, and a beautiful Japanese spring atmosphere.**

The result should feel like a premium showcase environment.

It should be visually striking enough that a good screenshot immediately creates a strong “wow” reaction.

A technically correct but visually crude result is not sufficient.

---

# What this benchmark evaluates

The benchmark evaluates:

- planning quality
- architectural decomposition
- coding ability
- spatial reasoning
- visual judgment
- aesthetic judgment
- scene composition
- complexity management
- procedural generation
- performance awareness
- debugging
- self-critique
- iterative improvement
- ability to finish and polish a large task

Do not optimize primarily for completion speed.

Prefer a thoughtful, refined solution over a shallow implementation completed quickly.

---

# Technical constraints

Create a single:

`index.html`

Use:

- HTML
- CSS
- JavaScript
- Three.js

Three.js and official Three.js helper modules may be loaded from a public CDN.

Do NOT use:

- external 3D models
- downloaded textures
- image assets
- prebuilt castle assets
- external scene files
- iframe content
- external terrain data

All visible geometry must be created in code.

The application must work when served through a simple local HTTP server.

Use a deterministic random seed of:

`1337`

Any procedural generation must produce the same layout across repeated runs.

---

# Engineering requirements

Organize the implementation into clear logical systems or reusable functions.

Avoid large amounts of duplicated construction code.

Use reusable geometry, materials, helper functions, procedural generators, or InstancedMesh where appropriate.

Use descriptive names.

Correctly support browser resizing.

Handle device pixel ratio reasonably.

Avoid browser console errors.

Maintain interactive frame rates on a normal desktop computer.

Performance optimization is encouraged, but it must not significantly reduce visible detail.

---

# Overall scene scale

The environment should feel large and dense.

Do not create a small isolated castle with a handful of buildings around it.

Create a complete miniature world containing approximately:

- 100–160 town buildings
- 60–100 cherry blossom trees
- 30–60 additional green trees
- multiple castle courtyards
- multiple gates
- several yagura
- large stone fortifications
- multiple bridges
- several roads
- narrow alleys
- plazas
- markets
- shrines or temples
- many environmental props
- hundreds of visible stone elements
- dozens of lanterns
- dozens of fences
- several thousand visible geometric elements overall

The scene should feel dense from both aerial and street-level viewpoints.

---

# World layout

Use a coordinate system where the main castle complex is approximately centered around:

`(0, 0, 0)`

The castle should occupy elevated terrain.

The castle town should primarily extend toward the south and east, with additional development where compositionally appropriate.

The world should resemble a carefully composed miniature diorama rather than an infinite world.

---

# Terrain and elevation

Do NOT build the entire environment on one completely flat plane.

Use meaningful elevation changes.

Include:

- elevated castle baileys
- stepped terrain
- sloped embankments
- terraced stone walls
- raised paths
- stairs
- lower town districts
- moat depressions
- elevated shrine areas
- small terrain variation

The main castle should clearly sit above the surrounding town.

Elevation should create strong silhouettes and visually interesting viewpoints.

---

# Main castle

Create a large Japanese-style castle keep at the center.

It must contain five clearly distinguishable stacked levels.

Each successive level should generally become smaller than the level below.

The castle should immediately read as traditional Japanese architecture.

Use:

- white plaster-like walls
- dark tiled roofs
- visible wooden structure
- windows or firing openings
- balconies
- railings
- structural beams
- corner pillars
- projecting architectural elements
- visible entrances
- multiple roof tiers
- decorative roof ridges
- roof-end details
- thick eaves
- visible under-roof structure

Do NOT construct the keep as several boxes stacked vertically with simple roof slabs.

The keep should contain substantial geometric detail.

Aim for hundreds of visible architectural components in and around the main castle complex.

---

# Roof quality

Japanese roofs are one of the most important visual features.

Do NOT represent major roofs using:

- a single wedge
- a flat slab
- one pyramid
- one simple prism

Construct important roofs from multiple overlapping geometric sections.

Include:

- layered eaves
- thick roof edges
- stepped or gently curved silhouettes
- decorative ridges
- ridge-end details
- corner articulation
- supporting beams beneath roof edges

The silhouette should look convincing even from a distance.

---

# Castle complex

The main keep must not stand alone.

Create a substantial fortified castle complex containing:

- the main five-level keep
- at least 4 secondary yagura or towers
- multiple enclosed courtyards
- multiple defensive gates
- interconnected castle walls
- elevated baileys
- stone platforms
- paths
- stairways
- defensive passages
- smaller auxiliary structures

The environment should feel like a complete Japanese castle compound.

---

# Stone fortifications

Stonework must feel hand-built and irregular.

Do not create large smooth gray walls.

Use many individual stone elements with controlled variation in:

- size
- position
- depth
- height
- rotation
- tone

Stone surfaces should remain structurally coherent while still showing natural irregularity.

Important fortifications should visibly contain hundreds of stone elements or equivalent geometric detail.

Use sloped and stepped retaining walls where appropriate.

---

# Moat

Create a substantial moat around the inner castle area.

The moat should be visually integrated with:

- stone embankments
- walls
- bridges
- vegetation
- cherry trees
- gates

The water should be clearly distinct from the surrounding terrain.

Use appropriate reflections, transparency, roughness, or color variation if practical.

Do not make the moat a simple blue rectangle with no environmental integration.

---

# Bridges

Create multiple bridges where appropriate.

At minimum include a major wooden bridge on the south side leading toward the main gate.

Important bridges should contain:

- planks
- side structures or railings
- supports
- visible thickness
- realistic proportions

They should feel like actual constructed objects rather than flat rectangles.

---

# Main gate

Create a substantial and recognizable main castle gate on the south side.

It should visually connect:

- the bridge
- walls
- defensive structures
- castle roads
- interior courtyards

The gate should be one of the hero-detail structures.

Include strong architectural depth and layered roof construction.

---

# Defensive walls and yagura

Create substantial defensive walls around important parts of the castle.

Include:

- white wall surfaces
- dark roof caps
- wooden structure
- corners
- openings
- elevation variation

Include at least 4 secondary yagura.

Yagura should not all be identical.

---

# Castle town

Create a dense castle town rather than scattered houses.

The town should contain approximately:

100–160 buildings.

Buildings should follow streets and create coherent urban blocks.

Avoid placing houses at random positions across open terrain.

Create at least 3 visually distinct districts.

Examples include:

- merchant district
- residential district
- samurai residence district
- market district
- shrine or temple district
- warehouse district

The town should feel intentionally planned.

---

# Building variety

Town buildings must not all share the same dimensions or shape.

Create multiple building archetypes, such as:

- small merchant houses
- traditional machiya
- larger merchant buildings
- workshops
- inns
- warehouses
- samurai residences
- small shrines
- temple buildings
- tea houses

Vary:

- width
- depth
- height
- roof shape
- facade structure
- wooden accents
- window placement
- entrance style

Procedural variation should look intentional rather than noisy.

---

# Architectural detail density

Major structures must contain layered geometric detail.

Avoid important structures that could be represented convincingly using fewer than approximately 10 primitive components.

Important buildings should include combinations of:

- recessed walls
- projecting beams
- visible frames
- windows
- doorways
- roof supports
- layered facades
- wooden posts
- roof ridges
- balconies
- railings
- overhangs
- decorative structures

The closer the camera gets, the more additional detail should become visible.

---

# Roads and alleys

Create a coherent road network.

Include:

- main roads
- secondary streets
- narrow alleys
- intersections
- paths toward the castle
- paths toward shrines
- market streets

Roads should visually guide the viewer toward important landmarks.

Avoid perfectly uniform grid layouts unless intentionally modified.

Use road width, direction, elevation, and building placement to create believable urban structure.

---

# Sakura / cherry blossoms

Cherry blossoms are a major artistic feature of the scene.

They are not optional decoration.

Create approximately:

60–100 cherry blossom trees.

Each important cherry tree should contain:

- visible trunks
- branching structures
- multiple blossom clusters
- controlled variation in size
- controlled variation in shape
- pale pink and white blossom tones

Do NOT represent a cherry tree as one trunk with one pink cube.

Use many blocky or low-poly blossom clusters.

The trees should feel lush and visually abundant.

---

# Sakura composition

Do not distribute cherry trees randomly.

Use them intentionally to shape the composition.

Place sakura:

- along selected roads
- beside the moat
- around shrine grounds
- near stone walls
- around open plazas
- along castle approaches
- near bridges
- on elevated terraces

Create sakura-lined paths.

Create visible blossom clusters when viewed from above.

Use cherry trees to frame important camera views.

At least one hero viewpoint should contain cherry blossoms in the foreground framing the castle.

Balance sakura with green trees and architecture.

Do not make the entire environment uniformly pink.

---

# Additional vegetation

Include additional vegetation for contrast.

Use:

- green trees
- shrubs
- grass-like clusters
- small garden plants

Vegetation should support architecture rather than obscure it.

---

# Shrine or temple

Create at least one significant shrine or temple complex.

It should contain more detail than ordinary town buildings.

Possible elements include:

- torii gate
- stone approach
- lanterns
- stairs
- wooden buildings
- courtyard
- sakura
- surrounding fences

This area should provide another visually memorable destination besides the castle.

---

# Market area

Create at least one substantial market or town plaza.

Include:

- stalls
- awnings
- crates
- goods
- lanterns
- carts
- barrels
- signboards
- benches
- small props

The area should feel visibly different from ordinary residential streets.

---

# Environmental storytelling

Add many small environmental details.

Examples include:

- barrels
- crates
- wooden carts
- stacked firewood
- wells
- fences
- benches
- signboards
- lantern rows
- market goods
- awnings
- garden stones
- stepping stones
- small footbridges
- banners
- storage piles
- gates
- stone paths

Place these logically.

Do not scatter props randomly.

Use them to make streets and empty spaces visually interesting.

---

# Visual hierarchy

Use at least three levels of detail density.

## Hero detail

Spend the most effort on:

- main castle
- main gate
- important yagura
- shrine or temple
- major bridges

## Medium detail

Use substantial detail on:

- major merchant buildings
- main streets
- castle walls
- market areas
- major residences

## Background detail

Use simpler but still coherent construction for:

- ordinary houses
- distant structures
- background vegetation

Do not distribute complexity uniformly.

The viewer's attention should naturally move toward important structures.

---

# Scenic composition

The scene must be designed as a landscape, not merely populated with objects.

Use:

- foreground
- midground
- background
- layered silhouettes
- visual corridors
- landmark visibility
- framing
- elevation
- color contrast

Create several intentionally composed viewpoints.

---

# Hero camera views

At least four deterministic camera presets must exist.

Required presets:

1. Isometric Hero View
2. Castle Front View
3. Castle Town Street View
4. Sakura Scenic View
5. Top View

You may add additional presets.

Each camera must use a deterministic position and target.

---

# Hero view quality

At least three camera presets should look strong enough to use as promotional screenshots.

They should not feel like arbitrary debug camera positions.

Use intentional composition.

For example:

Foreground:
- sakura
- roof edges
- lanterns
- fences

Midground:
- bridge
- gate
- street
- moat
- plaza

Background:
- castle keep
- secondary towers
- mountains or distant town silhouettes if created procedurally

The castle should remain visually dominant.

---

# Street-level composition

At least one camera view should be positioned near street level.

From this angle, the viewer should see:

- buildings on both sides
- street details
- lanterns or signs
- trees
- the castle visible in the distance

The view should create strong depth.

---

# Lighting

Implement a complete lighting setup.

Include:

- hemisphere or ambient lighting
- directional sunlight
- shadows

Important structures should cast and receive shadows where practical.

Lighting should enhance depth and architectural form.

Avoid flat lighting.

---

# Spring daylight mood

The default daylight should feel aesthetically pleasing.

Prefer:

- soft spring atmosphere
- slightly warm sunlight
- readable shadows
- pleasant blue sky
- strong but natural architectural contrast

Avoid harsh neutral lighting.

The scene should feel calm, beautiful, and cinematic.

---

# Day / night system

Implement a UI control to switch between day and night.

Day mode should use:

- bright spring sky
- warm natural sunlight
- visible architectural detail

Night mode should visibly change:

- sky
- ambient lighting
- directional lighting
- scene mood

Lanterns should appear illuminated at night.

The night scene should remain readable.

Use soft moonlit ambient light rather than making everything black.

---

# Twilight / cinematic atmosphere

If practical, also support a sunset, dusk, or twilight mode.

This mode should emphasize:

- warm directional light
- long shadows
- glowing lanterns
- pink cherry blossoms
- castle silhouette

This should be one of the most visually dramatic modes.

---

# Camera controls

Implement OrbitControls.

The user must be able to:

- rotate
- zoom
- pan

Choose an initial camera position that immediately presents the scene attractively.

Camera movement should feel comfortable at the scale of the environment.

---

# Object interaction

Implement mouse picking using raycasting.

When the user clicks a significant object, display information identifying its category.

At minimum distinguish:

- Castle Keep
- Gate
- Yagura
- House
- Merchant Building
- Shrine or Temple
- Market Stall
- Cherry Tree
- Tree
- Bridge

Visually highlight the selected object or associated structure.

---

# User interface

Create a modern, unobtrusive overlay UI.

Show:

- scene title
- day / night toggle
- optional twilight toggle
- camera preset buttons
- selected object information
- current FPS
- approximate object or mesh count

Do not let the interface obscure the castle.

The interface should visually match the quality of the scene.

---

# Visual quality rules

Avoid cheap-looking construction.

Do NOT rely heavily on:

- large primitive cubes
- simple pyramids
- flat facades
- identical repeated houses
- uniform stone grids
- huge empty spaces
- single-piece roofs
- one-cube trees

Repeated elements are allowed, but repetition should be hidden through controlled variation.

---

# Long-distance quality

From a distant camera:

- the castle silhouette should dominate
- multiple roof levels should be readable
- stone fortifications should create strong mass
- the moat should be visible
- the town should feel dense
- sakura clusters should create visible accents

---

# Medium-distance quality

From medium distance, the viewer should notice:

- roof variation
- courtyards
- walls
- roads
- different building types
- sakura-lined paths
- layered terrain
- markets
- bridges

---

# Close-distance quality

From close distance, additional detail should become visible:

- beams
- roof edges
- windows
- railings
- lanterns
- stone variation
- fences
- market props
- tree branches
- architectural joints

The environment should reward exploration.

---

# Visual impact

The scene should not merely satisfy the object checklist.

It should feel beautiful, memorable, and intentionally composed.

The target emotional impression is:

**a breathtaking Japanese castle town in spring, surrounded by layered fortifications, dense traditional streets, and abundant cherry blossoms.**

The environment should feel scenic enough that people would want to share screenshots of it.

Optimize equally for:

- correctness
- sophistication
- beauty
- atmosphere
- coherence
- depth
- visual impact

---

# Required autonomous workflow

Do not stop after producing the first working implementation.

Work in multiple stages.

## Stage 1 — Planning

Before implementation, internally reason about:

- world structure
- reusable building systems
- castle decomposition
- road layout
- terrain elevation
- procedural generation
- scene scale
- material reuse
- performance strategy
- camera composition

Then implement.

Do not spend the final response explaining the plan.

Use the planning to improve the actual result.

---

## Stage 2 — First complete implementation

Implement the full scene.

Do not create only a prototype.

All major requirements should exist before moving on.

---

## Stage 3 — Functional inspection

If execution or browser tools are available:

- launch the page
- inspect the rendered output
- inspect the browser console
- test camera controls
- test camera presets
- test day/night
- test object selection
- check performance

Fix functional problems.

---

## Stage 4 — Visual critique

Critically inspect the result.

Actively identify weaknesses such as:

- buildings that look too simple
- weak castle silhouette
- repetitive architecture
- flat-looking roofs
- large empty spaces
- poor sakura placement
- weak composition
- sparse vegetation
- uniform stonework
- unrealistic proportions
- insufficient height variation
- boring roads
- lack of foreground detail
- weak lighting
- poor scale relationships

Identify at least five meaningful weaknesses if they exist.

---

## Stage 5 — Improvement pass

Substantially improve the scene based on the critique.

Do not only make cosmetic changes.

Improve geometry, layout, density, lighting, composition, or architecture where appropriate.

---

## Stage 6 — Final polish

Perform another pass focused on:

- visual density
- architectural detail
- spring atmosphere
- sakura composition
- cinematic lighting
- camera framing
- environmental storytelling
- coherence
- overall beauty

Do not stop simply because all checklist items technically exist.

---

# Performance expectations

The scene should remain responsive.

Use intelligent optimization where useful:

- shared geometry
- shared materials
- InstancedMesh
- procedural reuse
- reasonable shadow settings
- sensible renderer settings

However:

Do not solve performance problems by removing most of the detail.

Balance visual richness and rendering efficiency intelligently.

---

# Final completion criteria

Before considering the task complete, verify that:

- the castle is large and detailed
- the castle contains five visible levels
- major roofs are layered and detailed
- the castle complex contains multiple yagura and courtyards
- stone fortifications are irregular and detailed
- elevation differences are significant
- the moat is visually integrated
- major bridges and gates are detailed
- the town contains approximately 100–160 buildings
- multiple building types exist
- streets and alleys form a believable network
- approximately 60–100 cherry blossom trees exist
- sakura placement is intentional
- green vegetation provides contrast
- a shrine or temple exists
- a market area exists
- environmental props are abundant
- camera controls work
- camera presets work
- at least three camera views are screenshot-worthy
- object picking works
- day/night works
- lighting produces depth
- the application contains no obvious runtime errors
- the final result looks substantially better than a minimal prototype

---

# Final instruction

Do not optimize only for correctness.

Do not optimize only for speed.

Do not stop at the first functional implementation.

Use your full reasoning ability to plan, build, inspect, criticize, refine, and polish the scene.

A scene that merely contains a castle, town, and trees is not sufficient.

The arrangement must feel deliberate, scenic, dramatic, detailed, and visually coherent.

The final result should look like a polished showcase-quality voxel environment rather than a technical demonstration.

The final deliverable is the working `index.html`.