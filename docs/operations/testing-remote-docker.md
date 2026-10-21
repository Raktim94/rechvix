# Running integration tests on a remote Docker host

If the local user cannot access `/var/run/docker.sock`, run the Testcontainers-based suite against
another machine's Docker daemon. Containers are throwaway (random ports, reaped by Ryuk) and never
touch the host's existing containers or volumes.

```bash
S=$(mktemp -d)
ssh -f -N -o ExitOnForwardFailure=yes -L $S/docker.sock:/var/run/docker.sock <ssh-host>
export DOCKER_HOST=unix://$S/docker.sock
export TESTCONTAINERS_HOST_OVERRIDE=<host-ip>        # where mapped ports are reachable
export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock
go test -tags=integration ./... -count=1
```

Notes: Go's SSH client does not read `~/.ssh/config` aliases, hence the socket forward. Do not use a
production host unless the suite is confined to throwaway containers as above.
