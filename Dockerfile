FROM alpine:3.23@sha256:5b10f432ef3da1b8d4c7eb6c487f2f5a8f096bc91145e68878dd4a5019afde11
WORKDIR /
COPY gitops-zombies /usr/bin/gitops-zombies
USER 65532:65532

ENTRYPOINT ["/usr/bin/gitops-zombies"]
