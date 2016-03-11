#!/usr/bin/env bash

export AUTH_URI=http://10.11.12.13:9292/userinfo
export GOTR_URI=https://govitra.com
export GOTR_STORAGE_URL_PATH=/uploads/
export GOTR_API_URL_PATH=/api/
export GOTR_TEMP_PATH=bin/temp
export GOTR_SERVE_PATH=bin/serve

mkdir -p $GOTR_TEMP_PATH
mkdir -p $GOTR_SERVE_PATH

./go-video-transcoder

