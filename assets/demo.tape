# Records the README demo. Run from the repo root: vhs assets/demo.tape
# Needs VHS (brew install vhs) and Spotify credentials (bs setup).

Output assets/demo.gif

Set Shell "bash"
Set FontSize 20
Set Width 1200
Set Height 460
Set Padding 30
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms

Hide
Type "export PATH=$HOME/.local/bin:$PATH && clear"
Enter
Show

Copy "https://music.apple.com/us/album/welcome-wagon/1751983285?i=1751983389"
Type "bs "
Sleep 500ms
Paste
Sleep 500ms
Enter
Sleep 3s

Down
Sleep 300ms
Down
Sleep 500ms
Up
Sleep 200ms
Up
Sleep 300ms
Enter
Sleep 1s

Type "?"
Sleep 2s
Type "?"
Sleep 1s
