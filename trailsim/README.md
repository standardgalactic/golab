# trailsim

trailsim is an ant foraging simulation written in Go. A colony of ants leaves a nest, searches a walled map for food, carries it home, and lays pheromone that other ants follow. The engine runs on the Go side, a small local server streams its state, and a browser page draws it live and lets you paint walls and food while it runs. The module has no dependencies outside the standard library.

## What the simulation does

The map is a grid of cells. A vertical barrier splits it with a single gap, so the straight line from the nest to either food source is blocked, and two circular patches of food sit on the far side. Each ant is either searching or carrying. A searching ant wanders with a small random turn on every step, smells food within four cells, and otherwise steers toward the stronger of two probes of the food trail ahead of it to the left and right. A carrying ant steers toward the strongest home signal it can sense and aims straight for the nest once it is within twelve cells.

There are two pheromone fields. Searching ants write the home field, and they write it as freshness: each ant records the larger of what is already in a cell and its own strength, and that strength decays with every step since it left the nest, so the field forms a gradient that falls away from the nest and points back to it. Carrying ants write the food trail, and this one accumulates, so every ant that walks a route strengthens it. Both fields diffuse into open neighbouring cells and evaporate, and a trail that is no longer walked fades. The first design accumulated both fields, and it failed in a way worth knowing about: where ants linger, a field saturates, the gradient disappears, and carriers with food in hand circle without finding the nest. Writing the home field as freshness removed that failure.

## How much trails matter

The command `trailsim sweep` runs the same colony with trails and without them (the control, where ants neither lay nor follow pheromone) across several seeds. On the reference machine, with 300 ants over 3000 ticks, the colony delivered a mean of about 3000 units of food with trails against about 200 without, a ratio between 8 and 17 depending on the seed. The trailed colony is also bounded by the food available, not by its own speed: deliveries level off near 3240 because the nearer source has been emptied.

## Running it

The Go toolchain must be version 1.22 or later. From this directory:

    go run ./cmd/trailsim serve

then open http://127.0.0.1:8080/ in a browser. The server listens on the loopback interface by default, so it is reachable only from the same machine. Flags are `-addr` for the listen address, `-ants` and `-seed` for the starting colony, and `-fps` for the frame rate. Interrupting the process shuts the server down cleanly.

In the page, the first control bar pauses and resumes the run, sets how many simulation steps happen per frame, and chooses a brush. Dragging over the map with the wall brush builds barriers, the food brush drops new food, and the erase brush removes walls and food. Walls cannot be painted over the nest. The second bar resets the world with a chosen ant count and seed, and the trails checkbox turns pheromone off so that the control run can be watched directly. A good first experiment is to let a trail form, then wall off the gap and watch the colony lose the route and rebuild it elsewhere.

Two headless commands exist for scripting. `trailsim run -ticks 3000 -seed 1` prints the result of one run along with a fingerprint of the final state, and `trailsim sweep` prints the comparison table described above.

## Tests

    go test ./...
    go test -race ./...

The tests check that a given configuration and seed always produce the same state, that every unit of food is accounted for (the food at the start plus the food painted in equals what remains, what is carried, what is delivered, and what painting destroyed), that no ant ever stands in a wall or leaves the grid, that pheromone stays within bounds and never enters a wall, that diffusion conserves pheromone and evaporation decays it at the stated rate, and that the trailed colony outperforms the control by a wide margin. The runner tests cover pausing, speed clamping, painting through the command channel, and failed resets leaving the old world in place. The server tests exercise the page, the frame endpoint, the event stream, every command, malformed and oversized bodies, wrong methods, and graceful shutdown. One long test is skipped under `-race`, since it uses a single goroutine and runs about twenty-five times slower with the detector on.

## Layout

The package `internal/world` holds the simulation and knows nothing about HTTP or goroutines. The package `internal/runner` gives a world to a single goroutine that steps it at a fixed frame rate, applies commands such as pause, paint and reset one at a time, and publishes an immutable frame after every change, so the world is never shared. The package `internal/server` serves the embedded page, a server-sent event stream of frames, and the JSON commands. The command in `cmd/trailsim` ties these together.

## Tuning

The defaults live in `world.Default`. Raising evaporation makes trails short-lived and the colony more flexible when the map changes, while lowering it makes trails durable and the colony slower to abandon a route that has run dry. Raising diffusion widens trails and makes them easier to find from the side. The jitter controls how much each ant wanders, and too little of it makes the colony unable to discover new routes. The speed slider in the page changes only how many steps run per frame, never the results.
