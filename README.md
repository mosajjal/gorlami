## Gorlami

There's a video about this if you want to learn the correct pronunciation.
![image](https://github.com/mosajjal/gorlami/assets/10158936/2aa4a308-b01b-4d8f-92f6-825e99695255)


`gorlami` is a web interface for requesting a browser sandbox.
the application (optionally) authenticates with oAtuh2, then presents a form to the user to choose a session length. it then creates a headless container with the browser, using `accetto/ubuntu-vnc-xfce-chromium-g3:latest`.


## Configuration

```yaml
webserver:
  listen: "0.0.0.0:3000"
  enable_tls: false
  tls_cert: "/path/to/cert.pem"
  tls_key: "/path/to/key.pem"
  auth_provider: basic # options: basic, azuread, none
  users: # used only if auth_provider is basic
    "admin": "admin"
    "user": "user"
  azuread_key: "AZUREAD_KEY" # used only if auth_provider is azuread
  azuread_secret: "AZUREAD_SECRET" # used only if auth_provider is azuread
  azuread_callback: "http://localhost:3000/auth/azuread/callback" # used only if auth_provider is azuread
  timeout_default: 5m
  timeout_max: 1h

services:
  Chromium:
    provider: "docker" # only option for now
    docker_image: "docker.io/accetto/ubuntu-vnc-xfce-chromium-g3:latest"
    docker_port: "6901"
    docker_port_type: "novnc"
    docker_port_is_tls: false
    entrypoint: ["/usr/bin/tini","--","/dockerstartup/startup.sh"]
  Firefox:
    provider: "docker" # only option for now
    docker_image: "docker.io/accetto/ubuntu-vnc-xfce-firefox-g3:latest"
    docker_port: "6901"
    docker_port_type: "novnc"
    docker_port_is_tls: false
    entrypoint: ["/usr/bin/tini","--","/dockerstartup/startup.sh"]
    env:
      - "A=B"
  Kali:
    provider: "docker" # only option for now
    docker_image: "docker.io/kasmweb/kali-rolling-desktop:1.14.0"
    docker_port: "6901"
    docker_port_type: "kasm" # BUG: not supported YET
    docker_port_is_tls: true
    entrypoint: ["/dockerstartup/kasm_default_profile.sh", "/dockerstartup/vnc_startup.sh", "/dockerstartup/kasm_startup.sh"]
    env:
      - "VNC_PW=headless"
```

## How to run

```
# build the binary using go build (no need for CGO)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags='-s -w' .
# run the binary
./gorlami -config config.yaml
```

