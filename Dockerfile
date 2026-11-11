FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
WORKDIR /
COPY gitops-zombies /usr/bin/gitops-zombies
USER 65532:65532

ENTRYPOINT ["/usr/bin/gitops-zombies"]
