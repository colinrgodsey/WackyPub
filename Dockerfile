FROM ubuntu:25.10

COPY wackypub /bin

RUN mkdir /ws

WORKDIR /ws

# ca-certificates: HTTPS to model backends. patch: files-rw's `patch` subcommand
# shells out to it - without it, that whole attack surface goes untested in any
# swarm pen-test run (see docs/SWARM_TESTING.md).
RUN apt-get update && apt-get install -y ca-certificates patch python3 golang-go nodejs npm && rm -rf /var/lib/apt/lists/*

ENTRYPOINT [ "/usr/bin/bash" ]