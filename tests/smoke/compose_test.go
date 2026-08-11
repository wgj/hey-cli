package smoke_test

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	csrfMetaPattern        = regexp.MustCompile(`<meta[^>]+name=["']csrf-token["'][^>]+content=["']([^"']+)["']`)
	csrfMetaReversePattern = regexp.MustCompile(`<meta[^>]+content=["']([^"']+)["'][^>]+name=["']csrf-token["']`)
)

func TestCompose(t *testing.T) {
	uid := uniqueID()
	subject := fmt.Sprintf("Smoke test %s", uid)

	stdout, stderr, code := hey(t, "compose",
		"--to", "david@basecamp.com",
		"--subject", subject,
		"-m", "Hello from smoke test",
		"--json",
	)
	if code != 0 {
		t.Fatalf("compose failed (exit %d): %s", code, stderr)
	}

	var resp Response
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("failed to parse compose response: %v", err)
	}
	if !resp.OK {
		t.Fatal("compose returned ok=false")
	}
	assertContains(t, resp.Summary, "Message sent")

	// Cross-verify: fetch the thread page and check the subject appears.
	composeData := dataAs[map[string]any](t, resp)
	if appURL, ok := composeData["app_url"].(string); ok {
		topicID := extractTopicID(appURL)
		if topicID != "" {
			html := fetchHTML(t, fmt.Sprintf("%s/topics/%s", baseURL, topicID))
			assertContains(t, html, subject)
		}
	}
}

func TestComposeRequiresSubject(t *testing.T) {
	heyFail(t, "compose", "-m", "no subject", "--json")
}

func TestThreads(t *testing.T) {
	// Get a posting from imbox to use as thread ID.
	resp := heyJSON(t, "box", "imbox")
	type Posting struct {
		AppURL string `json:"app_url"`
		ID     int    `json:"id"`
	}
	type BoxResp struct {
		Postings []Posting `json:"postings"`
	}
	data := dataAs[BoxResp](t, resp)
	if len(data.Postings) == 0 {
		t.Fatal("no postings in imbox to test threads")
	}

	// The thread ID in the CLI is the topic ID, extracted from app_url.
	// app_url looks like "http://host/topics/12345", so we extract the topic ID.
	topicID := extractTopicID(data.Postings[0].AppURL)
	if topicID == "" {
		t.Fatalf("could not extract topic ID from app_url: %s", data.Postings[0].AppURL)
	}

	threadsResp := heyJSON(t, "threads", topicID)
	type Entry struct {
		ID      int    `json:"id"`
		Summary string `json:"summary"`
	}
	entries := dataAs[[]Entry](t, threadsResp)
	if len(entries) == 0 {
		t.Error("expected at least one entry in thread")
	}

	// Cross-verify: the thread content should exist on the topic page.
	html := fetchHTML(t, fmt.Sprintf("%s/topics/%s", baseURL, topicID))
	if len(entries) > 0 && entries[0].Summary != "" {
		assertContains(t, html, entries[0].Summary)
	}
}

func TestReply(t *testing.T) {
	// First try to compose a message to get a thread.
	uid := uniqueID()
	subject := fmt.Sprintf("Reply test %s", uid)
	_, _, composeCode := hey(t, "compose",
		"--to", "david@basecamp.com",
		"--subject", subject,
		"-m", "Original message for reply test",
		"--json",
	)

	// Find a thread in the imbox (use an existing one if compose failed).
	resp := heyJSON(t, "box", "imbox")
	type Posting struct {
		ID      int    `json:"id"`
		AppURL  string `json:"app_url"`
		Summary string `json:"summary"`
	}
	type BoxResp struct {
		Postings []Posting `json:"postings"`
	}
	data := dataAs[BoxResp](t, resp)

	var topicID string
	// First pass: find the thread we just composed.
	if composeCode == 0 {
		for _, p := range data.Postings {
			if p.Summary == subject {
				topicID = extractTopicID(p.AppURL)
				break
			}
		}
	}
	// Fallback: use any thread with a valid app_url.
	if topicID == "" {
		for _, p := range data.Postings {
			if p.AppURL != "" {
				topicID = extractTopicID(p.AppURL)
				break
			}
		}
	}
	if topicID == "" {
		t.Fatal("could not find a thread to reply to")
	}

	// Reply to it.
	stdout, stderr, code := hey(t, "reply", topicID,
		"-m", fmt.Sprintf("Reply from smoke test %s", uid),
		"--json",
	)
	if code != 0 {
		t.Fatalf("reply failed (exit %d): %s", code, stderr)
	}
	var replyResp Response
	if err := json.Unmarshal([]byte(stdout), &replyResp); err != nil {
		t.Fatalf("failed to parse reply response: %v", err)
	}
	assertContains(t, replyResp.Summary, "Reply sent")

	// Cross-verify: the reply should appear on the thread page.
	html := fetchHTML(t, fmt.Sprintf("%s/topics/%s", baseURL, topicID))
	assertContains(t, html, uid)
}

func TestDrafts(t *testing.T) {
	resp := heyJSON(t, "drafts")
	// Just verify the command succeeds and returns valid data.
	// The data is a list (possibly empty).
	if resp.Data == nil {
		// nil data is ok for empty drafts (returned as "null").
		return
	}
	type Draft struct {
		ID int `json:"id"`
	}
	_ = dataAs[[]Draft](t, resp)
}

