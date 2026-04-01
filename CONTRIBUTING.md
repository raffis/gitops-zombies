## Release process

### Create release
1. Merge all pr's to master which need to be part of the new release
2. Create pr to master with kustomization bump (new deployment version)
3. Push a tag following semantic versioning prefixed by 'v'. Do not create a github release, this is done automatically.
4. Create a new pr and bump the helm chart version as well as the appVersion