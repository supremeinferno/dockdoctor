# Holds port 8080 for the demo, like a forgotten dev server. Serves nothing.
import socket, time
s = socket.socket()
s.bind(("", 8080))
s.listen()
time.sleep(1800)
