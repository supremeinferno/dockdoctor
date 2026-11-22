# Records docs/demo.gif with https://github.com/charmbracelet/vhs.
# Setup: ~/myapp holds compose.yaml and nginx.conf.example from docs/demo/,
# and something already listens on port 8080 (old_dev_server.py).
Output "docs/demo.gif"
Set Shell zsh
Set FontSize 18
Set Width 1280
Set Height 800
Set Padding 28
Set Theme "Catppuccin Mocha"
Set TypingSpeed 45ms
Set Framerate 30
Env PATH "$HOME/.local/bin:/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin"
Hide
Type "setopt interactivecomments; cd ~/myapp; export PS1='~/myapp $ '; clear"
Enter
Sleep 500ms
Show
Sleep 600ms
Type "docker compose up -d"
Sleep 300ms
Enter
Sleep 5s
Hide
Type "clear"
Enter
Sleep 300ms
Show
Type "dockdoctor"
Sleep 300ms
Enter
Sleep 9s
Hide
Type "clear"
Enter
Sleep 300ms
Show
Type "pkill -f old_dev_server.py     # the forgotten dev server on :8080"
Enter
Sleep 600ms
Type "rmdir nginx.conf && cp nginx.conf.example nginx.conf"
Enter
Sleep 600ms
Type "echo DB_PASSWORD=s3cret > .env"
Enter
Sleep 700ms
Type "dockdoctor"
Enter
Sleep 3s
Type "docker compose up -d && curl -s localhost:8080"
Enter
Sleep 6s
