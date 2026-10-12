# The site videos' look: docs/tape-settings.tape at twice the size. main-hd.tape
# and every docs/**/<feature>-hd.tape Source it right after their Output line.
#
# A 1200 px video shown at ~900 CSS px is under 2x on a retina screen and looks
# soft. 2400x1600 with the font doubled keeps the same columns and rows, so the
# TUI lays out as in the GIF and the text stays sharp. Keep the two files in step:
# the margin, radius, font size and window here are twice theirs.

Set WindowBar Colorful

Set Margin 30
Set MarginFill "#674EFF"
Set BorderRadius 20

Set FontFamily "JetBrainsMono Nerd Font Mono"
Set FontSize 22
Set Width 2400
Set Height 1600

Set TypingSpeed 120ms
