# dockdoctor

**Find out why your Docker setup is broken, and get the command that fixes it.**

[![test](https://github.com/supremeinferno/dockdoctor/actions/workflows/test.yml/badge.svg)](https://github.com/supremeinferno/dockdoctor/actions/workflows/test.yml)
[![release](https://img.shields.io/github/v/release/supremeinferno/dockdoctor)](https://github.com/supremeinferno/dockdoctor/releases/latest)
[![license](https://img.shields.io/github/license/supremeinferno/dockdoctor)](LICENSE)

Your container won't start, a port won't bind, or a mount fails with `read-only file system`, and the error
doesn't say why. Run `dockdoctor` in the folder with your `compose.yaml`: it checks for the usual causes and
prints the reason and the fix for each one.

![dockdoctor finding a restart loop, a busy port, a missing config file and an unset variable](docs/demo.gif)

```sh
curl -fsSL https://raw.githubusercontent.com/supremeinferno/dockdoctor/main/install.sh | sh
```

- **Read-only.** It never starts, stops or changes anything. It runs `docker info`, `docker inspect` and `docker compose config`, reads a few files, and briefly tries each published port to see if it's free.
- **One binary, no dependencies.** Linux and macOS, amd64 and arm64.
- **Fixes, not just warnings.** Every finding says why it breaks things and gives a command you can copy.

<details>
<summary>Full output of a run against a Compose project broken on purpose</summary>

```text
$ dockdoctor
dockdoctor Docker 29.9.2 on Docker Desktop · Compose 5.6.1 · project "demo" (compose.yaml) · 5 containers

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

✗ myapp-api-1 is in a restart loop
  It has restarted 8 times and isn't staying up. Exit code 127: the entrypoint or command wasn't found in the image.
  Fix:
    docker logs --tail 50 myapp-api-1

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

6 passed · 5 problems · 2 warnings · 5 skipped
```

On Linux the port fix uses `ss` instead of `lsof`. In a terminal, long lines wrap to its width.

</details>

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/supremeinferno/dockdoctor/main/install.sh | sh
```

The script picks the binary for your OS and CPU from the [latest release](https://github.com/supremeinferno/dockdoctor/releases/latest) and puts it in
`/usr/local/bin`, or `~/.local/bin` when that isn't writable. Want to read it first? It's
[short](install.sh). Each release also has a `checksums.txt`.

With Go 1.25 or newer:

```sh
go install github.com/supremeinferno/dockdoctor@latest
```

## Use

```sh
dockdoctor                         # check Docker, your containers and the Compose file here
dockdoctor -f path/to/compose.yaml # check a specific Compose file (repeat -f to merge several)
dockdoctor -v                      # also list what passed and what was skipped
dockdoctor -json                   # machine-readable, for scripts and coding agents
```

The exit code is `1` when it finds a problem, so you can run it in CI or before `docker compose up`.

## What it checks

| Check | What goes wrong without it |
|---|---|
| **Daemon reachable** | Tells apart "daemon not running" from "your user can't use the socket", with the fix for each |
| **Snap Docker** | Ubuntu's snap only reaches your home directory: mounts in `/opt` or `/srv` fail with `read-only file system` |
| **Compose v2** | The old `docker-compose` v1 can't read current Compose files |
| **Disk space** | A full Docker disk breaks pulls, databases and logs |
| **Log size limits** | The default `json-file` driver keeps every log line forever until the disk fills |
| **Restart loops and OOM kills** | Explains the exit code (127 = command not found, 137 = killed, 139 = wrong architecture…) |
| **CPU architecture** | An `amd64`-only image on a Raspberry Pi or ARM VPS dies with `exec format error`, or runs slowly under emulation |
| **Unhealthy containers** | Shows the healthcheck's last output, the reason `service_healthy` dependents never start |
| **UFW bypass** | Docker's iptables rules sit in front of UFW, so `ufw deny` doesn't protect published ports |
| **Compose file valid** | Shows Compose's own error |
| **Unset variables** | `${DB_PASSWORD}` silently becomes an empty string |
| **host.docker.internal** | Defined by Docker Desktop, not by Docker Engine on Linux, unless you add `host-gateway` |
| **Ports free** | Finds what already holds a port before the container fails with `address already in use` |
| **Bind mount sources** | A missing `./config.yml` becomes an empty root-owned *directory* |
| **Rootless ports** | Rootless Docker can't publish ports below 1024 without a sysctl |

Every check comes from a problem people actually ran into and asked about.

## Missing the problem you hit?

That's how new checks get added. [Open an issue](https://github.com/supremeinferno/dockdoctor/issues/new) with the error message and what turned out
to be the cause.

Want to write one? Each check is a small Go function with table tests, in
[`internal/doctor`](internal/doctor). Good places to start:

- [Issues labelled `good first issue`](https://github.com/supremeinferno/dockdoctor/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22)
- [Ideas for new checks](https://github.com/supremeinferno/dockdoctor/issues?q=is%3Aissue+is%3Aopen+label%3A%22new+check%22)

## License

[MIT](LICENSE)
