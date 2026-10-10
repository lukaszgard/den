# Editing den.yaml

Edit den.yaml while den runs, mistakes included

## What it is

den applies a changed `den.yaml` without a restart, and keeps the connections whose entry did not change. Press `e` in the Config panel to edit the file: when your editor closes, den checks it and applies it. After an edit made outside den, press `R` in any panel. Every check lists every problem in the file, with its line. An error (YAML that does not parse, a value of the wrong kind, a setting that is missing) keeps den from using the file: den keeps running the last version without errors and says what is wrong. A warning (a key den does not know, a panel it does not have, a file that is not there) is named, and the file is applied anyway.

## What you can do

- Edit `den.yaml` from the Config panel and have it applied when the editor closes.
- Add a panel to `menu_order`, or hide one with `disabled_panes`, and see the menu change at once.
- Change the log level or the log file without restarting.
- See every mistake in the file at once, each with its line and a hint, in a pop-up, in the Config panel and marked beside the lines it is on.
- Press `p` in the Config panel to list every problem, and `enter` on one to see its line.
- Have a misspelt key named with the key you most likely meant: `reconect` gets the hint "did you mean reconnect?".
- Edit again, or undo the edit, when the file has an error.
- Be asked before an edit stops a running connection, with the connections named.
- Start den with a broken file and fix it from den: it opens on the Config panel with the errors in front.

## Before you start

- An editor in `$VISUAL` or `$EDITOR`; den opens `vi` without one (`notepad` on Windows).
- A graphical editor must wait for its window to close: `code --wait`, `subl --wait`, `zed --wait`.

## Usage

1. Open the Config panel and press `e`.
2. Change the file, save it, and close the editor.
3. A file without errors is applied, and the status bar says what the reload kept and stopped.
4. If the change stops a running connection, den names it first: `y` applies the file and stops it, `n` keeps the old config until you press `R`.
5. If the file has an error, nothing changes: `e` edits it again, `u` puts it back as it was before your edit, `esc` leaves it as it is.

## Limitations

- den does not watch the file: after an edit outside den, press `R`.
- A file with an error is never applied, not even its correct parts.
- Applying a file stops every running connection whose entry it changes or removes; den asks first, but cannot keep one running on an old entry.
- `u` undoes the edits made through `e` since the error appeared, not older ones.
- A key den does not know is a warning, not an error, so a config that worked before still loads.

## When it goes wrong

- Pressing `e` returns at once and den says `den.yaml unchanged`: your editor started in the background. Make it wait, such as `code --wait`.
- The status bar says `den.yaml has 2 errors, den runs the last good version`: the file on disk is not what den runs. Open the Config panel to see the errors and their lines.
- The status bar says `den.yaml not applied`: you answered `n` to applying it; `R` asks again.
- A panel you added does not appear: the Config panel names a panel den does not have, with the one it most likely is.

## See also

- Getting started
- Checking den.yaml
- Menu and logs
- Command line
- `docs/config-reload/README.md`
- `docs/schema/README.md`