func TestDraftCreateSavedAndNotSent(t *testing.T) {
	commands := []struct {
		name string
		args []string
	}{
		{name: "draft create", args: []string{"draft", "create"}},
		{name: "compose draft", args: []string{"compose", "--draft"}},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			assertDraftSavedAndNotSent(t, command.args)
		})
	}
}

func assertDraftSavedAndNotSent(t *testing.T, command []string) {
	t.Helper()

	uid := uniqueID()
	subject := fmt.Sprintf("Draft smoke %s", uid)
	body := fmt.Sprintf("Draft body %s", uid)

	args := append([]string(nil), command...)
	args = append(args,
		"--to", smokeEmail,
		"--subject", subject,
		"-m", body,
		"--json",
	)
	stdout, stderr, code := hey(t, args...)
	if code != 0 {
		t.Fatalf("%s failed (exit %d): %s", strings.Join(command, " "), code, stderr)
	}

	var createResp Response
	if err := json.Unmarshal([]byte(stdout), &createResp); err != nil {
		t.Fatalf("failed to parse %s response: %v", strings.Join(command, " "), err)
	}
	if !createResp.OK {
		t.Fatalf("%s returned ok=false", strings.Join(command, " "))
	}
	assertContains(t, createResp.Summary, "Draft created")
	assertNotContains(t, strings.ToLower(createResp.Summary), "sent")

	type Draft struct {
		ID      int64  `json:"id"`
		Subject string `json:"subject"`
		EditURL string `json:"edit_url"`
	}
	created := dataAs[Draft](t, createResp)
	if created.ID <= 0 {
		t.Fatalf("draft ID = %d, want a positive ID", created.ID)
	}
	t.Cleanup(func() { deleteDraftFixture(t, created.ID) })

	draftsResp := heyJSON(t, "drafts", "--all")
	drafts := dataAs[[]Draft](t, draftsResp)
	var saved *Draft
	for i := range drafts {
		if drafts[i].ID == created.ID {
			saved = &drafts[i]
			break
		}
	}
	if saved == nil {
		t.Fatalf("created draft %d was not returned by hey drafts", created.ID)
	}
	if saved.Subject != subject {
		t.Errorf("saved subject = %q, want %q", saved.Subject, subject)
	}

	editURL := created.EditURL
	if editURL == "" {
		editURL = saved.EditURL
	}
	if strings.HasPrefix(editURL, "/") {
		editURL = strings.TrimRight(baseURL, "/") + editURL
	}
	if editURL == "" {
		t.Fatal("created draft did not include an edit URL")
	}
	editHTML := fetchHTML(t, editURL)
	assertContains(t, editHTML, subject)
	assertContains(t, editHTML, uid)

	draftsPage := browserPageText(t, baseURL+"/entries/drafts")
	assertContains(t, draftsPage, subject)

	sentJSON := fetchHTML(t, baseURL+"/topics/sent.json")
	assertNotContains(t, sentJSON, subject)
	sentPage := browserPageText(t, baseURL+"/topics/sent")
	assertNotContains(t, sentPage, subject)
}

func deleteDraftFixture(t *testing.T, draftID int64) {
	t.Helper()

	editURL := fmt.Sprintf("%s/messages/%d/edit", strings.TrimRight(baseURL, "/"), draftID)
	editHTML := fetchHTML(t, editURL)
	token := extractCSRFToken(editHTML)
	if token == "" {
		t.Errorf("could not find a CSRF token while deleting draft %d", draftID)
		return
	}

	values := url.Values{}
	values.Set("_method", "delete")
	values.Set("status", "drafted")
	values.Set("authenticity_token", token)

	deleteURL := fmt.Sprintf("%s/messages/%d", strings.TrimRight(baseURL, "/"), draftID)
	req, err := http.NewRequest(http.MethodPost, deleteURL, strings.NewReader(values.Encode()))
	if err != nil {
		t.Errorf("could not create draft cleanup request: %v", err)
		return
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", editURL)
	req.Header.Set("X-CSRF-Token", token)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: sessionCookie})

	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Errorf("draft %d cleanup failed: %v", draftID, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		t.Errorf("draft %d cleanup returned HTTP %d", draftID, resp.StatusCode)
	}
}

func extractCSRFToken(page string) string {
	for _, pattern := range []*regexp.Regexp{csrfMetaPattern, csrfMetaReversePattern} {
		if match := pattern.FindStringSubmatch(page); len(match) == 2 {
			return html.UnescapeString(match[1])
		}
	}
	return ""
}

func TestDraftsLimit(t *testing.T) {
	resp := heyJSON(t, "drafts", "--limit", "2")
	if resp.Data == nil {
		return
	}
	type Draft struct {
		ID int `json:"id"`
	}
	drafts := dataAs[[]Draft](t, resp)
	if len(drafts) > 2 {
		t.Errorf("expected at most 2 drafts with --limit 2, got %d", len(drafts))
	}
}

func TestDraftsAll(t *testing.T) {
	resp := heyJSON(t, "drafts", "--all")
	// Just verify the command succeeds with --all.
	_ = resp
}

func TestThreadsNoArgument(t *testing.T) {
	heyFail(t, "threads", "--json")
}

func TestReplyNoArgument(t *testing.T) {
	heyFail(t, "reply", "--json")
}
