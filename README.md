# GitOps zombies

This simple tool will help you find kubernetes resources which are not managed via GitOps (flux2).

<p align="center"><img src="https://github.com/raffis/gitops-zombies/blob/main/assets/logo.png?raw=true" alt="logo"/></p>

## How does it work?

It will discover all apis installed on a cluster and identify resources which are not part of a Kustomization or a HelmRelease.
The app will also acknowledge the following things:

* Ignores resources which are owned by a parent resource (For example pods which are created by a deployment)
* Ignores resources which are considered dynamic (metrics, leases, events, endpoints, ...)
* Filter out resources which are created by the apiserver itself (like default rbacs)
* Filters secrets which are managed by other parties including helm or ServiceAccount tokens
* Checks if the referenced HelmRelease or Kustomization exists


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

## CLI reference
```
Finds all kubernetes resources from all installed apis on a kubernetes cluster and evaluates whether they are managed by a flux kustomization or a helmrelease.

Usage:
  gitops-zombies [flags]

Flags:
      --cluster-as string                      Username to impersonate for the operation
      --cluster-as-group stringArray           Group to impersonate for the operation, this flag can be repeated to specify multiple groups.
      --cluster-as-uid string                  UID to impersonate for the operation
      --cluster-certificate-authority string   Path to a cert file for the certificate authority
      --cluster-client-certificate string      Path to a client certificate file for TLS
      --cluster-client-key string              Path to a client key file for TLS
      --cluster-cluster string                 The name of the kubeconfig cluster to use
      --cluster-context string                 The name of the kubeconfig context to use
      --cluster-insecure-skip-tls-verify       If true, the server's certificate will not be checked for validity. This will make your HTTPS connections insecure
  -n, --cluster-namespace string               If present, the namespace scope for this CLI request
      --cluster-password string                Password for basic authentication to the API server
      --cluster-proxy-url string               If provided, this URL will be used to connect via proxy
      --cluster-request-timeout string         The length of time to wait before giving up on a single server request. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h). A value of zero means don't timeout requests. (default "0")
      --cluster-server string                  The address and port of the Kubernetes API server
      --cluster-tls-server-name string         If provided, this name will be used to validate server certificate. If this is not provided, hostname used to contact the server is used.
      --cluster-token string                   Bearer token for authentication to the API server
      --cluster-user string                    The name of the kubeconfig user to use
      --cluster-username string                Username for basic authentication to the API server
      --gitops-as string                       Username to impersonate for the operation
      --gitops-as-group stringArray            Group to impersonate for the operation, this flag can be repeated to specify multiple groups.
      --gitops-as-uid string                   UID to impersonate for the operation
      --gitops-certificate-authority string    Path to a cert file for the certificate authority
      --gitops-client-certificate string       Path to a client certificate file for TLS
      --gitops-client-key string               Path to a client key file for TLS
      --gitops-cluster string                  The name of the kubeconfig cluster to use
      --gitops-context string                  The name of the kubeconfig context to use
      --gitops-insecure-skip-tls-verify        If true, the server's certificate will not be checked for validity. This will make your HTTPS connections insecure
      --gitops-namespace string                If present, the namespace scope for this CLI request
      --gitops-password string                 Password for basic authentication to the API server
      --gitops-proxy-url string                If provided, this URL will be used to connect via proxy
      --gitops-request-timeout string          The length of time to wait before giving up on a single server request. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h). A value of zero means don't timeout requests. (default "0")
      --gitops-server string                   The address and port of the Kubernetes API server
      --gitops-tls-server-name string          If provided, this name will be used to validate server certificate. If this is not provided, hostname used to contact the server is used.
      --gitops-token string                    Bearer token for authentication to the API server
      --gitops-user string                     The name of the kubeconfig user to use
      --gitops-username string                 Username for basic authentication to the API server
  -h, --help                                   help for gitops-zombies
  -a, --include-all                            Includes resources which are considered dynamic resources
      --kubeconfig string                      Path to the kubeconfig file to use for CLI requests. (default "/home/johndoe/.kube/config")
  -o, --output string                          Output format. One of: (json, yaml, name, go-template, go-template-file, template, templatefile, jsonpath, jsonpath-as-json, jsonpath-file, custom-columns, custom-columns-file, wide). See custom columns [https://kubernetes.io/docs/reference/kubectl/overview/#custom-columns], golang template [http://golang.org/pkg/text/template/#pkg-overview] and jsonpath template [https://kubernetes.io/docs/reference/kubectl/jsonpath/].
  -l, --selector string                        Label selector (is used for all apis)
  -v, --verbose                                Verbose mode (logged to stderr)
      --version                                Print version and exit
```
