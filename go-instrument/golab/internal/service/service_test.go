package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golab/internal/ledger"
	"golab/internal/pipeline"
)

type fixture struct {
	t    *testing.T
	ts   *httptest.Server
	w    *ledger.Writer
	path string
}

func newFixture(t *testing.T, p pipeline.Policy) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	w := ledger.NewWriter(path, 16)
	tok, err := ParseTokens("alice=wsecret:rw,bob=rsecret:r")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(w, p, tok))
	t.Cleanup(func() { ts.Close(); w.Close() })
	return &fixture{t, ts, w, path}
}

func (f *fixture) do(method, path, token, body string) (int, string, http.Header) {
	f.t.Helper()
	req, err := http.NewRequest(method, f.ts.URL+path, strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func (f *fixture) ledgerLen() int {
	f.t.Helper()
	recs, err := f.w.Snapshot()
	if err != nil {
		f.t.Fatal(err)
	}
	return len(recs)
}

const goodBatch = `[{"id":"a","author":"ann","scope":"lab/x","body":"first"},
 {"id":"b","author":"bo","scope":"lab/x","body":"second","refs":["a"]}]`

func TestRouterDecidesFromMethodAndPathOnly(t *testing.T) {
	rt := NewRouter([]Route{
		{http.MethodGet, "/a", NeedRead, nil},
		{http.MethodPost, "/a", NeedWrite, nil},
		{http.MethodGet, "/b", NeedRead, nil},
	})
	cases := []struct {
		method, path string
		found        bool
		status       int
		allow        string
	}{
		{"GET", "/a", true, 200, ""},
		{"POST", "/a", true, 200, ""},
		{"DELETE", "/a", false, 405, "GET, POST"},
		{"POST", "/b", false, 405, "GET"},
		{"GET", "/nope", false, 404, ""},
		{"GET", "/a/", false, 404, ""},
	}
	for _, c := range cases {
		r, status, allow := rt.Match(c.method, c.path)
		if (r != nil) != c.found || status != c.status || allowHeader(allow) != c.allow {
			t.Errorf("%s %s: found=%v status=%d allow=%q", c.method, c.path, r != nil, status, allowHeader(allow))
		}
	}
}

func TestPermitTable(t *testing.T) {
	reader := Principal{Name: "r"}
	writer := Principal{Name: "w", CanWrite: true}
	cases := []struct {
		p      Principal
		authed bool
		n      Need
		status int
	}{
		{Principal{}, false, NeedNothing, 200},
		{Principal{}, false, NeedRead, 401},
		{Principal{}, false, NeedWrite, 401},
		{reader, true, NeedRead, 200},
		{reader, true, NeedWrite, 403},
		{writer, true, NeedWrite, 200},
		{writer, true, Need(99), 403},
	}
	for i, c := range cases {
		if status, _ := Permit(c.p, c.authed, c.n); status != c.status {
			t.Errorf("case %d: status %d, want %d", i, status, c.status)
		}
	}
}

func TestAuthenticationAndAuthorization(t *testing.T) {
	f := newFixture(t, pipeline.AllOf())
	if code, _, _ := f.do("GET", "/healthz", "", ""); code != 200 {
		t.Fatalf("healthz: %d", code)
	}
	code, _, h := f.do("GET", "/v1/ledger/head", "", "")
	if code != 401 || h.Get("WWW-Authenticate") == "" {
		t.Fatalf("no token: %d %v", code, h)
	}
	if code, _, _ := f.do("GET", "/v1/ledger/head", "wrong", ""); code != 401 {
		t.Fatalf("wrong token: %d", code)
	}
	if code, _, _ := f.do("GET", "/v1/ledger/head", "rsecret", ""); code != 200 {
		t.Fatalf("reader read: %d", code)
	}
	// A reader may not write, and the refusal happens before the handler
	// can touch the ledger.
	if code, _, _ := f.do("POST", "/v1/admit", "rsecret", goodBatch); code != 403 {
		t.Fatalf("reader write: %d", code)
	}
	if code, _, _ := f.do("POST", "/v1/admit", "", goodBatch); code != 401 {
		t.Fatalf("anonymous write: %d", code)
	}
	if n := f.ledgerLen(); n != 0 {
		t.Fatalf("denied requests wrote %d ledger records", n)
	}
}

func TestRoutingErrorsDoNotDependOnCredentials(t *testing.T) {
	f := newFixture(t, pipeline.AllOf())
	for _, tok := range []string{"", "wrong", "rsecret", "wsecret"} {
		if code, _, _ := f.do("GET", "/v1/nothing", tok, ""); code != 404 {
			t.Errorf("token %q: unknown path gave %d", tok, code)
		}
		code, _, h := f.do("DELETE", "/v1/admit", tok, "")
		if code != 405 || h.Get("Allow") != "POST" {
			t.Errorf("token %q: wrong method gave %d allow=%q", tok, code, h.Get("Allow"))
		}
	}
}

func TestAdmitRecordsOutcomeAndPrincipal(t *testing.T) {
	f := newFixture(t, pipeline.AllOf(pipeline.DenyAuthors("bo")))
	code, body, _ := f.do("POST", "/v1/admit", "wsecret", goodBatch)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var resp struct {
		Seq       int `json:"ledger_seq"`
		Judgments []judgment
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, j := range resp.Judgments {
		got[j.ID] = j.Disposition
	}
	if got["a"] != "admitted" || got["b"] != "refused" {
		t.Fatalf("dispositions: %v", got)
	}
	recs, _ := f.w.Snapshot()
	if len(recs) != 1 || recs[0].Action != "admit" || recs[0].Detail["principal"] != "alice" || resp.Seq != 1 {
		t.Fatalf("ledger: %+v", recs)
	}
}

func TestAdmitRejectsBadInputs(t *testing.T) {
	f := newFixture(t, pipeline.AllOf())
	cases := []struct {
		name, body string
		code       int
		ledger     int
	}{
		{"malformed json", `[{`, 400, 0},
		{"unknown field", `[{"id":"a","author":"x","scope":"s","body":"b","extra":1}]`, 400, 0},
		{"dangling ref", `[{"id":"a","author":"x","scope":"s","body":"b","refs":["zz"]}]`, 422, 1},
		{"too large", `[{"id":"a","author":"x","scope":"s","body":"` + strings.Repeat("x", MaxBody) + `"}]`, 413, 1},
	}
	for _, c := range cases {
		code, _, _ := f.do("POST", "/v1/admit", "wsecret", c.body)
		if code != c.code || f.ledgerLen() != c.ledger {
			t.Errorf("%s: code %d (want %d), ledger %d (want %d)", c.name, code, c.code, f.ledgerLen(), c.ledger)
		}
	}
	recs, _ := f.w.Snapshot()
	if recs[0].Action != "admit-failed" {
		t.Errorf("failed validation recorded as %q", recs[0].Action)
	}
}

func TestReadEndpoints(t *testing.T) {
	f := newFixture(t, pipeline.AllOf())
	for i := 0; i < 5; i++ {
		f.w.Append("verify-file", fmt.Sprintf("p%d", i), map[string]string{"sha256": fmt.Sprintf("d%d", i)})
	}
	_, body, _ := f.do("GET", "/v1/ledger/head", "rsecret", "")
	var head struct {
		Records int
		Head    string
	}
	json.Unmarshal([]byte(body), &head)
	recs, _ := f.w.Snapshot()
	if head.Records != 5 || head.Head != recs[4].Digest {
		t.Fatalf("head: %s", body)
	}

	_, body, _ = f.do("GET", "/v1/ledger?after=2&limit=2", "rsecret", "")
	var list struct{ Records []ledger.Record }
	json.Unmarshal([]byte(body), &list)
	if len(list.Records) != 2 || list.Records[0].Seq != 3 {
		t.Fatalf("list: %s", body)
	}
	for _, q := range []string{"after=-1", "limit=0", "limit=5000", "after=x"} {
		if code, _, _ := f.do("GET", "/v1/ledger?"+q, "rsecret", ""); code != 400 {
			t.Errorf("%s: %d", q, code)
		}
	}

	code, body, _ := f.do("GET", "/v1/index?path=p3", "rsecret", "")
	if code != 200 || !strings.Contains(body, `"last_digest":"d3"`) {
		t.Fatalf("index: %d %s", code, body)
	}
	if code, _, _ := f.do("GET", "/v1/index?path=absent", "rsecret", ""); code != 404 {
		t.Fatalf("absent path: %d", code)
	}
	if code, _, _ := f.do("GET", "/v1/index", "rsecret", ""); code != 400 {
		t.Fatalf("missing path: %d", code)
	}
}

// Many clients writing at once, while others read, leave one valid chain
// with a record for every accepted request.
func TestConcurrentClientsShareOneLedgerSafely(t *testing.T) {
	f := newFixture(t, pipeline.AllOf())
	const writers, readers = 40, 10
	var wg sync.WaitGroup
	errs := make(chan string, writers+readers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`[{"id":"r%d","author":"a","scope":"s","body":"b"}]`, i)
			if code, _, _ := f.do("POST", "/v1/admit", "wsecret", body); code != 200 {
				errs <- fmt.Sprintf("write %d: %d", i, code)
			}
		}(i)
	}
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if code, _, _ := f.do("GET", "/v1/ledger/head", "rsecret", ""); code != 200 {
					errs <- fmt.Sprintf("read: %d", code)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	recs, err := ledger.Load(f.path)
	if err != nil || len(recs) != writers {
		t.Fatalf("len=%d err=%v", len(recs), err)
	}
	if err := ledger.Check(recs); err != nil {
		t.Fatal(err)
	}
}

func TestParseTokens(t *testing.T) {
	for _, bad := range []string{"", "x", "a=b", "a=b:x", "=t:r", "a=:r", "a=t:r,b=t:rw"} {
		if _, err := ParseTokens(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	tk, err := ParseTokens("a=t1:r, b=t2:rw")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", "/", bytes.NewReader(nil))
	req.Header.Set("Authorization", "Bearer t2")
	if p, ok := tk.Authenticate(req); !ok || p.Name != "b" || !p.CanWrite {
		t.Fatalf("%+v %v", p, ok)
	}
	req.Header.Set("Authorization", "bearer t2")
	if _, ok := tk.Authenticate(req); ok {
		t.Fatal("scheme matched case-insensitively")
	}
}

func TestEveryRequestIsLoggedAndNoCredentialIs(t *testing.T) {
	var buf bytes.Buffer
	path := filepath.Join(t.TempDir(), "l.jsonl")
	w := ledger.NewWriter(path, 4)
	defer w.Close()
	tok, _ := ParseTokens("alice=wsecret:rw")
	srv := New(w, pipeline.AllOf(), tok)
	srv.Logger = slog.New(slog.NewJSONHandler(&buf, nil))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	f := &fixture{t: t, ts: ts, w: w, path: path}
	f.do("GET", "/healthz", "", "")
	f.do("GET", "/v1/ledger/head", "guess-1234567890", "")
	f.do("POST", "/v1/admit", "wsecret", goodBatch)
	f.do("GET", "/nowhere", "", "")

	type entry struct {
		Level     string
		Method    string
		Path      string
		Status    int
		Principal string
	}
	var got []entry
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var e entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		got = append(got, e)
	}
	want := []entry{
		{"INFO", "GET", "/healthz", 200, ""},
		{"WARN", "GET", "/v1/ledger/head", 401, ""},
		{"INFO", "POST", "/v1/admit", 200, "alice"},
		{"WARN", "GET", "/nowhere", 404, ""},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d:\n%s", len(got), len(want), buf.String())
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	for _, secret := range []string{"wsecret", "guess-1234567890", "Bearer"} {
		if strings.Contains(buf.String(), secret) {
			t.Errorf("log contains %q", secret)
		}
	}
}

// When the ledger cannot be written, the client is told the request failed,
// is not told why, and the cause appears in the log.
func TestLedgerFailureIsReportedWithoutLeakingDetails(t *testing.T) {
	var buf bytes.Buffer
	bad := filepath.Join(t.TempDir(), "no-such-directory", "ledger.jsonl")
	w := ledger.NewWriter(bad, 4)
	defer w.Close()
	tok, _ := ParseTokens("alice=wsecret:rw")
	srv := New(w, pipeline.AllOf(), tok)
	srv.Logger = slog.New(slog.NewJSONHandler(&buf, nil))
	ts := httptest.NewServer(srv)
	defer ts.Close()
	f := &fixture{t: t, ts: ts, w: w, path: bad}

	code, body, _ := f.do("POST", "/v1/admit", "wsecret", goodBatch)
	if code != 500 || !strings.Contains(body, `"internal error"`) {
		t.Fatalf("%d %s", code, body)
	}
	if strings.Contains(body, "no-such-directory") {
		t.Fatalf("response leaks a path: %s", body)
	}
	if !strings.Contains(buf.String(), "no-such-directory") || !strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Fatalf("cause missing from the log: %s", buf.String())
	}
}
