package cmd

import (
	"fmt"
	"html"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/output"
)

type draftCommand struct {
	cmd *cobra.Command
}

func newDraftCommand() *draftCommand {
	draftCommand := &draftCommand{}
	draftCommand.cmd = &cobra.Command{
		Use:   "draft",
		Short: "Create saved drafts",
		Annotations: map[string]string{
			"agent_notes": "Creates saved email drafts without sending them. Use `hey drafts` to list saved drafts.",
		},
	}

	draftCommand.cmd.AddCommand(newDraftCreateCommand().cmd)

	return draftCommand
}

type draftCreateCommand struct {
	cmd     *cobra.Command
	to      string
	cc      string
	bcc     string
	subject string
	message string
}

type draftCreateResult struct {
	ID      int64  `json:"id"`
	Subject string `json:"subject"`
	URL     string `json:"url"`
	EditURL string `json:"edit_url"`
}

type draftCreateInput struct {
	to      string
	cc      string
	bcc     string
	subject string
	message string
}

func newDraftCreateCommand() *draftCreateCommand {
	draftCreateCommand := &draftCreateCommand{}
	draftCreateCommand.cmd = &cobra.Command{
		Use:   "create",
		Short: "Create a saved draft without sending",
		Annotations: map[string]string{
			"agent_notes": "Saves a new outbound email as a draft. This command never sends the message.",
		},
		Example: `  hey draft create --to alice@example.com --subject "Project update" -m "Here is the latest update."
  echo "Longer draft" | hey draft create --to bob@example.com --subject "Weekly report"`,
		Args: cobra.NoArgs,
		RunE: draftCreateCommand.run,
	}

	draftCreateCommand.cmd.Flags().StringVar(&draftCreateCommand.to, "to", "", "Recipient email address(es)")
	draftCreateCommand.cmd.Flags().StringVar(&draftCreateCommand.cc, "cc", "", "CC recipient email address(es)")
	draftCreateCommand.cmd.Flags().StringVar(&draftCreateCommand.bcc, "bcc", "", "BCC recipient email address(es)")
	draftCreateCommand.cmd.Flags().StringVar(&draftCreateCommand.subject, "subject", "", "Draft subject (required)")
	draftCreateCommand.cmd.Flags().StringVarP(&draftCreateCommand.message, "message", "m", "", "Draft body (or opens $EDITOR)")

	return draftCreateCommand
}

func (c *draftCreateCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	return saveDraft(cmd, "hey draft create", draftCreateInput{
		to:      c.to,
		cc:      c.cc,
		bcc:     c.bcc,
		subject: c.subject,
		message: c.message,
	})
}

func saveDraft(cmd *cobra.Command, invocation string, input draftCreateInput) error {
	if idsOnly || countFlag {
		return output.ErrUsage("--ids-only and --count are not supported by " + invocation)
	}

	if strings.TrimSpace(input.subject) == "" {
		return output.ErrUsageHint("--subject is required", invocation+" --to <email> --subject <subject> -m <message>")
	}

	message, err := input.readMessage()
	if err != nil {
		return err
	}

	draft, err := sdk.Messages().CreateDraft(
		cmd.Context(),
		input.subject,
		draftContentHTML(message),
		parseAddresses(input.to),
		parseAddresses(input.cc),
		parseAddresses(input.bcc),
	)
	if err != nil {
		return convertSDKError(err)
	}
	if draft == nil || draft.Id <= 0 || draft.EditUrl == "" {
		return output.ErrAPI(0, "HEY did not return a verifiable saved draft")
	}
	result := draftCreateResult{
		ID:      draft.Id,
		Subject: draft.Subject,
		URL:     draft.Url,
		EditURL: draft.EditUrl,
	}

	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Draft created: %d\n", result.ID)
		fmt.Fprintf(cmd.OutOrStdout(), "Edit: %s\n", result.EditURL)
		return nil
	}

	return writeOK(result,
		output.WithSummary("Draft created"),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "list",
			Command:     "hey drafts",
			Description: "List saved drafts",
		}),
	)
}

func (i draftCreateInput) readMessage() (string, error) {
	if i.message != "" {
		return i.message, nil
	}

	if !stdinIsTerminal() {
		message, err := readStdin()
		if err != nil {
			return "", err
		}
		if message == "" {
			return "", output.ErrUsage("no draft body provided (use -m or --message to provide inline, or pipe to stdin)")
		}
		return message, nil
	}

	message, err := editor.Open("")
	if err != nil {
		return "", output.ErrAPI(0, fmt.Sprintf("could not open editor: %v", err))
	}
	if strings.TrimSpace(message) == "" {
		return "", output.ErrUsage("empty draft body, aborting")
	}
	return message, nil
}

func draftContentHTML(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if line == "" {
			lines[i] = "<div><br></div>"
			continue
		}
		lines[i] = "<div>" + html.EscapeString(line) + "</div>"
	}
	return strings.Join(lines, "")
}
