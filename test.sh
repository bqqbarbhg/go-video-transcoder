#!/usr/bin/env bash

export AUTH_URI=http://10.11.12.13:9292/oidc/userinfo
export GOTR_URI=https://govitra.com
export GOTR_STORAGE_URL_PATH=/uploads/
export GOTR_API_URL_PATH=/api/
export GOTR_TEMP_PATH=bin/temp
export GOTR_SERVE_PATH=bin/serve
export GOTR_FAST_TRANSCODE_THREADS=8
export GOTR_SLOW_TRANSCODE_THREADS=2

# AWS-specific environment variables
export USE_AWS=0
export AWS_BUCKET_NAME=aalt-achso
export AWS_ACCESS_KEY_ID=id
export AWS_SECRET_ACCESS_KEY=key

mkdir -p $GOTR_TEMP_PATH
mkdir -p $GOTR_SERVE_PATH

./go-video-transcoder

