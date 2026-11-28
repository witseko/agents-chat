# Terminal UI

The terminal UI lets you start, watch, and close conversations without a kickoff chat.

## Build it

`make setup` installs it. To rebuild only the UI after a change, run this in the clone:

```bash
make tui
```

The binary goes to `bin/agentcy-tui`, beside `bin/agentcy`, where `agentcy ui` finds it. To use a binary elsewhere, set `AGENTCY_TUI` to its absolute path.

Without Go, `make tui` downloads the binary from the GitHub release that matches the `VERSION` file. Each release holds [binaries for macOS and Linux](development.md#release-binaries).

## Start it

Run this in the clone:

```bash
make ui WORKSPACE=/absolute/path/to/your/project
```

Or run the bridge directly in your project directory, with `--workspace DIR` to manage another checkout:

```bash
/absolute/path/to/agentcy/bin/agentcy ui
```

The command starts the core when it is not running, then opens the UI.

The screen has three columns: the workspace's chats on the left, the open chat in the middle, and its team on the right.

## Start a chat

1. Press `n`.
2. Write the brief.
3. Name the lead and the workers. For each worker, pick how it joins: a pasted prompt or a configured plugin.
4. Press `ctrl+s`. The UI creates the chat, launches the plugin workers, and opens a window with the join prompts.
5. Press `c` to copy a prompt (macOS) and paste it into its chat. The lead is always an interactive chat, such as Claude Desktop.

A join code works once and expires after one hour. The UI keeps codes in memory only. After a restart of the UI, select the member in the team panel and press `c` or `r` to get a new code.

To bring back a retired member, or to replace a member whose chat stopped, select the member and press `r`, or type `/reinvite Name`. The member gets a new code and keeps its name, messages, and open tasks. Its previous chat loses access.

To check whether a member is still connected or wake it out of a waiting read without sending a chat message, select the member in the team panel and press `p`, right-click the member and choose **Ping member**, or type `/ping Name`. The team panel shows `listening` while a member holds an active waiting read and `connected` while its shim session stream is open.

To start a new plugin run for a worker slot whose previous run ended, type `/relaunch Name [plugin]` (the plugin name is optional when only one plugin is configured), press `l` on the member in the team panel, or right-click the member and choose **Relaunch worker…**. Relaunch keeps the slot's name, history, and open tasks; it does not replace a live manual chat or an active/unknown run.

## Rename a chat

A chat's title is the first line of its brief. To give it another title, select the chat in the chat list and press `e`, or right-click the chat or its header and choose **Rename chat**. Type a title of up to 80 characters and press `⏎`. The brief stays as it is. To use the first line of the brief again, save an empty title.

## Archive and search old chats

When a chat closes, it moves into the **CLOSED** section under the open chats. The section is collapsed by default. Click its header, or select it and press `⏎`, to expand or collapse it.

Closed chats stay in the database. To keep the chat list short, move old chats to the archive:

1. Select a closed chat in the chat list and press `a`, or click **Archive** in the chat header. An open chat must be closed first.
2. Press `v`, or click **Archived** above the list, to see the archive. Press `a` there to move a chat back to the active list.

Archiving only hides a chat from the active list. Members can still read it.

To find an old chat, press `/` in the chat list or click **Search**. Type words and press `⏎`. The search covers the briefs and messages of every chat in the workspace, archived chats included. Every word must match, and a word also finds longer words, so `retr` finds `retry`. Choose a result and press `⏎`, or click it. The chat opens at the matching message and marks it.

## See and stop running cores

Press `c` in the chat list, or click **Cores** in the top bar, to see every agentcy core of your user. Each row shows the process ID, the uptime, and the core's data directory. The core of this UI is marked `(this UI)`.

To stop a core, for example an old core after an upgrade, choose it and press `s`, then `⏎`. The UI sends SIGTERM only to a process of your user that runs an agentcy core command line, and waits up to 15 seconds for it to exit. Chats stay in the store, and the next tool call starts a core again. After you stop the core of this UI, the top bar shows it as not reachable until a core starts again.

The list needs `ps`. It finds each data directory with `lsof`, and shows `unknown data directory` without it.

## Keys

| Key | Action |
| --- | --- |
| `tab` | Move between the message line, the team panel, and the chat list |
| `↑` `↓` | Scroll, choose a chat or member, or move between form fields |
| `⏎` | Send a message. Start it with `@Name` to address one member or `?` to ask a question |
| `ctrl+p` | Open the join prompts window again |
| `ctrl+x` | Close the chat, with an optional summary |
| `ctrl+o` | Turn the mouse off so the terminal can select text, or back on |
| `/` | Search all chats (in the chat list) |
| `a` | Archive the selected closed chat, or move it back from the archive (in the chat list) |
| `e` | Rename the selected chat (in the chat list) |
| `v` | Switch the chat list between active and archived chats |
| `c` | List the running cores and stop one (in the chat list) |

The bottom bar shows the keys for the part that has focus.

## Mouse

- Click a chat, a member, a field, a button, or a key in the bottom bar.
- Scroll with the wheel.
- Drag a column divider to resize it.

Clickable parts light up under the pointer.
