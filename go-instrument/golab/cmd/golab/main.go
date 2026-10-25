// Command golab is the laboratory tool developed across the book. It
// discovers files, verifies their content, runs external programs, and
// records each action in a hash-chained ledger. Part IV adds an index that is
// derived from the ledger and can always be rebuilt.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"golab/internal/artifact"
	"golab/internal/atomicfile"
	"golab/internal/index"
	"golab/internal/ledger"
	"golab/internal/manifest"
	"golab/internal/pipeline"
	"golab/internal/pool"
	"golab/internal/runner"
	"golab/internal/service"
	"golab/internal/summary"
)

const usage = `usage: golab [-ledger file] <command> [arguments]

commands:
  discover <dir>            list files and their kinds
  verify [flags] <dir>      hash every file and record the result
  run <dir> <program> ...   run a program in dir and record the outcome
  admit [flags] <file>      run a file of records through the pipeline
  audit                     check that the ledger chain holds
  index [flags]             write the index derived from the ledger
  status [flags] <dir>      compare files with what the ledger last recorded
  serve [flags]             serve the ledger and admission over HTTP
  manifest [flags] [dir]    record or check what an experiment ran on
  experiment compress [-n]  search for what a summary of the ledger loses

flags for verify:
  -workers n     files to hash at once (default: number of CPUs)
  -each          also write one ledger record per file
  -timeout d     stop starting new files after d

flags for index and status:
  -index file    index file (default golab-index.json)
  -rebuild       (index) ignore any existing index and rebuild it
  -workers n     (status) files to hash at once (default: number of CPUs)

flags for manifest:
  -name s        experiment name (default "experiment")
  -params k=v,.. experiment parameters
  -out file      write the manifest to file and record it in the ledger
  -check file    compare a saved manifest with the current state; exit 1 if different

flags for serve (tokens come from the environment, as GOLAB_TOKENS=name=token:rw,...):
  -addr a        address to listen on (default 127.0.0.1:8080)
  -deny, -scope, -gate   the policy applied to submitted records, as for admit
  -log format    request log on standard error: json, text, or off (default json)

flags for admit:
  -deny a,b      refuse records written by these authors
  -scope prefix  mark records outside this scope prefix as unscoped
  -gate phrase   hold back records whose body contains the phrase
  -project s     project and activate the records visible in scope s
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is separate from main so that it can be tested and so that main has
// exactly one place where the process exit status is decided.
func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("golab", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ledgerPath := fs.String("ledger", "golab-ledger.jsonl", "path of the ledger file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	var err error
	switch rest[0] {
	case "discover":
		err = cmdDiscover(rest[1:], stdout)
	case "verify":
		err = cmdVerify(*ledgerPath, rest[1:], stdout)
	case "run":
		err = cmdRun(*ledgerPath, rest[1:], stdout)
	case "admit":
		err = cmdAdmit(*ledgerPath, rest[1:], stdout)
	case "audit":
		err = cmdAudit(*ledgerPath, stdout)
	case "index":
		err = cmdIndex(*ledgerPath, rest[1:], stdout)
	case "status":
		err = cmdStatus(*ledgerPath, rest[1:], stdout)
	case "serve":
		err = cmdServe(*ledgerPath, rest[1:], stdout)
	case "manifest":
		err = cmdManifest(*ledgerPath, rest[1:], stdout)
	case "experiment":
		err = cmdExperiment(rest[1:], stdout)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", rest[0], usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "golab:", err)
		return 1
	}
	return 0
}

func cmdDiscover(args []string, out *os.File) error {
	if len(args) != 1 {
		return fmt.Errorf("discover needs exactly one directory")
	}
	found, err := artifact.Discover(args[0])
	if err != nil {
		return err
	}
	for _, d := range found {
		fmt.Fprintf(out, "%-8s %8d  %s\n", d.Kind, d.Size, d.Path)
	}
	return nil
}

func cmdVerify(ledgerPath string, args []string, out *os.File) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	workers := fs.Int("workers", runtime.NumCPU(), "number of files to hash at once")
	each := fs.Bool("each", false, "also write one ledger record per file")
	timeout := fs.Duration("timeout", 0, "stop starting new files after this long (0 means no limit)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("verify needs exactly one directory")
	}
	dir := fs.Arg(0)

	found, err := artifact.Discover(dir)
	if err != nil {
		return err
	}

	ctx := context.Background()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	w := ledger.NewWriter(ledgerPath, 64)
	defer w.Close()

	opts := pool.Options{Workers: *workers}
	var eachErr atomic.Pointer[error]
	if *each {
		opts.OnResult = func(o pool.Outcome) {
			detail := map[string]string{"kind": string(o.File.Kind)}
			action := "verify-file"
			if o.Err != nil {
				action = "verify-file-failed"
				detail["error"] = o.Err.Error()
			} else {
				detail["sha256"] = o.Verified.SHA256()
			}
			if _, err := w.Append(action, o.File.Path, detail); err != nil {
				eachErr.CompareAndSwap(nil, &err)
			}
		}
	}
	outcomes := pool.VerifyAll(ctx, found, opts)

	verified := 0
	var failures []string
	for _, o := range outcomes {
		if o.Err != nil {
			failures = append(failures, o.Err.Error())
			continue
		}
		verified++
		fmt.Fprintf(out, "%s  %s\n", o.Verified.SHA256(), o.Verified.Path())
	}
	detail := map[string]string{
		"found":    strconv.Itoa(len(found)),
		"verified": strconv.Itoa(verified),
		"failed":   strconv.Itoa(len(failures)),
		"workers":  strconv.Itoa(*workers),
	}
	if _, err := w.Append("verify", dir, detail); err != nil {
		return err
	}
	if p := eachErr.Load(); p != nil {
		return *p
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d files could not be verified: %s", len(failures), strings.Join(failures, "; "))
	}
	return nil
}

func cmdRun(ledgerPath string, args []string, out *os.File) error {
	if len(args) < 2 {
		return fmt.Errorf("run needs a directory and a program")
	}
	res, runErr := runner.Run(args[0], time.Minute, args[1], args[2:]...)
	detail := map[string]string{
		"outcome": string(res.Outcome),
		"exit":    strconv.Itoa(res.ExitCode),
		"elapsed": res.Elapsed.Round(time.Millisecond).String(),
	}
	subject := strings.Join(args[1:], " ")
	if _, err := ledger.Append(ledgerPath, "run", subject, detail, time.Now()); err != nil {
		return err
	}
	if runErr != nil {
		return runErr
	}
	fmt.Fprint(out, res.Stdout)
	fmt.Fprintf(out, "[%s, exit %d, %s]\n", res.Outcome, res.ExitCode, res.Elapsed.Round(time.Millisecond))
	if res.Outcome != runner.Succeeded {
		return fmt.Errorf("program %s", res.Outcome)
	}
	return nil
}

func cmdAudit(ledgerPath string, out *os.File) error {
	records, err := ledger.Load(ledgerPath)
	if err != nil {
		return err
	}
	if err := ledger.Check(records); err != nil {
		return err
	}
	fmt.Fprintf(out, "ledger holds: %d records\n", len(records))
	return nil
}

func cmdAdmit(ledgerPath string, args []string, out *os.File) error {
	fs := flag.NewFlagSet("admit", flag.ContinueOnError)
	deny := fs.String("deny", "", "comma-separated authors to refuse")
	scope := fs.String("scope", "", "required scope prefix")
	gate := fs.String("gate", "", "phrase that sends a record to review")
	project := fs.String("project", "", "scope to project and activate")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("admit needs exactly one records file")
	}
	path := fs.Arg(0)

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	records, err := pipeline.LoadJSONL(f)
	f.Close()
	if err != nil {
		return err
	}

	inscribed := make([]pipeline.Inscribed, len(records))
	for i, r := range records {
		inscribed[i] = pipeline.Inscribe(r)
	}
	valid, err := pipeline.Validate(inscribed)
	if err != nil {
		// A structural failure is recorded too: the attempt happened.
		detail := map[string]string{"stage": "validate", "error": err.Error()}
		if _, lerr := ledger.Append(ledgerPath, "admit-failed", path, detail, time.Now()); lerr != nil {
			return lerr
		}
		return err
	}

	var policies []pipeline.Policy
	if *deny != "" {
		policies = append(policies, pipeline.DenyAuthors(strings.Split(*deny, ",")...))
	}
	if *scope != "" {
		policies = append(policies, pipeline.RequireScope(*scope))
	}
	if *gate != "" {
		policies = append(policies, pipeline.GateContaining(*gate))
	}
	admitted := pipeline.Admit(valid, pipeline.AllOf(policies...))

	for _, j := range admitted.Judgments() {
		fmt.Fprintf(out, "%-12s %s  %s\n", j.Disposition, j.Record.ID, j.Receipt[:12])
	}

	counts := admitted.Counts()
	detail := map[string]string{"records": strconv.Itoa(valid.Len())}
	names := make([]string, 0, len(counts))
	for d := range counts {
		names = append(names, string(d))
	}
	sort.Strings(names)
	for _, n := range names {
		detail[n] = strconv.Itoa(counts[pipeline.Disposition(n)])
	}

	if *project != "" {
		active, err := pipeline.Activate(pipeline.Project(admitted, *project))
		if err != nil {
			detail["activation"] = err.Error()
			if _, lerr := ledger.Append(ledgerPath, "admit", path, detail, time.Now()); lerr != nil {
				return lerr
			}
			return err
		}
		detail["active"] = strconv.Itoa(len(active.Records()))
		fmt.Fprintf(out, "active in %q: %d records\n", active.Scope(), len(active.Records()))
	}
	_, err = ledger.Append(ledgerPath, "admit", path, detail, time.Now())
	return err
}

// openIndex loads the ledger, checks its chain, and returns an index that
// agrees with it, reporting how the index was obtained.
func openIndex(ledgerPath, indexPath string, rebuild bool) (*index.Index, index.Source, string, error) {
	records, err := ledger.Load(ledgerPath)
	if err != nil {
		return nil, "", "", err
	}
	if rebuild {
		x, err := index.Build(records)
		return x, index.Rebuilt, "requested", err
	}
	return index.Open(indexPath, records)
}

func cmdIndex(ledgerPath string, args []string, out *os.File) error {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	indexPath := fs.String("index", "golab-index.json", "path of the index file")
	rebuild := fs.Bool("rebuild", false, "ignore any existing index and rebuild it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("index takes no arguments")
	}
	x, src, reason, err := openIndex(ledgerPath, *indexPath, *rebuild)
	if err != nil {
		return err
	}
	if err := x.Save(*indexPath); err != nil {
		return err
	}
	if reason != "" {
		reason = " (" + reason + ")"
	}
	fmt.Fprintf(out, "index %s%s: %d paths from %d ledger records\n", src, reason, x.Len(), x.Records())
	return nil
}

// cmdStatus hashes the files under dir and compares each with the index.
// It reads the ledger and the files and writes nothing.
func cmdStatus(ledgerPath string, args []string, out *os.File) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	indexPath := fs.String("index", "golab-index.json", "path of the index file")
	workers := fs.Int("workers", runtime.NumCPU(), "number of files to hash at once")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("status needs exactly one directory")
	}
	dir := fs.Arg(0)
	x, _, _, err := openIndex(ledgerPath, *indexPath, false)
	if err != nil {
		return err
	}
	found, err := artifact.Discover(dir)
	if err != nil {
		return err
	}
	outcomes := pool.VerifyAll(context.Background(), found, pool.Options{Workers: *workers})

	type line struct{ state, path string }
	var lines []line
	seen := map[string]bool{}
	for _, o := range outcomes {
		p := o.File.Path
		seen[p] = true
		e, known := x.Lookup(p)
		switch {
		case o.Err != nil:
			lines = append(lines, line{"unreadable", p})
		case !known:
			lines = append(lines, line{"new", p})
		case e.LastDigest == "":
			lines = append(lines, line{"never-verified", p})
		case e.LastDigest != o.Verified.SHA256():
			lines = append(lines, line{"changed", p})
		case !e.Verified:
			lines = append(lines, line{"last-attempt-failed", p})
		default:
			lines = append(lines, line{"unchanged", p})
		}
	}
	prefix := filepath.Clean(dir) + string(filepath.Separator)
	for _, e := range x.Entries() {
		if strings.HasPrefix(e.Path, prefix) && !seen[e.Path] {
			lines = append(lines, line{"missing", e.Path})
		}
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].path < lines[j].path })
	differ := 0
	for _, l := range lines {
		fmt.Fprintf(out, "%-19s %s\n", l.state, l.path)
		if l.state != "unchanged" {
			differ++
		}
	}
	if differ > 0 {
		return fmt.Errorf("%d of %d paths differ from the ledger", differ, len(lines))
	}
	return nil
}

// buildPolicy assembles the policy named by the flags shared by admit and
// serve. With no flags set the combination is empty and admits everything.
func buildPolicy(deny, scope, gate string) pipeline.Policy {
	var policies []pipeline.Policy
	if deny != "" {
		policies = append(policies, pipeline.DenyAuthors(strings.Split(deny, ",")...))
	}
	if scope != "" {
		policies = append(policies, pipeline.RequireScope(scope))
	}
	if gate != "" {
		policies = append(policies, pipeline.GateContaining(gate))
	}
	return pipeline.AllOf(policies...)
}

// cmdServe runs the HTTP service until it receives SIGINT or SIGTERM, then
// stops accepting connections, lets requests in progress finish, and closes
// the ledger writer so that every accepted append is on disk.
func cmdServe(ledgerPath string, args []string, out *os.File) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "address to listen on")
	deny := fs.String("deny", "", "comma-separated authors to refuse")
	scope := fs.String("scope", "", "required scope prefix")
	gate := fs.String("gate", "", "phrase that sends a record to review")
	logFormat := fs.String("log", "json", "request log format: json, text, or off")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("serve takes no arguments")
	}
	var logger *slog.Logger
	switch *logFormat {
	case "json":
		logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	case "text":
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	case "off":
	default:
		return fmt.Errorf("-log must be json, text, or off")
	}
	tokens, err := service.ParseTokens(os.Getenv("GOLAB_TOKENS"))
	if err != nil {
		return fmt.Errorf("GOLAB_TOKENS: %w", err)
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	w := ledger.NewWriter(ledgerPath, 64)
	svc := service.New(w, buildPolicy(*deny, *scope, *gate), tokens)
	if logger != nil {
		svc.Logger = logger
	}
	srv := &http.Server{
		Handler:           svc,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	fmt.Fprintf(out, "listening on %s\n", ln.Addr())

	select {
	case err := <-serveErr:
		w.Close()
		return err
	case <-ctx.Done():
	}
	fmt.Fprintln(out, "shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	w.Close()
	if err == nil {
		fmt.Fprintln(out, "stopped")
	}
	return err
}

func parseParams(s string) (map[string]string, error) {
	m := map[string]string{}
	for _, item := range strings.Split(s, ",") {
		if item == "" {
			continue
		}
		k, v, ok := strings.Cut(item, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("parameter %q: want key=value", item)
		}
		m[k] = v
	}
	return m, nil
}

// cmdManifest collects the manifest of an experiment. With -out it writes the
// manifest atomically and records its digest in the ledger. With -check it
// compares a saved manifest with the present state and lists the differences.
func cmdManifest(ledgerPath string, args []string, out *os.File) error {
	fs := flag.NewFlagSet("manifest", flag.ContinueOnError)
	name := fs.String("name", "experiment", "experiment name")
	params := fs.String("params", "", "comma-separated key=value parameters")
	outPath := fs.String("out", "", "write the manifest here and record it in the ledger")
	check := fs.String("check", "", "compare this saved manifest with the present state")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("manifest takes at most one directory")
	}
	dir := fs.Arg(0)

	if *check != "" {
		b, err := os.ReadFile(*check)
		if err != nil {
			return err
		}
		saved, err := manifest.Decode(b)
		if err != nil {
			return err
		}
		now, err := manifest.Collect(saved.Name, saved.Params, dir)
		if err != nil {
			return err
		}
		diffs := manifest.Differences(saved, now)
		for _, d := range diffs {
			fmt.Fprintln(out, d)
		}
		if len(diffs) > 0 {
			return fmt.Errorf("%d differences from the saved manifest", len(diffs))
		}
		fmt.Fprintln(out, "manifest matches")
		return nil
	}

	p, err := parseParams(*params)
	if err != nil {
		return err
	}
	m, err := manifest.Collect(*name, p, dir)
	if err != nil {
		return err
	}
	b, err := m.Encode()
	if err != nil {
		return err
	}
	digest, err := m.Digest()
	if err != nil {
		return err
	}
	if *outPath == "" {
		fmt.Fprintf(out, "%s\n", b)
		return nil
	}
	if err := atomicfile.WriteBytes(*outPath, 0o644, append(b, '\n')); err != nil {
		return err
	}
	detail := map[string]string{"digest": digest, "inputs": strconv.Itoa(len(m.Inputs)), "file": *outPath}
	if _, err := ledger.Append(ledgerPath, "manifest", m.Name, detail, time.Now()); err != nil {
		return err
	}
	fmt.Fprintf(out, "manifest %s: %d inputs, digest %s\n", m.Name, len(m.Inputs), digest)
	return nil
}

func cmdExperiment(args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "compress" {
		return fmt.Errorf("experiment: the only experiment is compress")
	}
	fs := flag.NewFlagSet("experiment compress", flag.ContinueOnError)
	n := fs.Int("n", 6, "longest ledger to examine (the search visits 7^n ledgers)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *n < 0 || *n > 7 {
		return fmt.Errorf("-n must be from 0 to 7")
	}
	res, err := summary.Run(*n)
	if err != nil {
		return err
	}
	fmt.Fprint(out, res.Table())
	return nil
}
