FROM alpine:3.24@sha256:a2d49ea686c2adfe3c992e47dc3b5e7fa6e6b5055609400dc2acaeb241c829f4
WORKDIR /
COPY gitops-zombies /usr/bin/gitops-zombies
USER 65532:65532

ENTRYPOINT ["/usr/bin/gitops-zombies"]
