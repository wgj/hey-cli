package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
)

type draftEndpointRecorder struct {
	mu              sync.Mutex
	form            url.Values
	draftPosts      int
	sendPosts       int
	allowSend       bool
	failDraftPost   bool
	omitLocation    bool
	unexpectedPaths []string
}

func (r *draftEndpointRecorder) handler(w http.ResponseWriter, req *http.Request) {
	switch {
	case req.Method == http.MethodGet && (req.URL.Path == "/identity" || req.URL.Path == "/identity.json"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"email_address":"user@hey.com","id":1,"senders":[{"id":42,"default":true}],"primary_contact":{"id":42}}`)
	case req.Method == http.MethodGet && req.URL.Path == "/messages/new":
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<html><head><meta name="csrf-token" content="draft-csrf-token"></head></html>`)
	case req.Method == http.MethodPost && req.URL.Path == "/messages":
		_ = req.ParseForm()
		r.mu.Lock()
		r.draftPosts++
		r.form = req.PostForm
		fail := r.failDraftPost
		omitLocation := r.omitLocation
		r.mu.Unlock()
		if fail {
			http.Error(w, "draft persistence failed", http.StatusUnprocessableEntity)
			return
		}
		if !omitLocation {
			w.Header().Set("Location", "/messages/987654321")
		}
		w.WriteHeader(http.StatusNoContent)
	case req.Method == http.MethodPost && req.URL.Path == "/messages.json":
		r.mu.Lock()
		r.sendPosts++
		allowSend := r.allowSend
		r.mu.Unlock()
		if allowSend {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "send endpoint must not be called", http.StatusInternalServerError)
	default:
		r.mu.Lock()
		r.unexpectedPaths = append(r.unexpectedPaths, req.Method+" "+req.URL.Path)
		r.mu.Unlock()
		http.NotFound(w, req)
	}
}

func (r *draftEndpointRecorder) snapshot() (url.Values, int, int, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	form := make(url.Values, len(r.form))
	for key, values := range r.form {
		form[key] = append([]string(nil), values...)
	}
	return form, r.draftPosts, r.sendPosts, append([]string(nil), r.unexpectedPaths...)
}

func runDraftCommand(t *testing.T, recorder *draftEndpointRecorder, token string, command []string, args ...string) (output.Response, error) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(recorder.handler))
	t.Cleanup(server.Close)

	oldJSONFlag, oldQuietFlag := jsonFlag, quietFlag
	oldHTMLOutput, oldVerboseFlag := htmlOutput, verboseFlag
	oldIDsOnly, oldCountFlag := idsOnly, countFlag
	oldMarkdownF, oldStyledFlag := markdownF, styledFlag
	oldAgentFlag, oldStatsFlag := agentFlag, statsFlag
	oldBaseURL, oldCfg := baseURL, cfg
	oldAuthMgr, oldWriter := authMgr, writer
	oldSDK, oldSDKStats := sdk, sdkStats
	t.Cleanup(func() {
		jsonFlag, quietFlag = oldJSONFlag, oldQuietFlag
		htmlOutput, verboseFlag = oldHTMLOutput, oldVerboseFlag
		idsOnly, countFlag = oldIDsOnly, oldCountFlag
		markdownF, styledFlag = oldMarkdownF, oldStyledFlag
		agentFlag, statsFlag = oldAgentFlag, oldStatsFlag
		baseURL, cfg = oldBaseURL, oldCfg
		authMgr, writer = oldAuthMgr, oldWriter
		sdk, sdkStats = oldSDK, oldSDKStats
	})

	jsonFlag, quietFlag = false, false
	htmlOutput, verboseFlag = false, 0
	idsOnly, countFlag = false, false
	markdownF, styledFlag = false, false
	agentFlag, statsFlag = false, false
	baseURL, cfg = "", nil
	authMgr, writer = nil, nil
	sdk, sdkStats = nil, nil

	t.Setenv("HEY_TOKEN", token)
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	rootArgs := append([]string(nil), command...)
	rootArgs = append(rootArgs, "--json", "--base-url", server.URL)
	root.SetArgs(append(rootArgs, args...))

	err := root.Execute()
	var resp output.Response
	if buf.Len() > 0 {
		_ = json.Unmarshal(buf.Bytes(), &resp)
	}
	return resp, err
}

func runDraftCreate(t *testing.T, recorder *draftEndpointRecorder, token string, args ...string) (output.Response, error) {
	t.Helper()
	return runDraftCommand(t, recorder, token, []string{"draft", "create"}, args...)
}

func runComposeDraft(t *testing.T, recorder *draftEndpointRecorder, token string, args ...string) (output.Response, error) {
	t.Helper()
	return runDraftCommand(t, recorder, token, []string{"compose", "--draft"}, args...)
}

