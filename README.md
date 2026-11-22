# dockdoctor

Your container won't start, a port won't bind, or a mount fails with
`read-only file system`. **dockdoctor** checks your Docker and Compose setup
for the usual causes, then prints the reason and the command that fixes it.

![dockdoctor finding a restart loop, a busy port, a missing config file and an unset variable](docs/demo.gif)

The full output of a run:

```text
$ dockdoctor
dockdoctor Docker 29.9.2 on Docker Desktop · Compose 5.6.1 · project "demo" (compose.yaml) · 4 containers

✗ demo-worker-1 is in a restart loop
  It has restarted 8 times and isn't staying up. Exit code 127: the entrypoint or command wasn't found in the image.
  Fix:
    docker logs --tail 50 demo-worker-1

✗ demo-hog-1 was killed for running out of memory
  The kernel stopped it because it used more memory than it was allowed (OOMKilled). It will keep dying the same way under load.
  Fix:
    docker stats --no-stream demo-hog-1
    # raise or remove its memory limit (mem_limit / deploy.resources.limits.memory), or add RAM or swap
  Docs: https://docs.docker.com/engine/containers/resource_constraints/

✗ Port 8080 is already in use (needed by web)
  Something else on this machine is listening on it, so the container fails with `bind: address already in use`.
  Fix:
    lsof -nP -iTCP:8080    # see what holds it
    # stop that program, or publish the service on another host port

✗ ./nginx.conf doesn't exist, so Docker will create it as a directory (web)
  Docker turns a missing bind mount source into an empty directory, so the app finds a folder where it expects a file (`is a directory`).
  Fix:
    # remove the directory Docker created (if any), then create the real file:
    sudo rmdir ./nginx.conf 2>/dev/null; touch ./nginx.conf

! demo-cache-1 is unhealthy
  Its healthcheck keeps failing, so containers that depend on it with `condition: service_healthy` won't start.
  Last check said: redis not answering on 6379
  Fix:
    docker inspect --format '{{json .State.Health}}' demo-cache-1
    docker logs --tail 50 demo-cache-1

! The Compose file uses variables that aren't set: DB_PASSWORD
  Compose replaces them with an empty string, so passwords, paths or image tags end up blank. That breaks things quietly.
  Fix:
    # put them in a .env file next to the Compose file:
    DB_PASSWORD=...
  Docs: https://docs.docker.com/compose/how-tos/environment-variables/variable-interpolation/

4 passed · 4 problems · 2 warnings · 5 skipped
```

That's a real run against a Compose project broken on purpose (on Linux the port fix uses `ss` instead of `lsof`).
It only reads. It never starts, stops or changes anything.

## Install

Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/supremeinferno/dockdoctor/main/install.sh | sh
```

This puts a single binary in `/usr/local/bin` (or `~/.local/bin` if that isn't writable). You can also
download it from [Releases](https://github.com/supremeinferno/dockdoctor/releases), or, with Go 1.25+:

```sh
go install github.com/supremeinferno/dockdoctor@latest
```

## Use

Run it in the folder with your `compose.yaml`:

```sh
dockdoctor                         # check Docker, your containers and the Compose file here
dockdoctor -f path/to/compose.yaml # check a specific Compose file (repeat -f to merge several)
dockdoctor -v                      # also list what passed and what was skipped
dockdoctor -json                   # machine-readable, for scripts and coding agents
```

The exit code is `1` when it finds a problem, so you can use it in CI or before `docker compose up`.

## What it checks

| Check | What goes wrong without it |
|---|---|
| **Daemon reachable** | Tells apart "daemon not running" from "your user can't use the socket", with the fix for each |
| **Snap Docker** | Ubuntu's snap only reaches your home directory: mounts in `/opt` or `/srv` fail with `read-only file system` |
| **Compose v2** | The old `docker-compose` v1 can't read current Compose files |
| **Disk space** | A full Docker disk breaks pulls, databases and logs |
| **Restart loops and OOM kills** | Explains the exit code (127 = command not found, 137 = killed, 139 = wrong architecture…) |
| **Unhealthy containers** | Shows the healthcheck's last output, the reason `service_healthy` dependents never start |
| **UFW bypass** | Docker's iptables rules sit in front of UFW, so `ufw deny` doesn't protect published ports |
| **Compose file valid** | Shows Compose's own error |
| **Unset variables** | `${DB_PASSWORD}` silently becomes an empty string |
| **host.docker.internal** | Defined by Docker Desktop, not by Docker Engine on Linux, unless you add `host-gateway` |
| **Ports free** | Finds what already holds a port before the container fails with `address already in use` |
| **Bind mount sources** | A missing `./config.yml` becomes an empty root-owned *directory* |
| **Rootless ports** | Rootless Docker can't publish ports below 1024 without a sysctl |

Every check comes from a real problem people ran into and asked about. If dockdoctor missed what broke your
setup, [open an issue](https://github.com/supremeinferno/dockdoctor/issues/new) with the error message: that's
how new checks get added.

## License

[MIT](LICENSE)
