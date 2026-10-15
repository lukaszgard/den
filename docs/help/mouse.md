# Mouse

Click, double-click, scroll and copy text in den

## What it is

den reads the mouse as well as the keyboard. A click selects what it lands on, a double-click does what `enter` does there, the wheel moves through what is under the pointer as the arrow keys do, and a drag selects text and copies it. Everything still works from the keyboard: the mouse is a shortcut to the same things, not a second set of features.

## What you can do

- Click a row of the menu to show its panel, the menu keeping the focus; double-click it to open the panel.
- Click a row of a panel to focus the panel and select the row: a service, a tunnel, a VPN, an AWS session or profile, a secret or one of its values, a runbook, a Docker service, a problem of `den.yaml`, a Help topic.
- Double-click a row to do what `enter` does on it: open a service's detail view or a runbook's view, log in to an AWS session, open a secret or a Help topic.
- Turn the wheel over a panel to move through it as ↑ and ↓ do, wherever the focus is; the focus stays where it is.
- Click a tab of a service's detail view or of a runbook's view to show it; `‹` and `›` step to the tabs the bar has no room for.
- Click a terminal's screen, or double-click its tab, to type into it; click another tab to give den the keys back.
- In a runbook's view, click a run in History or a file in Files, and double-click it to see the run's whole log or the file.
- Scroll the logs, a runbook's Run and About tabs and a file shown with the wheel, and the shortcuts pop-up (`h`) too.
- Drag over text to select it, in reverse video: letting go copies it, and the status bar says `copied 3 lines`. A key, a click or the wheel clears the highlight.
- Press `M` to give the mouse back to the terminal, whose own drag-to-select then works as it always did; `M` again gives it back to den. While the terminal has it, the status bar ends with `M: mouse to den`.

## Before you start

- A terminal that reports the mouse; iTerm2, Terminal.app, Ghostty, kitty, WezTerm, Alacritty and Windows Terminal all do.
- Nothing to configure: den asks the terminal for the mouse when it starts and gives it back when it quits.

## Usage

1. Click Runbooks in the menu: its panel shows, and the menu keeps the focus.
2. Turn the wheel over the panel to move through the runbooks.
3. Double-click one: its view opens on the Run tab.
4. Click History, then a run: its log shows under the runs; double-click the run for the whole log.
5. Press `t` for a terminal, click Run to give den the keys back, then click the terminal's tab and its screen to type into it again.
6. Drag over a few lines of the terminal's screen, let go, and paste them anywhere: the box's border does not come with them.

## Limitations

- A selection stays in what it started in: the menu, the panel, the inside of a view's box, or one bar at the bottom. It never picks up the menu beside a panel or a border.
- A drag copies what is on screen: a line cut with `…` stays cut, a wrapped line comes out as several lines, and a drag does not scroll.
- For more than that, press `M` and select with the terminal, or hold its modifier while you drag: Option in iTerm2, Fn in Terminal.app (or ⌘R, which turns mouse reporting off), Shift in Ghostty, kitty, WezTerm, Alacritty and Windows Terminal.
- A click on a terminal tab's screen hands that tab the keyboard; the program in it, `vim` or `less` say, never sees where you clicked. The wheel reaches it as arrow keys while you type into it.
- Pop-ups, forms and y/n questions are keyboard-only: a click, a drag or a turn of the wheel while one is up does nothing, so a stray click never answers one.
- The hint lines and the bars at the bottom are text, not buttons.
- The wheel over the menu does nothing, so a notch too many never switches panels.
- Most panels show their cursor only while they have the focus: the wheel over one while the menu has it moves a cursor you see once you click or tab into the panel.
- No hover, and only the left button.

## When it goes wrong

- Clicks do nothing: the terminal does not report the mouse to den; the keys work as before.
- Clicks do nothing and the status bar ends with `M: mouse to den`: the terminal has the mouse; press `M`.
- A drag selects nothing: a pop-up or a question is up. Answer it, or press `M` and select with the terminal.
- The status bar says `copied … (sent to the terminal)`: the machine has no clipboard den can write to (no `xclip` or `wl-copy`, a session over SSH), so den asked the terminal to set it with OSC 52, which the terminal may refuse; tmux, for one, passes it on only with `set -g set-clipboard on`.
- Moving the mouse prints characters in the shell after den: den was killed before it could give the mouse back (kill -9, a crash); `reset` turns the reporting off.

## See also

- Getting started
- Terminal tabs
- Runbooks
- `docs/shortcuts/README.md`
