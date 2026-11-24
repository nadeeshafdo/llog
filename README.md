# llog

A personal life-log CLI for one encrypted SD card and one plain-text file.
`llog` appends timestamped entries to `LOG.TXT` and nothing else. It never
rewrites history, never syncs anywhere, and refuses to write rather than
risk the file.

Built with Go as a single static binary. Terminal UI by Bubble Tea.

## Install

```sh
go install .
```

This puts `llog` in `~/go/bin`. Add that to your `PATH` if needed.

## Usage

```sh
llog                 # write a multi-line entry (Ctrl+S saves, Ctrl+C discards)
llog "quick note"    # append a one-line entry
llog help            # show usage
```

Entries look like this:

```
2026/11/19 18:30.. Today I finally fixed the Esc bug.
```

Keys while writing:

- Type to write. Left/Right/Home/End move the cursor anywhere.
- Enter inserts a newline, Backspace deletes, Tab accepts the top suggestion.
- Up/Down browses previous entries. Ctrl+F searches the log.
- Ctrl+E edits the shown entry (there Up/Down moves the cursor instead,
  Esc cancels without saving). Ctrl+S saves, Ctrl+C discards.
- Ctrl+A adds the word before the cursor to your dictionary.
- Backspace right after an autocorrect undoes it.

Piped input still works (`echo "x" | llog` appends `x`) and never opens
the prompt.

## Safety model

The log file is append-only by default and every write is guarded:

- The tool never creates `LOG.TXT`. If the card is not mounted, it refuses
  instead of writing to the wrong disk.
- Before each write it takes a full, hash-verified backup.
- It records the file fingerprint at startup and refuses the write if the
  file changed on disk since (for example an editor saved over it).
- Entry edits rewrite the file atomically (temp file, fsync, rename) and
  the result is re-read and verified: same entry count, every other entry
  byte-identical.
- A lock file (`LOG.TXT.lock`) keeps two `llog` processes from writing at
  once.
- No network, no sync, no telemetry. Entry text is never logged anywhere.

## Configuration

| Variable           | Meaning                                             |
| ------------------ | --------------------------------------------------- |
| `LLOG_FILE`        | Path to `LOG.TXT` (default `/media/nadeeshafdo/nadeeshafdo/LOG.TXT`) |
| `LLOG_BACKUP_DIR`  | Backup folder (default `.../nadeeshafdo/llog-backups`) |
| `LLOG_DICT`        | Spell dictionary path (default: system hunspell)    |
| `LLOG_AUTOCORRECT` | Set to `0` to disable autocorrect (underline stays) |
| `NO_COLOR` / `LLOG_NO_COLOR` | Set to disable red underline colors      |

Spell check uses the system hunspell dictionary when available and learns
words from your own log, so names and project terms stop flagging after a
couple of uses. Words added with Ctrl+A persist to
`~/.config/llog/words.txt`.

## Development

```sh
go vet ./...
go test ./...   # 78 tests, including a real-pty end-to-end save test
```

Layout: `main.go` (commands), `tui.go` (Bubble Tea prompt), `store.go`
(locked backup/append/edit/verify), `journal.go` (entry parsing),
`spell.go` (dictionary, check, suggest, autocorrect), `llog_TUI.drawio`
(screen designs for the prompt).
