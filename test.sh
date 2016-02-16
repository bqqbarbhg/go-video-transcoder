#!/usr/bin/env bash

export LAYERS_API_URI=https://api.learning-layers.eu/
export GOTR_STORAGE_URL_PATH=/uploads/
export GOTR_TEMP_PATH=bin/temp
export GOTR_SERVE_PATH=bin/serve

./go-video-transcoder

