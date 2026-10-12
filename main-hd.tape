# The home page's recording: main.tape at twice the size, as MP4 only. The look
# is docs/tape-settings-hd.tape, shared with every feature's site video.
#
# A 1200 px video shown at ~900 CSS px is under 2x on a retina screen and looks
# soft. This renders the same script at 2400x1600 with the font doubled, so the
# text stays sharp. The GIF stays small in main.tape for the README, which GitHub
# plays only as a GIF. The steps live in main-steps.tape, shared by both.
Output main-hd.mp4

Source docs/tape-settings-hd.tape
Source main-steps.tape
