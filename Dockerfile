FROM ubuntu:25.10

COPY wackypub /bin

RUN mkdir /ws

WORKDIR /ws

# Install the CA certificates package
RUN apt-get update && apt-get install -y ca-certificates python3 golang-go nodejs npm && rm -rf /var/lib/apt/lists/*

ENTRYPOINT [ "/usr/bin/bash" ]