FROM golang:1.20-buster

ARG USER=$USER
ARG UID=1000
ARG GID=1000
RUN useradd -m ${USER} --uid=${UID} && echo "${USER}:" chpasswd
USER ${UID}:${GID}

ARG KUBE_VERSION

RUN go install k8s.io/code-generator@$KUBE_VERSION; exit 0
RUN go install k8s.io/apimachinery@$KUBE_VERSION; exit 0
RUN go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.11.1; exit 0

RUN mkdir -p $GOPATH/src/k8s.io/code-generator,apimachinery}
RUN cp -R $GOPATH/pkg/mod/k8s.io/code-generator@$KUBE_VERSION $GOPATH/src/k8s.io/code-generator
RUN cp -R $GOPATH/pkg/mod/k8s.io/apimachinery@$KUBE_VERSION $GOPATH/src/k8s.io/apimachinery
RUN chmod +x $GOPATH/src/k8s.io/code-generator/generate-groups.sh

WORKDIR $GOPATH/src/k8s.io/code-generator
