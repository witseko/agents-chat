# agentcy

MCP conversations and project memory for agents working in different desktop apps and CLIs.

This document describes the planned first release. Start a task in a familiar chat, invite existing conversations or launch fresh agents, and collect the answer in the initiating chat. The same task can produce verified additions to the project's Markdown knowledge base while work is underway.

## Example workflow

1. In Codex Desktop, ask an open Claude chat and a fresh GLM agent to review the project's retry behavior. Codex creates one conversation with the brief and two participant slots.
2. Give the Claude chat its join code once. It retrieves the brief and can use its own history to answer. The core dispatches the GLM assignment to the Z.ai plugin, which runs OpenCode with the configured model and connects it to the core as a participant.
3. Each agent submits its proposal. When the first round closes, the core releases the proposals for one critique round. Codex presents the recommendation, supporting evidence, disagreements, and missing responses.
4. If the review verifies a retry rule, a participant can propose adding it to the worker notes. The user approves the change from Codex, the core writes it to the Markdown note, and a future chat following the project index can find the rule and its evidence without reading the review transcript.

## Non-goals

The first release does not include multi-user access, a hosted or shared core, remote access other than the user's own remote chat through the [HTTP transport](#http-transport), a graphical interface for the human, cloud synchronization, committing to Git, MCP file uploads, running the waiting read as an MCP task, or fresh runs that edit the workspace. The human participates through a chat or the terminal UI.

## Core and plugin interface

