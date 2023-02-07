# GitOps zombies

![Release](https://img.shields.io/github/v/release/raffis/gitops-zombies)
[![release](https://github.com/raffis/gitops-zombies/actions/workflows/release.yaml/badge.svg)](https://github.com/raffis/gitops-zombies/actions/workflows/release.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/raffis/gitops-zombies)](https://goreportcard.com/report/github.com/raffis/gitops-zombies)
[![Coverage Status](https://coveralls.io/repos/github/raffis/gitops-zombies/badge.svg?branch=main)](https://coveralls.io/github/raffis/gitops-zombies?branch=main)

This simple tool will help you find kubernetes resources which are not managed via GitOps (flux2).

<p align="center"><img src="https://github.com/raffis/gitops-zombies/blob/main/assets/logo.png?raw=true" alt="logo"/></p>

## How does it work?

gitops-zombies discovers all apis installed on a cluster and identify resources which are not part of a Kustomization or a HelmRelease.
It also acknowledges the following facts:

* Ignores resources which are owned by a parent resource (For example pods which are created by a deployment)
* Ignores resources which are considered dynamic (metrics, leases, events, endpoints, ...)
* Filter out resources which are created by the apiserver itself (like default rbacs)
* Filters secrets which are managed by other parties including helm or ServiceAccount tokens
* Checks if the referenced HelmRelease or Kustomization exists
* Checks if resources are still part of the kustomization inventory


## How do I install it?

```
brew tap raffis/gitops-zombies
brew install gitops-zombies
```

## How to use

```
gitops-zombies
```

A more advanced call might include a filter like the following to exclude certain resources which are considered dynamic (besides the builtin exclusions):
```
gitops-zombies --context staging -l app.kubernetes.io/managed-by!=kops,app.kubernetes.io/name!=velero,io.cilium.k8s.policy.cluster!=default
```

Also you might want to exclude some specific resources based on their names. It can be achieved through YAML configuration:
```yaml
---
apiVersion: gitopszombies/v1
kind: Config
excludeResources:
- name: default
  apiVersion: v1
  kind: ServiceAccount
- name: velero-capi-backup-.*
  namespace: velero
  apiVersion: velero.io/v1
  kind: Backup
  cluster: management
```

## CLI reference

```
Finds all kubernetes resources from all installed apis on a kubernetes cluste and evaluates whether they are managed by a flux kustomization or a helmrelease.

Usage:
  gitops-zombies [flags]

Flags:
      --add_dir_header                   If true, adds the file directory to the header of the log messages
      --alsologtostderr                  log to standard error as well as files (no effect when -logtostderr=true)
      --as string                        Username to impersonate for the operation. User could be a regular user or a service account in a namespace.
      --as-group stringArray             Group to impersonate for the operation, this flag can be repeated to specify multiple groups.
      --as-uid string                    UID to impersonate for the operation.
      --cache-dir string                 Default cache directory (default "~/.kube/cache")
      --certificate-authority string     Path to a cert file for the certificate authority
      --client-certificate string        Path to a client certificate file for TLS
      --client-key string                Path to a client key file for TLS
      --cluster string                   The name of the kubeconfig cluster to use
      --config string                    Config file (default "~/.gitops-zombies.yaml")
      --context string                   The name of the kubeconfig context to use
      --disable-compression              If true, opt-out of response compression for all requests to the server
      --exclude-cluster strings          Exclude cluster from zombie detection (default none)
      --fail                             Exit with an exit code > 0 if zombies are detected
  -h, --help                             help for gitops-zombies
  -a, --include-all                      Includes resources which are considered dynamic resources
      --insecure-skip-tls-verify         If true, the server's certificate will not be checked for validity. This will make your HTTPS connections insecure
      --kubeconfig string                Path to the kubeconfig file to use for CLI requests.
      --log_backtrace_at traceLocation   when logging hits line file:N, emit a stack trace (default :0)
      --log_dir string                   If non-empty, write log files in this directory (no effect when -logtostderr=true)
      --log_file string                  If non-empty, use this log file (no effect when -logtostderr=true)
      --log_file_max_size uint           Defines the maximum size a log file can grow to (no effect when -logtostderr=true). Unit is megabytes. If the value is 0, the maximum file size is unlimited. (default 1800)
      --logtostderr                      log to standard error instead of files (default true)
  -n, --namespace string                 If present, the namespace scope for this CLI request
      --no-stream                        Display discovered resources at the end instead of live
      --one_output                       If true, only write logs to their native severity level (vs also writing to each lower severity level; no effect when -logtostderr=true)
  -o, --output string                    Output format. One of: (json, yaml, name, go-template, go-template-file, template, templatefile, jsonpath, jsonpath-as-json, jsonpath-file, custom-columns, custom-columns-file, wide). See custom columns [https://kubernetes.io/docs/reference/kubectl/overview/#custom-columns], golang template [http://golang.org/pkg/text/template/#pkg-overview] and jsonpath template [https://kubernetes.io/docs/reference/kubectl/jsonpath/].
      --request-timeout string           The length of time to wait before giving up on a single server request. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h). A value of zero means don't timeout requests. (default "0")
  -l, --selector string                  Label selector (Is used for all apis)
  -s, --server string                    The address and port of the Kubernetes API server
      --skip_headers                     If true, avoid header prefixes in the log messages
      --skip_log_headers                 If true, avoid headers when opening log files (no effect when -logtostderr=true)
      --stderrthreshold severity         logs at or above this threshold go to stderr when writing to files and stderr (no effect when -logtostderr=true or -alsologtostderr=false) (default 2)
      --tls-server-name string           Server name to use for server certificate validation. If it is not provided, the hostname used to contact the server is used
      --token string                     Bearer token for authentication to the API server
      --user string                      The name of the kubeconfig user to use
  -v, --v Level                          number for the log level verbosity
      --version                          Print version and exit
      --vmodule moduleSpec               comma-separated list of pattern=N settings for file-filtered logging
```