func assertDraftOnlyMutation(t *testing.T, recorder *draftEndpointRecorder) url.Values {
	t.Helper()

	form, draftPosts, sendPosts, unexpected := recorder.snapshot()
	if draftPosts != 1 {
		t.Errorf("POST /messages calls = %d, want 1", draftPosts)
	}
	if sendPosts != 0 {
		t.Errorf("POST /messages.json calls = %d, want 0", sendPosts)
	}
	if len(unexpected) != 0 {
		t.Errorf("unexpected requests: %v", unexpected)
	}
	if got := form.Get("entry[status]"); got != "drafted" {
		t.Errorf("entry[status] = %q, want drafted", got)
	}
	if got := form.Get("commit"); got != "" {
		t.Errorf("commit = %q, want no send commit", got)
	}
	return form
}

func TestDraftCreateSavesWithoutSending(t *testing.T) {
	recorder := &draftEndpointRecorder{}
	resp, err := runDraftCreate(t, recorder, "test-token",
		"--to", "alice@example.com,bob@example.org",
		"--cc", "carol@example.com",
		"--bcc", "dave@example.org",
		"--subject", "Project update",
		"-m", "First line\n\nDetails: <ready>",
	)
	if err != nil {
		t.Fatalf("draft create: %v", err)
	}

	if resp.Summary != "Draft created" {
		t.Fatalf("summary = %q, want %q", resp.Summary, "Draft created")
	}
	if strings.Contains(strings.ToLower(resp.Summary), "sent") {
		t.Fatalf("summary must not claim the draft was sent: %q", resp.Summary)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", resp.Data)
	}
	if data["id"] != float64(987654321) {
		t.Errorf("id = %#v, want 987654321", data["id"])
	}
	if !strings.HasSuffix(fmt.Sprint(data["edit_url"]), "/messages/987654321/edit") {
		t.Errorf("edit_url = %#v, want message edit URL", data["edit_url"])
	}
	if got, want := len(data), 4; got != want {
		t.Errorf("response fields = %v, want only id, subject, url, and edit_url", data)
	}

	form := assertDraftOnlyMutation(t, recorder)
	if got := form.Get("acting_sender_id"); got != "42" {
		t.Errorf("acting_sender_id = %q, want 42", got)
	}
	if got := form.Get("message[subject]"); got != "Project update" {
		t.Errorf("message[subject] = %q", got)
	}
	if got := form.Get("message[content]"); got != "<div>First line</div><div><br></div><div>Details: &lt;ready&gt;</div>" {
		t.Errorf("message[content] = %q", got)
	}
	if got := form["entry[addressed][directly][]"]; strings.Join(got, ",") != "alice@example.com,bob@example.org" {
		t.Errorf("direct recipients = %v", got)
	}
	if got := form["entry[addressed][copied][]"]; strings.Join(got, ",") != "carol@example.com" {
		t.Errorf("copied recipients = %v", got)
	}
	if got := form["entry[addressed][blindcopied][]"]; strings.Join(got, ",") != "dave@example.org" {
		t.Errorf("blind-copied recipients = %v", got)
	}
	if got := form.Get("authenticity_token"); got != "draft-csrf-token" {
		t.Errorf("authenticity_token got %q, want draft-csrf-token", got)
	}
}

func TestComposeDraftSavesWithoutSending(t *testing.T) {
	recorder := &draftEndpointRecorder{}
	resp, err := runComposeDraft(t, recorder, "test-token",
		"--to", "alice@example.com",
		"--subject", "Project update",
		"-m", "Draft body",
	)
	if err != nil {
		t.Fatalf("compose --draft: %v", err)
	}
	if resp.Summary != "Draft created" {
		t.Fatalf("summary = %q, want %q", resp.Summary, "Draft created")
	}
	if strings.Contains(strings.ToLower(resp.Summary), "sent") {
		t.Fatalf("summary must not claim the draft was sent: %q", resp.Summary)
	}

	form := assertDraftOnlyMutation(t, recorder)
	if got := form.Get("message[subject]"); got != "Project update" {
		t.Errorf("message[subject] = %q", got)
	}
	if got := form.Get("message[content]"); got != "<div>Draft body</div>" {
		t.Errorf("message[content] = %q", got)
	}
	if got := form["entry[addressed][directly][]"]; strings.Join(got, ",") != "alice@example.com" {
		t.Errorf("direct recipients = %v", got)
	}
}

func TestComposeDraftRejectsThreadIDBeforeMutation(t *testing.T) {
	recorder := &draftEndpointRecorder{}
	_, err := runComposeDraft(t, recorder, "test-token",
		"--thread-id", "12345",
		"--subject", "Project update",
		"-m", "Draft body",
	)
	if err == nil {
		t.Fatal("expected --draft and --thread-id conflict")
	}
	if !strings.Contains(err.Error(), "--draft cannot be combined with --thread-id") {
		t.Fatalf("error = %q, want flag conflict", err)
	}
	_, draftPosts, sendPosts, unexpected := recorder.snapshot()
	if draftPosts != 0 || sendPosts != 0 || len(unexpected) != 0 {
		t.Fatalf("requests = draft:%d send:%d unexpected:%v, want zero", draftPosts, sendPosts, unexpected)
	}
}

