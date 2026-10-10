# The home page's recording: main.tape at twice the size, as MP4 only. Its font
# (22) is twice docs/tape-settings.tape's (11), and the margin and radius follow.
#
# A 1200 px video shown at ~900 CSS px is under 2x on a retina screen and looks
# soft. This renders the same script at 2400x1600 with the font doubled, so the
# text stays sharp. The GIF stays small in main.tape for the README, which GitHub
# plays only as a GIF. The steps live in main-steps.tape, shared by both.
Output main-hd.mp4

Set WindowBar Colorful

Set Margin 30
Set MarginFill "#674EFF"
Set BorderRadius 20

Set FontFamily "JetBrainsMono Nerd Font Mono"
Set FontSize 22
Set Width 2400
Set Height 1600

Set TypingSpeed 120ms

Source main-steps.tape
