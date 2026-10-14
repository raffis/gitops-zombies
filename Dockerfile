FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
WORKDIR /
COPY gitops-zombies /usr/bin/gitops-zombies
USER 65532:65532

ENTRYPOINT ["/usr/bin/gitops-zombies"]
