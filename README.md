# GoLab

[Go as an Instrument of Continuation](https://standardgalactic.github.io/golab/go-textbook.pdf)

[The Hotel Question](https://standardgalactic.github.io/golab/the-hotel-question.pdf)

**GoLab** accompanies *Go as an Instrument of System Truth*, a textbook and experimental Go codebase exploring how program structure can make system behavior observable, constrained, and testable.

The project brings together a LaTeX textbook, executable examples, a Go laboratory application, and an audio overview with captions. The examples and tests are intended to make architectural claims inspectable rather than merely descriptive.

## Explore the laboratory

The Go module lives in [`go-instrument/golab/`](go-instrument/golab/). It includes a command entry point, chapter examples, and packages for artifacts, atomic files, indexing, ledgers, manifests, pipelines, pools, runners, services, summaries, and transitions.

With Go installed:

```sh
cd go-instrument/golab
go test ./...
go run ./cmd/golab
```

The last command starts the application according to its current implementation; consult [`go-instrument/README.md`](go-instrument/README.md) and the command source for configuration and usage.

## Repository layout

```text
golab/
├── index.html                     # Static publication page
├── go-textbook.pdf                # Compiled textbook
├── How_Go_Architecture_Enforces_System_Truth.*  # Audio and transcripts
└── go-instrument/
    ├── book/                      # LaTeX source and chapters
    └── golab/                     # Go module, tests, examples, deployment files
```

## License

The repository is distributed under the [MIT License](LICENSE). This covers the original material in this repository to the extent the copyright holder has rights to license it; third-party works retain their own terms.