func TestComposeWithoutDraftStillSends(t *testing.T) {
	recorder := &draftEndpointRecorder{allowSend: true}
	resp, err := runDraftCommand(
		t,
		recorder,
		"test-token",
		[]string{"compose"},
		"--to", "alice@example.com",
		"--subject", "Project update",
		"-m", "Message body",
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if resp.Summary != "Message sent" {
		t.Fatalf("summary = %q, want %q", resp.Summary, "Message sent")
	}
	_, draftPosts, sendPosts, unexpected := recorder.snapshot()
	if draftPosts != 0 || sendPosts != 1 || len(unexpected) != 0 {
		t.Fatalf("requests = draft:%d send:%d unexpected:%v, want draft:0 send:1", draftPosts, sendPosts, unexpected)
	}
}

func TestDraftCreateRequiresSubjectBeforeMutation(t *testing.T) {
	recorder := &draftEndpointRecorder{}
	_, err := runDraftCreate(t, recorder, "test-token", "-m", "Draft body")
	if err == nil {
		t.Fatal("expected a missing-subject error")
	}
	if !strings.Contains(err.Error(), "--subject is required") {
		t.Fatalf("error = %q, want subject requirement", err)
	}
	_, draftPosts, sendPosts, _ := recorder.snapshot()
	if draftPosts != 0 || sendPosts != 0 {
		t.Fatalf("mutation calls = draft:%d send:%d, want zero", draftPosts, sendPosts)
	}
}

func TestDraftCreateReadsBodyFromStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		_ = r.Close()
		os.Stdin = oldStdin
	})

	go func() {
		_, _ = w.Write([]byte("Draft body from stdin\n"))
		_ = w.Close()
	}()

	recorder := &draftEndpointRecorder{}
	_, err = runDraftCreate(t, recorder, "test-token", "--subject", "Stdin draft")
	if err != nil {
		t.Fatalf("draft create from stdin: %v", err)
	}
	form, _, _, _ := recorder.snapshot()
	if got := form.Get("message[content]"); got != "<div>Draft body from stdin</div>" {
		t.Errorf("message[content] = %q", got)
	}
}

func TestDraftCreateRequiresAuthentication(t *testing.T) {
	recorder := &draftEndpointRecorder{}
	_, err := runDraftCreate(t, recorder, "", "--subject", "Private draft", "-m", "Draft body")
	if err == nil {
		t.Fatal("expected authentication error")
	}
	_, draftPosts, sendPosts, _ := recorder.snapshot()
	if draftPosts != 0 || sendPosts != 0 {
		t.Fatalf("mutation calls = draft:%d send:%d, want zero", draftPosts, sendPosts)
	}
}

func TestDraftCreateRejectsListOutputModesBeforeMutation(t *testing.T) {
	for _, flag := range []string{"--ids-only", "--count"} {
		t.Run(flag, func(t *testing.T) {
			recorder := &draftEndpointRecorder{}
			_, err := runDraftCreate(t, recorder, "test-token", flag, "--subject", "Private draft", "-m", "Draft body")
			if err == nil {
				t.Fatalf("expected %s error", flag)
			}
			_, draftPosts, sendPosts, _ := recorder.snapshot()
			if draftPosts != 0 || sendPosts != 0 {
				t.Fatalf("mutation calls = draft:%d send:%d, want zero", draftPosts, sendPosts)
			}
		})
	}
}

func TestDraftCreateReportsPersistenceFailure(t *testing.T) {
	recorder := &draftEndpointRecorder{failDraftPost: true}
	resp, err := runDraftCreate(t, recorder, "test-token", "--subject", "Failed draft", "-m", "Draft body")
	if err == nil {
		t.Fatal("expected draft persistence error")
	}
	if resp.Summary != "" {
		t.Fatalf("summary = %q, want no success summary", resp.Summary)
	}
	_, draftPosts, sendPosts, _ := recorder.snapshot()
	if draftPosts != 1 || sendPosts != 0 {
		t.Fatalf("mutation calls = draft:%d send:%d, want draft:1 send:0", draftPosts, sendPosts)
	}
}

func TestDraftCreateRejectsUnverifiablePersistence(t *testing.T) {
	recorder := &draftEndpointRecorder{omitLocation: true}
	_, err := runDraftCreate(t, recorder, "test-token", "--subject", "Missing location", "-m", "Draft body")
	if err == nil {
		t.Fatal("expected unverifiable-persistence error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "location") && !strings.Contains(strings.ToLower(err.Error()), "verifiable") {
		t.Fatalf("error = %q, want missing persistence location", err)
	}
}

func TestDraftContentHTML(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "single line", in: "Hello", want: "<div>Hello</div>"},
		{name: "blank line", in: "Hello\n\nWorld", want: "<div>Hello</div><div><br></div><div>World</div>"},
		{name: "CRLF", in: "Hello\r\nWorld", want: "<div>Hello</div><div>World</div>"},
		{name: "escaped HTML", in: `<script>alert("x")</script>`, want: "<div>&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;</div>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := draftContentHTML(tt.in); got != tt.want {
				t.Errorf("draftContentHTML(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
