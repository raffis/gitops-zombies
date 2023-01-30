#!/bin/bash -e

set -e -o pipefail

PROJECT_MODULE="github.com/raffis/gitops-zombies"
IMAGE_NAME="kubernetes-codegen:latest"

echo "Building codegen Docker image..."
docker build --build-arg KUBE_VERSION=v0.26.1 --build-arg USER=$USER -f "./hack/code-gen.Dockerfile" \
             -t "${IMAGE_NAME}" \
             "."

cmd="/go/src/k8s.io/code-generator/generate-groups.sh deepcopy $PROJECT_MODULE/pkg/client $PROJECT_MODULE/pkg/apis gitopszombies:v1 --go-header-file /go/src/k8s.io/code-generator/hack/boilerplate.go.txt"
echo "Generating clientSet code ..."
echo $(pwd)
docker run --rm \
           -v "$(pwd):/go/src/${PROJECT_MODULE}" \
           -w "/go/src/${PROJECT_MODULE}" \
           "${IMAGE_NAME}" $cmd
