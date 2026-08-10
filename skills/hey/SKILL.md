---
name: hey
description: |
  Interact with HEY email via the HEY CLI. Read and send emails, manage boxes,
  calendars and daily schedules, todos, habits, time tracking, and journal
  entries. Use for ANY HEY-related question or action.
triggers:
  # Direct invocations
  - hey
  - /hey
  # Email actions
  - hey boxes
  - hey box
  - hey threads
  - hey reply
  - hey compose
  - hey drafts
  # Calendar actions
  - hey calendars
  - hey recordings
  - hey day-view
  # Todos
  - hey todo
  # Seen/unseen
  - hey seen
  - hey unseen
  - mark as read
  - mark as seen
  - mark as unseen
  - mark as unread
  # Habits
  - hey habit
  # Time tracking
  - hey timetrack
  # Journal
  - hey journal
  # Auth
  - hey auth
  # Common actions
  - check my email
  - read email
  - send email
  - reply to email
  - compose email
  - list mailboxes
  - check calendar
  - check my schedule
  - what's my schedule today
  - what is my schedule today
  - what's on my calendar today
  - add todo
  - complete todo
  - track time
  - write journal
  # Questions
  - can I hey
  - how do I hey
  - what's in hey
  - what hey
  - does hey
  # My work
  - my emails
  - my inbox
  - my imbox
  - my todos
  - my calendar
  - my schedule
  - my journal
  # URLs
  - hey.com
invocable: true
argument-hint: "[command] [args...]"
---

# /hey - HEY Email Workflow Command

CLI for HEY email: mailboxes, email threads, replies, compose, calendars, todos, habits, time tracking, and journal entries.

## Agent Invariants

**MUST follow these rules:**

1. **Always use `--json`** for structured, predictable output
2. **Authentication required** for all data commands — run `hey auth login` first
3. **HTML output** is available via `--html` for commands that return HTML content
4. **Use `hey day-view --json` for daily schedule questions** — `hey recordings` is not a complete Day View

## Quick Reference

| Task | Command |
|------|---------|
| List mailboxes | `hey boxes --json` |
| List emails in a box | `hey box imbox --json` |
| Read email thread | `hey threads <topic_id> --json` |
| Reply to email | `hey reply <topic_id> -m "Thanks!"` |
| Compose email | `hey compose --to user@example.com --subject "Hello"` |
| Compose with CC/BCC | `hey compose --to alice@example.com --cc bob@example.com --bcc carol@example.org --subject "Hello"` |
| List drafts | `hey drafts --json` |
| List calendars | `hey calendars --json` |
| List source recordings | `hey recordings 123 --json` |
| Show today's schedule | `hey day-view --json` |
| List todos | `hey todo list --json` |
| Add todo | `hey todo add "Buy milk"` |
| Complete todo | `hey todo complete 123` |
| Uncomplete todo | `hey todo uncomplete 123` |
| Delete todo | `hey todo delete 123` |
| Mark as seen | `hey seen 12345` |
| Mark as unseen | `hey unseen 12345` |
| Complete habit | `hey habit complete 123` |
| Uncomplete habit | `hey habit uncomplete 123` |
| Start time tracking | `hey timetrack start` |
| Stop time tracking | `hey timetrack stop` |
| Current timer | `hey timetrack current --json` |
| List time entries | `hey timetrack list --json` |
| List journal entries | `hey journal list --json` |
| Read journal entry | `hey journal read 2024-03-15 --json` |
| Write journal entry | `hey journal write "Today was great"` |
| Check auth status | `hey auth status` |
| Print access token | `hey auth token` |
| Launch TUI | `hey` |

## Decision Trees

### Reading Email

```
Want to read email?
├── Which mailbox? → hey boxes --json
├── List emails in box? → hey box <name|id> --json
├── Read full thread? → hey threads <topic_id> --json
├── Mark as seen? → hey seen <posting-id>
├── Mark as unseen? → hey unseen <posting-id>
└── Launch interactive UI? → hey (no args, launches TUI)
```

### Sending Email

```
Want to send email?
├── Reply to thread? → hey reply <topic_id> -m "message"
│   └── Open editor? → hey reply <topic_id> (omit -m to open $EDITOR)
├── Compose new? → hey compose --to <email> --subject "Subject"
│   ├── With body? → hey compose --to <email> --subject "Subject" -m "Body"
│   ├── With CC? → add --cc <email>
│   └── With BCC? → add --bcc <email>
└── Check drafts? → hey drafts --json
```

### Reading the Calendar

```
Want to read the calendar?
├── Today's schedule? → hey day-view --json
├── Another day's schedule? → hey day-view YYYY-MM-DD --json
├── Only selected calendars? → add --calendar <id> for each calendar
├── List available calendars? → hey calendars --json
└── Inspect source recordings for one calendar? → hey recordings <calendar-id> --json
```

### Managing Todos

