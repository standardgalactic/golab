# Run guide

Check the toolchain with `go version`; it should report 1.22 or later. Unpack the archive, enter the `trailsim` directory, and run `go test ./...` to confirm everything passes on your machine. Then start the server with `go run ./cmd/trailsim serve` and open http://127.0.0.1:8080/ in a browser. Within a second or two the ants should begin to stream out of the nest, find the gap in the wall, and settle into a trail to the food.

If port 8080 is taken, choose another with `-addr 127.0.0.1:9000`. If the page loads but nothing moves, the status in the corner of the control bar will read "reconnecting" and the server's terminal will show the reason. For a standalone binary, `go build -o trailsim ./cmd/trailsim` produces one file with the page embedded, and `./trailsim sweep` prints the trails against control comparison without a browser.

Stop the server with Ctrl+C.
