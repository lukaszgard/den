# Terminal tabs

The store's client inside den, already logged in

## What it is

In the detail view of a connected data store, den can run its client in a tab next to the log: `psql`, `mysql`, `redis-cli` or `mongosh`, started against the tunnel with the current credential. There is no command to copy and no token to paste. For OpenSearch and Neptune the tab is your own shell, with `DEN_URL` pointing at the signing proxy.

## What you can do

- Open up to nine terminals per service, each a real terminal: pagers, `\e` into your editor, colours and `ctrl+c` behave as outside den.
- Switch between the log and the tabs, rename them and close them; they keep running while you look elsewhere.
- Restart a client whose session ended, in the same tab, with the current credential.
- Use an editor or pager in a tab: den gives it the first `esc`, and a second within half a second gives den the keys back.
- Keep credentials off the command line: they reach the client through its environment or den's option file.
- Press `shift+enter` for a new line in an agent's prompt, where your terminal reports it (kitty, Ghostty, WezTerm, iTerm2); elsewhere it is `enter`.
- See the client's own cursor, with its shape, while you type into a tab.
- Check what your terminal does: run `vim` in a tab and the cursor becomes a bar in insert mode; if nothing here changes for you, the terminal does not report it (`go version -m $(which den)` also shows whether your den is a recent build).
- Find den among your terminal tabs by its title, `den · <pane> · N connected`; the tab shows a progress bar during an update or a runbook run where the terminal supports it.
- Keys: press `h` in the detail view.

## Before you start

- The client of the store: `psql`, the MySQL 8 client, `redis-cli` 6 or `mongosh` 2.5 (`den doctor --for rds`, `mysql`, `redshift`, `redis` or `documentdb`). A shell tab needs nothing.
- A connected service: on one that is not, the tab says so and opens nothing.
- Nothing else: no extra permission, no setting.

## Limitations

- No scrollback inside den: long output goes through the client's pager; what scrolled off the top is gone.
- `ctrl+t` belongs to den, so readline's transpose is not available in a tab, nor is vi mode at a shell prompt.
- `shift+enter`, the cursor shape and the progress bar depend on the terminal; a plain one gets `enter`, a block cursor and no bar. Apple Terminal and tmux (unless set up for it) are plain.
- Tab names are not kept when den quits; the clients stop with it, and when the service is disconnected.
- A client that reconnects by itself later reuses the token it started with, which may have expired; restart the tab instead.
- On Windows the tabs are built on ConPTY but have not been tried yet.

## When it goes wrong

- The client is not found: den prints its install command; Homebrew's `libpq` and `mysql-client` are found even when not on your PATH.
- A login fails in the tab while the copied command works in your own terminal: that is a bug in den; please report it.

## See also

- Services
- RDS and Aurora
- Runbooks
- `docs/terminal/README.md`
