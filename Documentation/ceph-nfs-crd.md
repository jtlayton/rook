---
title: NFS CRD
weight: 31
indent: true
---

# Ceph NFS Gateway CRD


## Overview
Rook allows exporting NFS shares of the filesystem or object store through the CephNFS custom resource definition. This will spin up a cluster of [NFS Ganesha](https://github.com/nfs-ganesha/nfs-ganesha) servers that coordinate with one another via shared RADOS objects. The servers will be configured for NFSv4.1+ access, as serving earlier protocols can inhibit responsiveness after a server restart.

## Samples

This configuration adds a cluster of ganesha gateways that store objects in the pool cephfs.a.meta and the namespace **

```yaml
apiVersion: ceph.rook.io/v1
kind: CephNFS
metadata:
  name: my-nfs
  namespace: rook-ceph
spec:
  RADOS:
    # RADOS pool where NFS client recovery data is stored.
    # In this example the data pool for the "myfs" filesystem is used.
    # If using the object store example, the data pool would be "my-store.rgw.buckets.data".
    pool: myfs-data0
    # RADOS namespace where NFS client recovery data is stored in the pool.
    namespace: nfs-ns
  # Settings for the NFS server
  server:
    # the number of active NFS servers
    active: 2
    # where to run the NFS server
    placement:
    #  nodeAffinity:
    #    requiredDuringSchedulingIgnoredDuringExecution:
    #      nodeSelectorTerms:
    #      - matchExpressions:
    #        - key: role
    #          operator: In
    #          values:
    #          - mds-node
    #  tolerations:
    #  - key: mds-node
    #    operator: Exists
    #  podAffinity:
    #  podAntiAffinity:
    # The requests and limits set here allow the ganesha pod(s) to use half of one CPU core and 1 gigabyte of memory
    resources:
    #  limits:
    #    cpu: "500m"
    #    memory: "1024Mi"
    #  requests:
    #    cpu: "500m"
    #    memory: "1024Mi"
```

## NFS Settings

### RADOS Settings

- `pool`: The pool where ganesha recovery backend and supplimental configuration objects will be stored
- `namespace`: The namespace in `pool` where ganesha recovery backend and supplimental configuration objects will be stored

## EXPORT Block Configuration

Each daemon will have a stock configuration with no exports defined, and that includes a RADOS object via:

```
%url	rados://<pool>/<namespace>/conf-<nodeid>
```

The pool and namespace are configured via the spec's RADOS block. The nodeid is a value automatically assigned internally by rook. Nodeids start with "a" and go through "z", at which point they become two letters ("aa" to "az").

When a server is started, it will create the included object if it does not already exist. It is possible to prepopulate the included objects prior to starting the server. The format for these objects is documented in the [NFS Ganesha](https://github.com/nfs-ganesha/nfs-ganesha/wiki) project.
