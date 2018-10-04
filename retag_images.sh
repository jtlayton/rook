#!/bin/bash

. ./build/common.sh

GOHOSTARCH=$(go env GOHOSTARCH)
docker image ls | grep "^${BUILD_REGISTRY}" |
while read line; do
	fullname=$(echo "$line" | cut -d' ' -f1)
	name=$(echo "$fullname" | cut -d/ -f2 | sed s/-${GOHOSTARCH}$//)
	docker tag $fullname rook/$name:master
done
