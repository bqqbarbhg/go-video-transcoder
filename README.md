Go Video Transcoder - Govitra
=============================

Provides a simple API for uploading videos to be transcoded.

Note: This does not actually implement the serving part. You need some other server, for example Nginx,
to serve the uploaded content.

## API

The API is very simple and has only two endpoints, one for uploading and one for deleting.

### Uploading

`POST /uploads` with raw video data in body.

```json
{
    "video": "$host/$id.mp4",
    "thumbnail": "$host/$id.jpg",
    "deleteUrl": "$self/uploads/$id"
}
```
or
```json
{ "error": "Human readable error description" }
```

### Deleting

`DELETE /uploads/$id`

These URLs should not be made by hand as the ID is not a real thing,
but the URL can be retrieved from the upload JSON response `deleteUrl`

Returns `204 No Content`
or
```json
{ "error": "Human readable error description" }
```

## Production setup

#### Dependencies

- [avconv](https://libav.org/avconv.html) for transcoding
- [exiftool](http://owl.phy.queensu.ca/~phil/exiftool/) for detecting video rotation

#### Environment variables

Govitra itself requires some amount of configuration to run, this is provided as environment variables.
It also requires a server to serve the actual video files. Note: You forbid the server from serving
`.owner` files as they are used to store who uploaded the video.

- Common:
    - `GOTR_TEMP_PATH`: Path to download and process videos in
    - `GOTR_SERVE_PATH`: Path to copy transcoded videos. _Needs_ to be in the same
    mount as `GOTR_TEMP_PATH` since the processed videos are renamed to here when done.
    - `GOTR_STORAGE_URL_PATH`: Base path appeneded to `GOTR_URI` or `LAYERS_API_URI`
    that serves files from `GOTR_SERVE_PATH`
    - `GOTR_API_URL_PATH`: Base path appended to `GOTR_UR` or `LAYERS_API_URI` that
    is used for the API calls
- Layers Box:
    - `LAYERS_API_URI`: URL of the box (should be predefined by Layers Box)
    - `AUTH_URL_PATH`: Path appended to `LAYERS_API_URI` for the authentication `/userinfo` endpoint
- Standalone:
    - `GOTR_URI`: URL of this server
    - `AUTH_URI`: URL of the authentication [OIDC `/userinfo` endpoint](http://openid.net/specs/openid-connect-core-1_0.html#UserInfo)

### Example setup:

Dependencies and server:
```
apt-get update

# Golang (and git for 'go get')
apt-get install -y golang git

# Govitra dependencies
apt-get install -y libav-tools exiftool

# Download and build Govitra
go get github.com/bqqbarbhg/go-video-transcoder
```

Govitra environment:
```
GOTR_TEMP_PATH = /var/govitra-uploads/temp
GOTR_SERVE_PATH = /var/govitra-uploads/serve

GOTR_STORAGE_URL_PATH=/govitra-videos/
GOTR_API_URL_PATH=/govitra-api/

GOTR_URI=https://server.com
AUTH_URI=https://server.com/achrails/oidc/auth
```

Server configuration (nginx):
```
# Proxy the API to Govitra
location /govitra-api/ {
    client_max_body_size 0;
    proxy_pass http://localhost:8080/;
}

# Serve the uploaded files
location /govitra-videos/ {
    root /var/govitra-uploads/data;
    try_files $uri =404;

    # Deny .owner-files
    location ~ \.owner$ {
        deny all;
        return 404;
    }
}
```

Now the API should be hosted at `https://server.com/govitra-api/`
and videos at `https://server.com/govitra-videos/`

## Development setup

- Get the [production dependencies](#dependencies)
- Clone [achrails](https://github.com/learning-layers/achrails)
- Follow the [achrails development setup](https://github.com/learning-layers/achrails#development-setup)
- Clone this repository and `cd` into it
- Build Govitra: `go build`
- To run it for testing: `./test.sh`, this points Govitra to the default achrails address `http://10.11.12.13:9292`
- Go to `10.11.12.13:9292/oidc/authorize?response_type=code&client_id=client&redirect_uri=http://example.com` and authenticate using the Developer authentication
- After being redirected to `http://example.com?code=<CODE>` copy the `<CODE>`
- Request `POST http://10.11.12.13:9292/oidc/token` with `code=<CODE>&client_id=client&client_secret=secret&grant_type=authorization_code` and copy the access token
- Now you can do authenticated requests to `localhost:8080` using the header `Authorization: Bearer <ACCESS-TOKEN>`

## Development

Made for [Ach so!](http://achso.aalto.fi) and [Learning Layers](http://learning-layers.eu)
in [Aalto University](http://www.aalto.fi/en/)

#### Authors

- Samuli Raivio (@bqqbarbhg)

## Licence

```
The MIT License (MIT)

Copyright © 2016 Aalto University

Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the "Software"), to deal in
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies
of the Software, and to permit persons to whom the Software is furnished to do
so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
