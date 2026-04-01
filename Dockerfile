FROM alpine:3.23@sha256:25109184c71bdad752c8312a8623239686a9a2071e8825f20acb8f2198c3f659
WORKDIR /
COPY gitops-zombies /usr/bin/gitops-zombies
USER 65532:65532

ENTRYPOINT ["/usr/bin/gitops-zombies"]
