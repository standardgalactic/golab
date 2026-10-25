# Go as an Instrument of Continuation

This directory holds a LaTeX textbook and the Go module it builds. The book is in `book/`, and `golab/` is the laboratory tool that the chapters develop one stage at a time.

All six parts are written: Local Computation, Governed Workflows, Concurrent Execution, Memory and Reconstruction, Distributed Systems, and Experimental Systems.

To run the tests, enter `golab/` and run `go test ./...`. To build the book, enter `book/` and run `latexmk -pdf main.tex`. Every code listing in the book is included directly from the files under `golab/`, and the session transcript in Chapter 6 is `book/session.txt`, captured from a real run. Nothing here has been committed to a repository, so the changes are ready for review and committing.