Use the Model Context Protocol (MCP) for both public interfaces. A desktop app or CLI acts as an MCP client of the core. The core acts as an MCP client of each configured provider plugin. The core coordinates conversations and memory. Each plugin owns the details of its provider. [MCP architecture](https://modelcontextprotocol.io/specification/2026-07-28/architecture)

The shim serves MCP revision 2026-07-28 and also the older revisions that the SDK supports, on stdio and on the HTTP transport. A host on an older revision keeps the full tool interface. A provider plugin may use either revision.

| Area | Responsibility |
| --- | --- |
| `protocol/` | Versioned, language-neutral contracts for the chat-facing tools, the plugin interface, and the operator protocol: JSON schemas, example exchanges, and compatibility fixtures. |
| `cmd/agentcy/` | The one `agentcy` binary: subcommand dispatch for the stdio shim (`--workspace`, `--http`), the core daemon (`agentcy core`), the fresh worker, and `agentcy ui`, which starts the terminal UI. |
| `internal/core/` | The daemon: Unix socket server, request dispatch, room wakes, waiting reads, idle exit, and the operator key. |
| `internal/conversation/` | Participants, invitations, messages, assignments, handoffs, rounds, tasks, history, search, and operator actions, with their own queries. |
| `internal/memory/` | Planned: scoped Markdown discovery, evidence, proposals, publication, and conflict handling, with its own tables and queries. |
| `internal/store/` | SQLite access, the embedded schema, the token ledger database, and file permissions. |
| `internal/fresh/` | Plugin settings, the fresh-run contract, the client used to call plugins, and the assignment worker. |
| `internal/shim/` | Tools exposed to chats over MCP on stdio and on the HTTP transport, guidance, skills, MCP tasks, and confirmation. |
| `internal/wire/` | Member and operator requests, their validation, size caps, and the line codec that shim, core, and tests share. |
| `internal/tokens/`, `internal/progress/`, `internal/paths/` | Token estimates, balance, and ledger; progress rules over status; data directory checks, workspace keys, and absolute `PATH` lookup. |
| `tui/` | Terminal UI for the human, written in Go, on the operator protocol. |
| `conformance/`, `bench/` | A black-box conformance suite for any core daemon, and the benchmark driver. |
| `tests/` | Cross-process end-to-end tests that drive any shim and core through the Python MCP SDK, and their fixture plugin. |

### Process model

Chats launch local MCP servers over stdio and stop them when the chat closes, while assignments, deadlines, and publications must outlive any single chat. Run one long-lived core per user. Each chat launches a small stdio shim that forwards to the core over a Unix socket in private local application data with owner-only permissions. The shim and the core are the same static `agentcy` binary. The shim starts the core as `agentcy core` when it is not running, using a lock file so concurrent chats start one instance. Waiting reads sleep on a conversation condition in the core and release the database while they wait. A shim sends a waiting read and the status that follows it as one `listen` request, so each wait costs one socket exchange. A stateful shim also opens a passive `session` stream to the running core without spawning a daemon, so the core can track live `connected` presence and answer on-demand MCP `ping` probes without defeating idle exit. The core exits when no conversation is open and no assignment or publication is pending. Measure the cold start from the first tool call.

### HTTP transport

A chat that runs in a provider's VM or hosted sandbox cannot launch the stdio shim on the user's computer. For such a chat the user runs `agentcy --workspace DIR --http PORT`. This process serves the same tools over MCP Streamable HTTP at `/mcp` and forwards to the same core, so the workspace key comes from the directory on the user's computer and join codes work across local and remote chats.

The process listens on the loopback interface only. The user exposes it through a tunnel with TLS and passes the tunnel's host name with `--http-public-host`. The process rejects every other Host header. Every request must carry `Authorization: Bearer` with the access token from `AGENTCY_HTTP_TOKEN`, a secret of at least 32 characters. The process compares it in constant time, answers 401 without it, never logs it, and removes it from its environment before it starts the core. The access token admits a caller to the tools of one workspace, which include starting fresh runs. Participant tokens keep their function inside a conversation.

### Participant identity

The join call exchanges a join code for a participant token. Every later call carries the token, and the core derives the author, conversation, and workspace from it. Tool arguments never name the author. Generate join codes and tokens with a cryptographic random source. A join code is bound to one invited slot, works once, and expires after one hour. A second attempt returns an error that names the slot as taken.

New join codes carry a per-code tag that detects a workspace mismatch before the core looks up the invitation in the configured workspace. The core authenticates the invitation by its full code digest.

The human acts through the operator protocol and holds no participant token. At each start the core writes a new random operator key to `operator.key` in the bridge data directory with owner-only permissions. Every operator request carries the key, and the core compares it in constant time. A missing or wrong key gets one generic error. The core never logs the key. Fresh runs cannot read the file: their sandbox denies reads inside the bridge data directory except the socket and their own scratch space.

### Conversation roles

The creator leads by default. Each plain-name invitation creates a worker slot. An invitation can also specify a name and the role `co-lead`. When any co-lead is invited, the creator and invited co-leads share leadership. The join code binds its holder to the assigned role. The create and join tools and a one-time read from cursor zero return the roster. Any member can call `conversation_status` to see who was invited, who joined, and who replied. Status shows time since the last recorded authenticated bridge call when known, and in open conversations reports `listening` (active waiting read) and `connected` (open shim session or waiting read). A lead or co-lead can call `ping_participant` to check a teammate's live connection and wake an active waiting read out of band without posting a message. Leads coordinate work and summarize findings through messages. All members can read and post. Any lead or co-lead can close the conversation.

A conversation created through the operator protocol has no creator token. Its lead slot is an invitation like every other slot, and an interactive chat takes it with its join prompt. The human is the member `Human` with role `human`. It has no token and no invitation, does not count toward the 17 participants, and cannot own a task. No invitation can use its name. Names that look like `Human` or `You` are rejected, and names cannot hold control or format characters. A message from the human carries role `human`, so agents do not trust a name alone. The core adds it when the operator creates a conversation, or at the first operator write to a conversation that a chat created. The human's messages wake listeners like any other message.

A lead or co-lead can call `invite_participant` to add a slot, up to 17 participants, or reissue an unused code for an existing slot. An explicit reinvite gives a joined or retired slot a new code: the slot becomes active again with its history and tasks, the previous token stops working, and the core closes any open session stream for that slot. Retiring a participant or closing the conversation also closes the affected session streams. A late co-lead invitation also promotes the creator. The bridge records each invitation change as an event, and returns a private code and one join prompt template to the caller. The create tool also returns the template with its initial join codes.

### Plugins

Implement the core in Go as one static binary, with no runtime to install. Register each plugin as a user-configured executable, in any language, communicating over MCP stdio. The plugin declares its contract version and capabilities. A fresh-run plugin accepts an assignment with a stable ID and exposes start, inspect, and cancel operations. It owns its credentials, native session identifiers, CLI flags, and model selection. Credentials come from the environment and are never stored in the database or logs. The core runs plugin executables with argument lists, explicit timeouts, and no shell. The initial Z.ai integration uses GLM through OpenCode on macOS. It uses an isolated OpenCode process and restricts writes to private scratch space. The core passes its own path to each plugin in `AGENTCY_BIN`, and a fresh run reaches the core through `agentcy fresh-worker`.

A fresh run participates like an existing chat. The plugin launches the agent with the core configured as an MCP server and a join code for its slot, so the agent reads the brief, submits proposals, and takes part in the critique round through the same tools. The plugin reports launch state and exit status; it does not relay message content.

A fresh run is read-only in the workspace. It may read files and run analysis, and it may not edit, commit, or install. Each assignment has a timeout and an optional token or cost cap set in the plugin configuration. The core cancels the run when its round or conversation closes.

A chat with MCP support connects directly to the core and can join without a provider plugin. For an app without a local MCP connection, the initiating chat produces a ready-to-paste assignment for each round and records the pasted reply with its participant name and manual origin.

### Host turn model

An existing chat calls tools during a turn its user started. In an active team turn, the lead assigns complementary work from one brief and takes a substantive part. Participants send questions and checkable evidence directly to peers who can use them. The lead verifies the findings against the brief and returns one conclusion with supporting evidence and any gaps. It can record the outcome and close during the same turn when the user asks at kickoff. An idle chat can resume on its next turn. Honor explicit one-time reads.

Each waiting read returns within the host's tool timeout, checks participant status, and gives the chat its next action. The shim counts consecutive silent waits and resets the count when the participant receives or posts a message. In open discussion, a worker that has replied and has no work it can continue stops after three silent waits. After its last reply is `done`, it stops after one silent wait, or after five while other agents still work. A lead or co-lead waits up to eight silent waits while peers are working, starting through a fresh run, or checking a blocker. If the only remaining peers hold unused manual invitations, tell the user which codes still need to be redeemed. Review deadlines and user instructions take precedence.

### Published skills

The shim publishes its conversation playbooks as skills through the MCP Skills extension (`io.modelcontextprotocol/skills`). A skill is a `SKILL.md` file that the host loads on demand. The shim serves each skill as the resource `skill://<name>/SKILL.md`. The manifest lists the URI, the SHA-256 digest, and the size of each file, computed from the bytes the shim serves.

Two skills exist: `lead-conversation` and `join-conversation`. A skill is static packaged text and holds no user data.

A host without the extension relies on the server instructions and the tool descriptions. They stay sufficient on their own.

### Task-backed fresh runs

When the host declares the MCP Tasks extension (`io.modelcontextprotocol/tasks`), `launch_participant` returns an MCP task for a new fresh run, backed by the persisted assignment. An MCP task is a protocol object that the host polls for status. The host can also cancel it, and a cancel stops the fresh run.

The MCP task ID is an unguessable capability. The core never logs it and status never shows it. It is valid only in its own workspace.

Other hosts get the immediate result and follow the launch state through `conversation_status`.

### Human confirmation

When the host declares elicitation, closing a conversation first asks the human to confirm through a one-field form. A decline or a cancel changes nothing. Retiring a participant is never confirmed, so that an unattended lead can still remove a participant.

A host without elicitation and a fresh worker behave as before. A headless host that declares elicitation and cancels the form cannot close. A form never asks for sensitive data.

### Operator tools

The core serves a versioned operator protocol on its Unix socket for a local interface such as a terminal UI. One request line gets one response line. The protocol lists the active or archived conversations of a checkout's workspace, archives and unarchives closed conversations, renames a conversation, searches briefs and messages, shows a conversation's status and messages, creates a conversation with an invitation and a ready join prompt for every slot, invites, launches or relaunches a configured plugin into a worker slot, pings a participant out of band, posts notes and questions as the human, closes with an optional summary, and lists plugin names and models. Requests name the checkout by its absolute path, and the core derives the workspace from it. The interface polls `operator_show`; the protocol has no waiting read. [Operator protocol](protocol/README.md#operator-protocol)

The terminal UI in `tui/` is the human's surface on this protocol. `agentcy ui` starts the core when it is not running and then runs the UI for a checkout. The UI lists the checkout's conversations, creates a conversation with a lead and worker slots, launches and relaunches plugin worker slots, shows the transcript, member status, and live `listening`/`connected` presence, pings members out of band, posts notes and questions as the human, reissues codes, and closes. It switches the chat list between active and archived conversations, archives or unarchives a closed one, renames a conversation, and searches all conversations; a result opens its conversation at the matching message. It lists the user's running cores and stops a selected one with SIGTERM. It signals only processes of the user that run an agentcy core command line. It polls `operator_show` about once a second. It shows join codes only in its invite window and keeps them in memory. It strips control characters and escape sequences from model text before it draws it.

The lead is always an interactive chat, such as a desktop app, that joins with the ready join prompt the UI shows. Each worker slot is either a manual chat with its own join prompt or a configured plugin that the UI launches.

Planned: a CLI with `argparse` for debugging that lists workspaces and conversations, shows a conversation's messages, assignments, and journal, approves or rejects a pending publication, rebuilds the Markdown index, and shows the core's status and log location. At the default level the core logs identifiers and states, not briefs or message bodies.

## Configuration

Keep user-level configuration in private local application data: registered plugins with their executables and models, timeouts, cost caps, and the wait bound. Keep workspace enrollment in a committed file at the repository root: the entry document and the permitted knowledge locations as paths relative to the root. A fresh checkout is enrolled as soon as it is cloned. The core resolves every enrolled path, rejects any that leaves the root including through a symlink, and ignores enrollment files outside the root.

## Conversation protocol

- **Identity and delivery.** A conversation belongs to one workspace. Its record contains the brief, invited participants, and ordered messages. Each message has a stable ID, author, kind, body, an optional addressee, and an optional reply target. Each submission carries a client-chosen submission ID; retrying the same submission records one message. Because models retry with new identifiers, the core also treats a message with the same author, conversation, and matching text, kind, addressee, and reply target within a short window as the same submission and returns the first message ID. Cursor-based reads let participants reconnect and catch up. A one-time read from cursor zero returns the brief, the roster, and every message; a later read returns only new messages from other members, and a waiting read returns messages only and collects a short burst of replies into one result, so a listening chat spends few tokens.
- **Ongoing work.** Participants can post notes, questions, findings, blockers, and completion messages. The core records lifecycle events. Every member can read every message. An unaddressed message wakes all listeners. An addressed message wakes its addressee and the leads. A blocker wakes the leads and any addressee. A lead can reply to a blocker with a `question` addressed to a teammate who can help. That teammate sends the answer to the blocked member. An unrelated message stays unread until the listener catches up or a relevant message arrives. The latest blocker, resume, or completion message sets the member's status. A member who owns any blocked task shows as blocked. Use a completion message when your part is done. Only content deliberately contributed to the task is shared. A participant's native chat history stays with its host.
- **Tasks.** A lead or co-lead assigns a task by sending a `task` message to a member. The lead owns the task timeline: each `task` message carries `due_minutes`, the number of minutes (1 to 1440) the lead expects the task to take. The core rejects a task without it and rejects it on any other message kind. The due time is the assignment time plus `due_minutes`. A handoff or reassignment keeps the due time. The current owner replies to the original task ID with `done` to close it, `blocker` to mark it blocked, or `resume` to continue after a blocker clears. Only the current owner can resume a blocked task. Status lists open tasks by current owner with each task's due time, and marks a task `overdue` when that time has passed. Before the human closes or archives a conversation with unfinished tasks, the terminal UI asks the human to check them or to deprecate them. A deprecated task is ended: the core records a `deprecate` message on it in the same step as the close or archive, and its state is `deprecated`. A waiting lead sees the IDs, owners, and overdue tasks in its guidance. The owner of an overdue task is told to report progress or post a blocker. A task stored before due times existed has no due time and is never overdue.
- **Handoffs.** The current owner sends a `handoff` message to the receiver with the task ID as the reply target. The body carries a summary, open questions, and references to relevant messages. The receiver replies to the handoff ID with `accept` or `decline`. Acceptance transfers the task and clears its blocker. A declined handoff leaves the work with the sender. When a participant is retired, the core moves each unfinished task to the least busy available worker (joined, invited, or launched), or to the lead when no worker is available, with a `reassign` message addressed to the new owner. The lead cannot be retired until another participant becomes lead.
- **Structured review.** A lead can create a sealed conversation with a deadline of 1 to 60 minutes, defaulting to 10. Each worker submits one independent proposal with kind `proposal`. The bridge holds proposals privately until every joined worker submits, the deadline passes, or a lead calls `release_proposals`. Release publishes the proposals together with new transcript IDs and an event that names missing workers. Participants can then critique the proposals with ordinary messages. Closing a conversation before release discards the held proposals. Apply the same visibility rule to memory proposals derived from sealed answers.
- **Existing chats.** Bind a join code to one invited slot. During its active turn, the joined chat requests assignments using bounded waits. Preserve its cursor and pending work when the host ends the turn, so the user can resume by asking the chat to continue the task.
- **Fresh runs.** A lead or co-lead calls `launch_participant` with a configured plugin and a worker name. A local worker tracks assignments and deadlines after the initiating chat disconnects. Persist an assignment before dispatch. The worker inspects each running fresh run every 5 seconds, so a state change shows up to 5 seconds later; a stop or a cancel acts at once. `conversation_status` includes `launch_state` for a slot with an assignment. A fresh worker can show as invited while its launch is `queued`, `starting`, or `running`. Lead guidance treats these states as work in progress. If a launch ends before the worker joins, report its state and any open tasks. On restart, reconcile the assignment ID with the plugin. If the provider cannot confirm whether the run started, report the uncertain state before issuing another launch. Once a fresh run reaches a confirmed terminal state (`finished`, `failed`, `cancelled`, or `timed_out`), a lead, co-lead, or the human may pass `relaunch` to start a new fresh run in the same worker slot, keeping its name, history, and open tasks while revoking the previous token and session stream; `relaunch` refuses active runs, `unknown` runs, and live manual chats that have no ended fresh run.
- **Lifecycle.** A conversation is open, closed, or cancelled. A lead, a co-lead, or the human closes it. Closing can record a final summary. It cancels running assignments and expires unused join codes. Participant tokens retain read access to the closed record. Any chat of the workspace can also list the closed conversations and read one without a token, archived or not, so later work can reuse the outcome; the record holds only published messages, and an open conversation stays private to its members and gets the same error as an unknown one. Leads can retry close safely. A lead, a co-lead, or the human can reopen a closed conversation (with an optional note), returning an archived conversation to the active list; existing tokens regain write access while previously expired join codes stay expired. The human can archive a closed conversation to hide it from the active list, and unarchive it. Archiving changes no access. A conversation's title is the first line of its brief until the human renames it; the human can rename it in any state, and a rename changes neither the brief nor any access. Closed conversations are kept for a configurable period and then removed with their journal entries.
- **Failures.** Publication conflicts, failed or cancelled plugins, uncertain launches, and expired deadlines are recorded as messages in the conversation, so a reconnecting chat sees them through its cursor.

## Storage and retrieval

Identify a workspace by its repository, using the Git common directory when the root is a checkout or worktree and the canonical path otherwise. Keep one SQLite database per workspace in private local application data, created with owner-only permissions. It holds the conversation record, participant state, assignments, memory proposals, publication history, and a rebuildable index of Markdown sections. Worktrees of one repository share the conversation and its proposals; each participant reads Markdown from its own checkout, and publications are written to the checkout of the approving participant. Markdown is the authoritative source for published knowledge and can travel through the project's existing Git workflow.

The Go core uses `database/sql` with `modernc.org/sqlite`, a SQLite build without CGO, and keeps each SQL statement beside the function that runs it. It opens only schema version 15 and creates a new store from the embedded version-15 schema. A store of an older version migrates with the Python core at commit 17eb9ce, the last commit that includes it: `make migrate-stores` there opens each store once with the Python code, which applies SQLAlchemy Core and Alembic migrations in batch mode. Use WAL mode, a busy timeout, short write transactions, and an index on conversation ID and message sequence for incremental reads. Use FTS5 for heading and section search scoped by project path. A second FTS5 index holds a copy of every brief and published message for operator search. Insert triggers on the conversation and message tables keep it in step, so held sealed proposals enter it only when released. Each query word is quoted and matched as a prefix, so query text cannot use FTS5 operators. SQLite allows readers to continue during a WAL write and serializes writers. SQLite replaces the PostgreSQL default from the backend guide because the tool runs locally with nothing to install. [SQLite WAL](https://sqlite.org/wal.html) and [SQLite FTS5](https://sqlite.org/fts5.html)

Enroll a workspace with its entry document and permitted knowledge locations. Follow the project index to the closest relevant folder index. Search results return headings, short summaries, paths, and links. Fetch full sections on demand. Refresh the index on demand: at query time compare each indexed file's size and modification time with the disk, hash only the files that changed, and re-index by path and heading. No file watcher runs. Reconcile edits, deletions, and branch switches against the files on disk. Rebuilding the index only updates SQLite.

## Publishing project knowledge

Expose `propose_knowledge` to participants. Each proposal carries one claim or decision, its folder scope, supporting evidence, and its originating message. Evidence is a file path relative to the root, an optional line range, a quoted excerpt, and the commit it was read at. A proposal either names the existing claim it extends, corrects, or duplicates, or declares itself new. The core applies a common publication policy across plugins.

The core performs the checks it can make without judgment. The human makes the rest, because published notes are read by every future agent and a manipulated agent must not be able to write a durable instruction into the knowledge base.

| Check by the core | Outcome when it fails |
| --- | --- |
| Target path is inside an enrolled knowledge location and resolves inside the root | Reject the proposal with the path. |
| Each evidence excerpt matches the file at the cited commit or on disk | Mark the evidence as unverified and show the difference. |
| The named existing claim exists in the index | Reject the proposal and offer FTS matches on the claim heading as hints. |
| Claim and evidence are within size limits | Reject the proposal. |

| Decision by the human | Outcome |
| --- | --- |
| New finding with checkable evidence, or an explicit user decision | The proposal waits as pending. On approval, add the claim and its evidence to the closest relevant note. If it needs a new topic file, link it from the nearest index. |
| Hypothesis, suggestion, or agreement without supporting evidence | Keep the finding open in the conversation for investigation. |
| Correction, contradiction, or proposed replacement | Present the existing text, proposed change, and evidence. Preserve the existing claim pending review. |
| Duplicate finding | On approval, associate the new evidence with the existing entry and keep one Markdown claim. |

An explicit user decision is a message whose author is the human or a manual-origin participant. An agent's statement that the user agreed is a claim like any other. The human approves or rejects from any joined chat or from the operator CLI. The core never commits; the Git diff is the second review.

For example, if the worker code and its tests establish that only idempotent jobs are retried, an agent can propose that finding during the task and the user can approve it from the initiating chat. If another participant discovers a counterexample, record the conflict and present both sources.

### Note and index format

An index is the `README.md` of a folder, or `index.md` when no README exists. The project index is the enrolled entry document. An index links to the topic files and sub-folder indexes beneath it with relative Markdown links. A topic file holds claims under a heading the core manages, named `## Verified claims` by default. Each claim is a list item with a stable claim ID in a trailing comment, followed by an indented evidence list of path, line range, commit, and the originating conversation ID. The core edits only the managed heading's section and the link lists it created in indexes. Everything else in the file is human-written and preserved byte for byte.

### Applying a publication

Restrict publication to enrolled knowledge paths. Journal the intended change and original file hash before applying it. Serialize publications per workspace, detect intervening edits, and replace files atomically. On restart, compare current content with the original and intended hashes. Complete any outstanding topic or index update when its base still matches. Surface a conflict as a conversation message when the file has diverged.

## Delivery and acceptance

1. **Core.** Connect two independent MCP clients through the shim with all provider plugins disabled. Exchange messages, disconnect, reconnect, and verify that replaying a submission produces one message, both with the same submission ID and with a new ID and the same body. Verify the core starts once under concurrent shims, exits when idle, and reports its cold start time.
2. **Security.** Reject calls with a missing or foreign participant token, a reused join code, and an expired join code. Reject operator requests with a missing or wrong operator key. Reject a publication path that resolves outside an enrolled location, including through a symlink. Verify database and socket permissions. Run `ruff` with the `S` rules and `pip-audit` with a committed `uv.lock`, and `govulncheck` with a committed `go.sum`.
3. **Ongoing work.** Start a conversation with one brief, an active lead, and two configured read-only workers. Delay one worker's join and confirm the lead keeps working and treats the launch as in progress. Have the other worker ask that peer directly for evidence and mark its task blocked. After the evidence arrives, have the owner resume the task. Verify that only the owner can resume it. Complete both tasks and return one verified conclusion that cites both workers' evidence without another user reminder. Also exercise handoff acceptance and refusal and recover cursors after reconnect. Close with a summary and verify that participants can read it, posting is disabled, unused join codes expire, and running assignments stop.
4. **Memory.** Demonstrate root-to-folder discovery, a pending proposal approved and written, a rejected proposal that leaves the file untouched, duplicate evidence, a conflicting proposal, and a human edit during publication. Interrupt a topic and index update, then recover it. Confirm isolation across repositories and sharing across two worktrees of one repository. Open a database created by the previous schema version.
5. **Plugins and review.** Run contract fixtures against each plugin. Exercise a review with an existing chat, a fresh read-only CLI run that joins through the core, and a manual participant. Check sealed-answer visibility, deadline handling, failed plugins, cancelled runs, and restart recovery. Smoke-test the Z.ai plugin in an environment with OpenCode installed.
6. **Tool usability.** On each target host, an agent given only the tool descriptions completes join, read, submit, and wait without further instructions.
7. **Performance.** Benchmark incremental message reads and scoped knowledge search against 100,000 messages and 10,000 Markdown sections. Target a local warm-query p95 under 100 ms and state the cold start bound. Report the dataset, environment, latency, and concurrency results. Measured on an Apple M1 with 100,000 messages (see [Performance](docs/development.md#performance)): warm p95 0.4 ms for read, 0.5 ms for status, 0.7 ms for post, 1.0 ms for `operator_show`, and 26 ms for `operator_search`. Cold start bound: 75 ms from shim start to the first tool result without a running daemon, plus one replay of about 50 ms for the first status of each room after the daemon starts. With 20 listening shims the core and shims used 1.1 to 1.4 CPU seconds per minute and 473 MB peak RSS.
8. **Protocol revisions.** Run the same join, read, submit, and wait flow with a client on an older revision and a client on revision 2026-07-28, on stdio and on the HTTP transport.
9. **Skills.** Verify that every manifest digest and size matches the bytes the shim serves, and that a host without the extension completes item 6 with the instructions and tool descriptions alone.
10. **MCP tasks.** Reject an MCP task ID from another workspace. Verify that MCP task IDs do not appear in logs or in `conversation_status`, that cancelling an MCP task cancels the fresh run, and that a host without the extension gets the immediate result.
11. **Confirmation.** Without elicitation support, close behaves as before. With it, a decline or a cancel changes nothing. Retire never asks, with or without elicitation support.

## Glossary

| Term | Meaning |
| --- | --- |
| Workspace | One repository, identified by its Git common directory, with one database. |
| Checkout | A directory holding the workspace's files; a clone or a worktree. |
| Participant | A chat, fresh run, or manual source holding a token for one conversation slot. The initiating chat is a participant. |
| Slot | An invitation for one participant, bound to one join code. |
| Human | The user as a member of a conversation, acting through the operator protocol without a token. |
| Operator key | A random secret in `operator.key` that authorizes operator protocol requests. The core replaces it at each start. |
| Assignment | Work the core dispatched to a plugin for a fresh run, with a stable ID. |
| Round | A phase of a structured review: proposals, then one critique. |
| Handoff | Transfer of pending work from one participant to another, accepted by the receiver. |
| Proposal | A request to publish one claim or decision to the knowledge base. |
| Claim | One published finding or decision with its evidence, identified by a claim ID. |
| Note | A topic file holding claims under the managed heading. |
| Index | A folder's README.md or index.md linking to notes and sub-folder indexes. |
| Skill | A packaged `SKILL.md` playbook that the shim publishes to hosts through the MCP Skills extension. It is static text with a manifest of URI, digest, and size. |
| MCP task | A protocol object that a host polls for the status of one long operation and can cancel. The bridge uses it for fresh runs. It is not a conversation task, which is a message of kind `task` with an owner and a due time. |