```
Want to manage todos?
├── List todos? → hey todo list --json
├── Add todo? → hey todo add "Task description"
├── Complete? → hey todo complete <id>
├── Uncomplete? → hey todo uncomplete <id>
└── Delete? → hey todo delete <id>
```

## Resource Reference

### Email - Boxes

```bash
hey boxes --json                              # List all mailboxes
hey box imbox --json                          # List emails in Imbox (by name)
hey box 123 --json                            # List emails in box (by ID)
```

Box names: `imbox`, `feedbox`, `trailbox`, `asidebox`, `laterbox`, `bubblebox`

**Response format:** `hey box` returns `{"box": {...}, "postings": [...]}`. Each posting has: `id` (posting ID), `topic_id` (topic ID), `name` (subject), `seen` (read status), `created_at`, `contacts`, `summary`, `app_url`. Use `topic_id` for `hey threads` and `hey reply`.

### Email - Threads

```bash
hey threads <topic_id> --json                 # Read full email thread
hey threads <topic_id> --html                 # Read with raw HTML content
```

**ID note:** `hey box` returns postings with an `id` (posting ID) and a `topic_id` (topic ID). `hey threads` and `hey reply` expect the **topic ID** — use `topic_id` directly. The `app_url` field also contains the topic ID as a fallback (e.g. `https://app.hey.com/topics/123` → `123`).

### Email - Reply & Compose

```bash
hey reply <topic_id> -m "Thanks!"             # Reply with inline message
hey reply <topic_id>                          # Reply via $EDITOR
hey compose --to user@example.com --subject "Hello"         # Compose new (opens $EDITOR)
hey compose --to user@example.com --subject "Hi" -m "Body"  # With inline body
hey compose --to alice@example.com --cc bob@example.com --bcc carol@example.org --subject "Project update" -m "Body"  # With CC/BCC
hey compose --subject "Update" --thread-id 12345 -m "msg"   # Post to existing thread
```

### Email - Seen/Unseen

```bash
hey seen 12345                                # Mark posting as seen
hey seen 12345 67890                          # Mark multiple postings as seen
hey unseen 12345                              # Mark posting as unseen
hey unseen 12345 67890                        # Mark multiple postings as unseen
```

Takes posting IDs (the `id` field from `hey box` output).

### Drafts

```bash
hey drafts --json                             # List drafts
```

### Calendars

```bash
hey calendars --json                          # List calendars (returns array of {id, name, kind})
hey recordings 123 --json                     # List events in calendar
hey day-view --json                           # Show today's schedule from all calendars
hey day-view 2026-01-15 --calendar 123 --calendar 456 --json  # Use only these calendars
```

**Response format:** `hey recordings` returns recordings grouped by type (e.g. `{"Calendar::Event": [...], "Calendar::Habit": [...], "Calendar::Todo": [...]}`). Each recording has: `id`, `title`, `starts_at`, `ends_at`, `all_day`, `recurring`, `starts_at_time_zone`. Access by type key in jq, e.g. `.["Calendar::Event"]`.

Use `hey day-view [YYYY-MM-DD] --json` to answer daily schedule questions. If you omit the date, it uses today in the HEY account time zone. If you omit `--calendar`, it queries all calendars. Repeat `--calendar <id>` to use only selected calendars.

`day-view` merges source recordings with realized and unrealized occurrences. It filters all sources to the requested day. It removes duplicates by event ID or occurrence ID, never by title and time. It returns only timed, all-day, overnight, and multi-day `Calendar::Event` records. It excludes HEY's Nighttime display block and all non-event records. JSON keeps `starts_at` and `ends_at` in UTC. The response metadata includes `date` and `time_zone`.

The command fails if any required recordings or occurrences request fails for a selected calendar. Treat an error as an unknown schedule. Do not report a partial result as complete. Use `recordings` only when you need the source records for one calendar.

### Todos

```bash
hey todo list --json                          # List all todos
hey todo add "Task description"                        # Add a todo
hey todo complete 123                         # Mark complete
hey todo uncomplete 123                       # Mark incomplete
hey todo delete 123                           # Delete a todo
```

### Habits

```bash
hey habit complete 123                        # Mark habit complete for today
hey habit complete 123 --date 2024-01-15      # Mark complete for specific date
hey habit uncomplete 123                      # Unmark habit for today
```

Habit IDs can be found via `hey recordings <calendar-id> --json`.

### Time Tracking

```bash
hey timetrack start                           # Start timer
hey timetrack stop                            # Stop timer
hey timetrack current --json                  # Show current timer
hey timetrack list --json                     # List time entries
```

### Journal

```bash
hey journal list --json                       # List journal entries
hey journal read 2024-03-15 --json            # Read entry by date
hey journal write "Today's entry"                     # Write entry inline
hey journal write                             # Write entry via $EDITOR
```

### Authentication

```bash
hey auth login                                # Log in (browser-based OAuth)
hey auth status                               # Check if authenticated
hey auth logout                               # Log out
```

If a command fails with an auth error, run `hey auth status` to check, then `hey auth login` to re-authenticate.
